package s3_networking

import (
	"context"
	"fmt"
	"time"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

const (
	networkOperatorChart     = "nvidia/network-operator"
	networkOperatorNamespace = "nvidia-network-operator"
	networkOperatorRelease   = "network-operator"
	networkOperatorVersion   = "25.7.0"
)

// installNetworkOperator deploys the NVIDIA Network Operator via its Helm chart.
// Values are loaded from embedded assets (common + fabric overlay) and merged
// with any user-provided site overrides.
func installNetworkOperator(ctx context.Context, hc *helm.Client, site *config.Site, profile *config.Profile, printer *output.Printer) error {
	printer.Debugf("installing %s", networkOperatorRelease)

	if err := hc.AddRepo(helm.NVIDIARepoName, helm.NVIDIARepoURL); err != nil {
		return fmt.Errorf("adding network-operator repo: %w", err)
	}

	// Determine fabric type from site config first, then profile.
	fabric := fabricType(site, profile)

	// Load values: common + fabric overlay (infiniband or roce) + site overrides.
	var overrides map[string]interface{}
	if site != nil && site.Overrides != nil {
		overrides = site.Overrides["network-operator"]
	}

	mergedValues, err := helm.LoadChartValues("network-operator", fabric, overrides)
	if err != nil {
		return fmt.Errorf("loading network-operator values: %w", err)
	}

	if err := hc.UpgradeOrInstall(
		ctx,
		networkOperatorRelease,
		networkOperatorChart,
		networkOperatorNamespace,
		mergedValues,
		helm.WithVersion(config.ResolveVersion(site, "network-operator", networkOperatorVersion)),
		helm.WithCreateNamespace(),
		helm.WithWait(),
		helm.WithTimeout(10*time.Minute),
	); err != nil {
		return fmt.Errorf("installing network-operator: %w", err)
	}

	return nil
}

// fabricType returns the RDMA fabric type from site or profile configuration.
// Site-level Fabric.Type takes precedence over profile-level Networking.Fabric.
func fabricType(site *config.Site, profile *config.Profile) string {
	if site != nil && site.Fabric != nil && site.Fabric.Type != "" {
		return site.Fabric.Type
	}
	if profile != nil {
		return profile.Networking.Fabric
	}
	return ""
}

// hasFabric returns true if a high-performance network fabric is configured.
func hasFabric(site *config.Site, profile *config.Profile) bool {
	ft := fabricType(site, profile)
	return ft != "" && ft != "none" && ft != "ethernet"
}
