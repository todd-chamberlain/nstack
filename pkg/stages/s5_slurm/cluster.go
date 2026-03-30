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
	slurmClusterRelease = "slurm-cluster"
)

// installSlurmCluster deploys the slurm-cluster Helm chart from the cloned soperator
// repository. Values are loaded from embedded common.yaml and distribution-specific
// overlay, then merged with any site overrides.
func installSlurmCluster(ctx context.Context, hc *helm.Client, site *config.Site, profile *config.Profile, repoDir string, cluster config.ClusterConfig, printer *output.Printer) error {
	chartDir := filepath.Join(repoDir, "helm", "slurm-cluster")

	// Load and merge values: common -> distribution overlay -> site overrides.
	var distribution string
	if profile != nil {
		distribution = profile.Kubernetes.Distribution
	}
	var siteOverrides map[string]interface{}
	if site != nil && site.Overrides != nil {
		siteOverrides = site.Overrides["slurm-cluster"]
	}
	mergedValues, err := helm.LoadChartValues("slurm-cluster", distribution, siteOverrides)
	if err != nil {
		return fmt.Errorf("loading slurm-cluster values: %w", err)
	}

	// Override image registry if the profile specifies a custom one.
	applyRegistryOverride(mergedValues, profile)

	if err := hc.UpgradeOrInstall(
		ctx,
		slurmClusterRelease,
		chartDir, // local chart path
		cluster.Namespace,
		mergedValues,
		helm.WithCreateNamespace(),
		helm.WithTimeout(15*time.Minute),
	); err != nil {
		return fmt.Errorf("installing slurm-cluster: %w", err)
	}

	return nil
}
