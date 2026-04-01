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
	docaRepoName  = "dpf-repository"
	docaRepoURL   = "https://helm.ngc.nvidia.com/nvidia/doca"
	docaChart     = "dpf-repository/dpf-operator"
	docaNamespace = "dpf-operator-system"
	docaRelease   = "dpf-operator"
	docaVersion   = "v25.10.1"
)

// installDOCA deploys the NVIDIA DOCA Platform Framework via its Helm chart.
// DOCA is only installed when DPU hardware is detected in the site config.
func installDOCA(ctx context.Context, hc *helm.Client, site *config.Site, _ *config.Profile, printer *output.Printer) error {
	printer.Debugf("installing %s", docaRelease)

	if err := hc.AddRepo(docaRepoName, docaRepoURL); err != nil {
		return fmt.Errorf("adding doca repo: %w", err)
	}

	var overrides map[string]interface{}
	if site != nil && site.Overrides != nil {
		overrides = site.Overrides["doca"]
	}

	mergedValues, err := helm.LoadChartValues("doca", "", overrides)
	if err != nil {
		return fmt.Errorf("loading doca values: %w", err)
	}

	return hc.UpgradeOrInstall(
		ctx,
		docaRelease,
		docaChart,
		docaNamespace,
		mergedValues,
		helm.WithVersion(config.ResolveVersion(site, "doca", docaVersion)),
		helm.WithCreateNamespace(),
		helm.WithWait(),
		helm.WithTimeout(10*time.Minute),
	)
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
