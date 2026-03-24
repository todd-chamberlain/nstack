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
	docaChart     = "nvidia/doca-platform"
	docaNamespace = "doca-platform"
	docaRelease   = "doca-platform"
	docaVersion   = "2.9.1"
)

// installDOCA deploys the NVIDIA DOCA Platform Framework via its Helm chart.
// DOCA is only installed when DPU hardware is detected in the site config.
func installDOCA(ctx context.Context, hc *helm.Client, site *config.Site, profile *config.Profile, printer *output.Printer) error {
	if err := hc.AddRepo(networkOperatorRepoName, networkOperatorRepo); err != nil {
		return fmt.Errorf("adding nvidia repo for doca: %w", err)
	}

	var overrides map[string]interface{}
	if site != nil && site.Overrides != nil {
		overrides = site.Overrides["doca"]
	}

	mergedValues, err := helm.LoadChartValues("doca", "", overrides)
	if err != nil {
		return fmt.Errorf("loading doca values: %w", err)
	}

	if err := hc.UpgradeOrInstall(
		docaRelease,
		docaChart,
		docaNamespace,
		mergedValues,
		helm.WithVersion(docaVersion),
		helm.WithCreateNamespace(),
		helm.WithWait(),
		helm.WithTimeout(10*time.Minute),
	); err != nil {
		return fmt.Errorf("installing doca: %w", err)
	}

	return nil
}

// hasDPUs returns true if any node in the site config has DPU hardware.
func hasDPUs(site *config.Site) bool {
	if site == nil {
		return false
	}
	for _, node := range site.Nodes {
		if len(node.DPUs) > 0 {
			return true
		}
	}
	return false
}
