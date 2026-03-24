package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/todd-chamberlain/nstack/pkg/config"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize NStack configuration",
	Long:  "Create ~/.nstack/config.yaml with site configuration.",
	RunE:  runInit,
}

func init() {
	initCmd.Flags().String("profile", "", "profile to use (e.g., k3s-single, kubeadm-ha, nebius-slurm)")
	initCmd.Flags().String("kubeconfig", "", "path to kubeconfig file")
}

func runInit(cmd *cobra.Command, args []string) error {
	siteName, _ := cmd.Flags().GetString("site")
	profile, _ := cmd.Flags().GetString("profile")
	kubeconfig, _ := cmd.Flags().GetString("kubeconfig")

	// If all flags provided, run non-interactively.
	if siteName != "" && profile != "" && kubeconfig != "" {
		return writeConfig(siteName, profile, kubeconfig)
	}

	// Interactive mode: prompt for missing values.
	if siteName == "" {
		fmt.Print("Site name: ")
		fmt.Scanln(&siteName)
		siteName = strings.TrimSpace(siteName)
		if siteName == "" {
			return fmt.Errorf("site name is required")
		}
	}

	if profile == "" {
		profiles := config.ListProfiles()
		fmt.Println("Available profiles:")
		for i, p := range profiles {
			fmt.Printf("  [%d] %s\n", i+1, p)
		}
		fmt.Print("Profile (name or number): ")
		var input string
		fmt.Scanln(&input)
		input = strings.TrimSpace(input)

		// Check if it's a number.
		var idx int
		if _, err := fmt.Sscanf(input, "%d", &idx); err == nil && idx >= 1 && idx <= len(profiles) {
			profile = profiles[idx-1]
		} else {
			profile = input
		}
		if profile == "" {
			return fmt.Errorf("profile is required")
		}
	}

	if kubeconfig == "" {
		defaultKC := filepath.Join(os.Getenv("HOME"), ".kube", "config")
		fmt.Printf("Kubeconfig path [%s]: ", defaultKC)
		fmt.Scanln(&kubeconfig)
		kubeconfig = strings.TrimSpace(kubeconfig)
		if kubeconfig == "" {
			kubeconfig = defaultKC
		}
	}

	return writeConfig(siteName, profile, kubeconfig)
}

func writeConfig(siteName, profile, kubeconfig string) error {
	cfg := config.Config{
		Version: "v1",
		Sites: map[string]*config.Site{
			siteName: {
				Profile:    profile,
				Kubeconfig: kubeconfig,
			},
		},
	}

	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	configDir := filepath.Join(os.Getenv("HOME"), ".nstack")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	fmt.Printf("Configuration written to %s\n", configPath)
	fmt.Printf("  Site:       %s\n", siteName)
	fmt.Printf("  Profile:    %s\n", profile)
	fmt.Printf("  Kubeconfig: %s\n", kubeconfig)
	return nil
}
