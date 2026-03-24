package s5_slurm

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

const (
	nodesetsRelease = "slurm-nodesets"
)

// installNodeSets deploys the nodesets Helm chart from the cloned soperator
// repository. Values are loaded from embedded common.yaml and merged with
// any site overrides.
func installNodeSets(ctx context.Context, hc *helm.Client, site *config.Site, profile *config.Profile, repoDir string, printer *output.Printer) error {
	// Run helm dependency update on the nodesets chart.
	chartDir := filepath.Join(repoDir, "helm", "nodesets")
	if err := helmDepUpdate(chartDir); err != nil {
		// nodesets may not have dependencies; log but don't fail.
		printer.Debugf("helm dep update for nodesets (non-fatal): %v", err)
	}

	// Load and merge values: common -> distribution overlay -> site overrides.
	var distribution string
	if profile != nil {
		distribution = profile.Kubernetes.Distribution
	}
	var siteOverrides map[string]interface{}
	if site != nil && site.Overrides != nil {
		siteOverrides = site.Overrides["nodesets"]
	}
	mergedValues, err := helm.LoadChartValues("nodesets", distribution, siteOverrides)
	if err != nil {
		return fmt.Errorf("loading nodesets values: %w", err)
	}

	if err := hc.UpgradeOrInstall(
		nodesetsRelease,
		chartDir, // local chart path
		slurmNamespace,
		mergedValues,
		helm.WithTimeout(10*time.Minute),
	); err != nil {
		return fmt.Errorf("installing nodesets: %w", err)
	}

	return nil
}
