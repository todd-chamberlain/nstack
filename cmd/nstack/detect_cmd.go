package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/todd-chamberlain/nstack/pkg/detect"
	"github.com/todd-chamberlain/nstack/pkg/output"
	"github.com/todd-chamberlain/nstack/pkg/stages/s0_discovery"
)

var detectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Detect cluster hardware and configuration",
	Long:  "Scan the target cluster for Kubernetes version, GPUs, operators, and storage classes.\nWith --network, scans a CIDR range for bare-metal BMC endpoints via Redfish/IPMI.",
	RunE:  runDetect,
}

func init() {
	detectCmd.Flags().String("network", "", "CIDR range to scan for BMC endpoints (e.g., 10.0.0.0/24)")
	detectCmd.Flags().String("bmc-user", "admin", "BMC username for IPMI/Redfish")
	detectCmd.Flags().String("bmc-pass", "", "BMC password for IPMI/Redfish")
	detectCmd.Flags().Int("concurrency", 32, "Number of hosts to probe in parallel")
}

func runDetect(cmd *cobra.Command, args []string) error {
	network, _ := cmd.Flags().GetString("network")

	// If --network is provided, run Stage 0 bare-metal discovery instead.
	if network != "" {
		return runBMCDiscovery(cmd, network)
	}

	// Normal cluster detection path.
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

// runBMCDiscovery executes Stage 0 network scanning for BMC endpoints.
func runBMCDiscovery(cmd *cobra.Command, network string) error {
	bmcUser, _ := cmd.Flags().GetString("bmc-user")
	bmcPass, _ := cmd.Flags().GetString("bmc-pass")
	concurrency, _ := cmd.Flags().GetInt("concurrency")

	printer := createPrinter()
	printer.Infof("Scanning %s for BMC endpoints...", network)

	creds := s0_discovery.BMCCredentials{
		Username: bmcUser,
		Password: bmcPass,
	}

	result, err := s0_discovery.ScanNetwork(cmd.Context(), network, creds, concurrency)
	if err != nil {
		return fmt.Errorf("BMC discovery failed: %w", err)
	}

	return printDiscoveryResult(result, printer)
}

// printDiscoveryResult formats and displays BMC discovery results.
func printDiscoveryResult(result *s0_discovery.ScanResult, printer *output.Printer) error {
	format := viper.GetString("output")
	if format == "json" {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	if len(result.Nodes) == 0 {
		printer.Infof("No BMC endpoints found")
		return nil
	}

	fmt.Println()
	fmt.Printf("Discovered %d BMC endpoint(s):\n", len(result.Nodes))
	fmt.Println()

	for _, node := range result.Nodes {
		name := node.Hostname
		if name == "" {
			name = node.BMCAddress
		}
		fmt.Printf("  %-20s %s\n", name, node.BMCAddress)
		fmt.Printf("    Protocol:   %s\n", node.Protocol)
		fmt.Printf("    Power:      %s\n", node.PowerState)

		if node.CPUs > 0 {
			fmt.Printf("    CPUs:       %d\n", node.CPUs)
		}
		if node.MemoryGB > 0 {
			fmt.Printf("    Memory:     %d GB\n", node.MemoryGB)
		}

		for _, gpu := range node.GPUs {
			fmt.Printf("    GPU:        %s x%d\n", gpu.Model, gpu.Count)
		}
		for _, nic := range node.NICs {
			nicInfo := nic.Model
			if nic.Type == "infiniband" {
				nicInfo += " (InfiniBand)"
			}
			fmt.Printf("    NIC:        %s\n", nicInfo)
		}
		fmt.Println()
	}

	return nil
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
