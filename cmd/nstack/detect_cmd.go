package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/todd-chamberlain/nstack/pkg/detect"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

var detectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Detect cluster hardware and configuration",
	Long:  "Scan the target cluster for Kubernetes version, GPUs, operators, and storage classes.",
	RunE:  runDetect,
}

func runDetect(cmd *cobra.Command, args []string) error {
	if err := requireSite(); err != nil {
		return err
	}

	site, _, err := loadSiteAndProfile()
	if err != nil {
		return err
	}

	kc, _, err := createClients(site)
	if err != nil {
		return err
	}

	printer := createPrinter()
	printer.Infof("Detecting cluster configuration for site %q...", site.Name)

	result, err := detect.Run(cmd.Context(), kc.Clientset())
	if err != nil {
		return fmt.Errorf("detection failed: %w", err)
	}

	return printDetectResult(result, printer)
}

func printDetectResult(result *detect.Result, printer *output.Printer) error {
	// JSON mode: emit as structured JSON.
	format := viper.GetString("output")
	if format == "json" {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	// Text mode.
	fmt.Println()
	fmt.Println("Kubernetes")
	fmt.Printf("  Distribution:     %s\n", result.Kubernetes.Distribution)
	fmt.Printf("  Version:          %s\n", result.Kubernetes.Version)
	fmt.Printf("  Cgroup Version:   v%d\n", result.Kubernetes.CgroupVersion)
	if result.Kubernetes.DefaultStorageClass != "" {
		fmt.Printf("  Storage Class:    %s\n", result.Kubernetes.DefaultStorageClass)
	}

	if len(result.GPUs) > 0 {
		fmt.Println()
		fmt.Println("GPUs")
		for _, gpu := range result.GPUs {
			fmt.Printf("  %s  %s  x%d  (%s)\n", gpu.Model, gpu.VRAM, gpu.Count, gpu.NodeName)
		}
	} else {
		fmt.Println()
		fmt.Println("GPUs: none detected")
	}

	if len(result.Operators) > 0 {
		fmt.Println()
		fmt.Println("Operators")
		for _, op := range result.Operators {
			ver := op.Version
			if ver == "" {
				ver = "-"
			}
			fmt.Printf("  %-25s %-15s %s\n", op.Name, ver, op.Status)
		}
	}

	if len(result.Storage) > 0 {
		fmt.Println()
		fmt.Println("Storage Classes")
		for _, sc := range result.Storage {
			def := ""
			if sc.IsDefault {
				def = " (default)"
			}
			fmt.Printf("  %-25s %s%s\n", sc.Name, sc.Provisioner, def)
		}
	}

	// Profile recommendation.
	fmt.Println()
	profile := recommendProfile(result)
	fmt.Printf("Recommended profile: %s\n", profile)

	return nil
}

// recommendProfile suggests a profile based on the detection results.
func recommendProfile(result *detect.Result) string {
	switch result.Kubernetes.Distribution {
	case "k3s":
		return "k3s-single"
	case "eks":
		return "eks"
	case "gke":
		return "gke"
	case "aks":
		return "aks"
	case "nebius":
		return "nebius-slurm"
	default:
		return "kubeadm-ha"
	}
}
