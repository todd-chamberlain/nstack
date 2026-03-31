package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/todd-chamberlain/nstack/pkg/discover"
)

var discoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Discover hosts on a network for deployment",
	Long: `Scan a network range to find bare metal hosts, VMs, and existing K8s clusters.
Classifies each host and generates a site config.

Probes each host via:
  - IPMI/Redfish (ports 443/623) for BMC detection
  - SSH (port 22) for OS, hardware, and K8s detection
  - K8s API (ports 6443/16443) for cluster detection

Does not require an existing kubeconfig or cluster connection.`,
	RunE: runDiscover,
}

func init() {
	discoverCmd.Flags().String("network", "", "CIDR range to scan (e.g., 10.0.0.0/24)")
	discoverCmd.Flags().String("ssh-user", "root", "SSH username")
	discoverCmd.Flags().String("ssh-key", "", "Path to SSH private key (default: ~/.ssh/id_ed25519 or ~/.ssh/id_rsa)")
	discoverCmd.Flags().String("ssh-pass", "", "SSH password (alternative to key)")
	discoverCmd.Flags().String("bmc-user", "", "BMC username for IPMI/Redfish")
	discoverCmd.Flags().String("bmc-pass", "", "BMC password for IPMI/Redfish")
	discoverCmd.Flags().Int("timeout", 10, "Per-host timeout in seconds")
	discoverCmd.Flags().Int("workers", 32, "Concurrent scan workers")
	discoverCmd.Flags().Bool("write-config", false, "Write generated config to ~/.nstack/config.yaml (or --config path)")

	_ = discoverCmd.MarkFlagRequired("network")
}

func runDiscover(cmd *cobra.Command, args []string) error {
	network, _ := cmd.Flags().GetString("network")
	sshUser, _ := cmd.Flags().GetString("ssh-user")
	sshKey, _ := cmd.Flags().GetString("ssh-key")
	sshPass, _ := cmd.Flags().GetString("ssh-pass")
	bmcUser, _ := cmd.Flags().GetString("bmc-user")
	bmcPass, _ := cmd.Flags().GetString("bmc-pass")
	timeout, _ := cmd.Flags().GetInt("timeout")
	workers, _ := cmd.Flags().GetInt("workers")
	writeConfig, _ := cmd.Flags().GetBool("write-config")

	// Resolve SSH key path
	if sshKey == "" {
		sshKey = resolveSSHKey()
	}

	opts := discover.ScanOptions{
		Network:    network,
		SSHUser:    sshUser,
		SSHKeyPath: sshKey,
		SSHPass:    sshPass,
		BMCUser:    bmcUser,
		BMCPass:    bmcPass,
		Timeout:    timeout,
		Workers:    workers,
	}

	printer := createPrinter()
	format := viper.GetString("output")

	// Count IPs for status message (approximate from CIDR)
	printer.Infof("Scanning %s (%d workers)...", network, workers)

	hosts, err := discover.Scan(cmd.Context(), opts)
	if err != nil {
		return fmt.Errorf("discovery failed: %w", err)
	}

	if len(hosts) == 0 {
		printer.Infof("No hosts found on %s", network)
		return nil
	}

	// Output results
	if format == "json" {
		return printDiscoverJSON(hosts)
	}

	printDiscoverTable(hosts)

	// Print site recommendations
	recs := discover.GroupHosts(hosts)
	if len(recs) > 0 {
		fmt.Println()
		fmt.Println("Recommended sites:")
		for _, rec := range recs {
			ipList := make([]string, 0, len(rec.Hosts))
			for _, h := range rec.Hosts {
				ipList = append(ipList, h.IP)
			}
			ipRange := strings.Join(ipList, ", ")
			if len(ipList) > 3 {
				ipRange = fmt.Sprintf("%s...%s", ipList[0], ipList[len(ipList)-1])
			}

			fmt.Printf("\n  %s (%s): %s\n", rec.Name, ipRange, rec.Summary)
			fmt.Printf("    Profile: %s | Entry: %s | nstack deploy --site %s --from %s\n",
				rec.Profile, capitalize(rec.FromStage), rec.Name, rec.FromStage)
		}
	}

	// Write config if requested
	if writeConfig {
		cfgContent, err := discover.GenerateConfig(hosts, opts)
		if err != nil {
			return fmt.Errorf("generating config: %w", err)
		}

		cfgPath := viper.GetString("config")
		if cfgPath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("finding home directory: %w", err)
			}
			cfgPath = filepath.Join(home, ".nstack", "config.yaml")
		}

		// Ensure directory exists
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
			return fmt.Errorf("creating config directory: %w", err)
		}

		if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}

		fmt.Printf("\nConfig written to %s\n", cfgPath)
	}

	return nil
}

func printDiscoverJSON(hosts []discover.DiscoveredHost) error {
	data, err := json.MarshalIndent(hosts, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func printDiscoverTable(hosts []discover.DiscoveredHost) {
	fmt.Println()
	fmt.Printf("%-15s %-10s %-15s %-20s %-15s %-15s %s\n",
		"IP", "TYPE", "ACCESS", "OS", "GPUs", "K8s", "ENTRY POINT")

	for _, h := range hosts {
		hostType := "VM"
		if h.IsPhysical {
			hostType = "Physical"
		}
		if !h.HasSSH && !h.IsPhysical {
			hostType = "(unknown)"
		}

		access := buildAccessStr(h)
		osName := h.OS
		if osName == "" {
			osName = "(no OS)"
		}
		if len(osName) > 18 {
			osName = osName[:18] + ".."
		}

		gpuStr := "None"
		if len(h.GPUs) > 0 {
			parts := make([]string, 0, len(h.GPUs))
			for _, g := range h.GPUs {
				parts = append(parts, fmt.Sprintf("%dx %s", g.Count, shortenGPUName(g.Model)))
			}
			gpuStr = strings.Join(parts, ", ")
		}
		if len(gpuStr) > 13 {
			gpuStr = gpuStr[:13] + ".."
		}

		k8sStr := "None"
		if h.HasK8s {
			k8sStr = h.K8sDistro
			if h.K8sVersion != "" {
				k8sStr += " " + h.K8sVersion
			}
		}
		if len(k8sStr) > 13 {
			k8sStr = k8sStr[:13] + ".."
		}

		entryStr := fmt.Sprintf("%s -> Stage %s", h.EntryPoint, h.RecommendedStages)

		fmt.Printf("%-15s %-10s %-15s %-20s %-15s %-15s %s\n",
			h.IP, hostType, access, osName, gpuStr, k8sStr, entryStr)
	}
}

func buildAccessStr(h discover.DiscoveredHost) string {
	var parts []string
	if h.HasBMC {
		parts = append(parts, "BMC")
	}
	if h.HasSSH {
		parts = append(parts, "SSH")
	}
	if len(parts) == 0 {
		if h.HasK8s {
			return "K8s API"
		}
		return "(none)"
	}
	return strings.Join(parts, "+")
}

func shortenGPUName(model string) string {
	// Try to extract just the key part (e.g., "H100" from "NVIDIA H100 80GB HBM3")
	model = strings.TrimPrefix(model, "NVIDIA ")
	if idx := strings.Index(model, " "); idx > 0 && idx < 15 {
		return model[:idx]
	}
	if len(model) > 12 {
		return model[:12]
	}
	return model
}

func resolveSSHKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// Try ed25519 first, then RSA
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		path := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
