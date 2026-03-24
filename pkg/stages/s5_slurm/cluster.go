package s5_slurm

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/todd-chamberlain/nstack/internal/assets"
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
func installSlurmCluster(ctx context.Context, hc *helm.Client, site *config.Site, profile *config.Profile, repoDir string, printer *output.Printer) error {
	// Run helm dependency update on the slurm-cluster chart.
	chartDir := filepath.Join(repoDir, "helm", "slurm-cluster")
	if err := helmDepUpdate(ctx, chartDir, printer); err != nil {
		return fmt.Errorf("helm dep update for slurm-cluster: %w", err)
	}

	// Load the common base values from embedded assets.
	commonData, err := assets.FS.ReadFile("charts/slurm-cluster/common.yaml")
	if err != nil {
		return fmt.Errorf("reading slurm-cluster common values: %w", err)
	}
	commonVals, err := helm.LoadValuesFile(commonData)
	if err != nil {
		return fmt.Errorf("parsing slurm-cluster common values: %w", err)
	}

	// Try to load the distribution-specific overlay (e.g., k3s.yaml).
	var profileVals map[string]interface{}
	if profile != nil && profile.Kubernetes.Distribution != "" {
		overlayPath := fmt.Sprintf("charts/slurm-cluster/%s.yaml", profile.Kubernetes.Distribution)
		overlayData, readErr := assets.FS.ReadFile(overlayPath)
		if readErr == nil {
			profileVals, err = helm.LoadValuesFile(overlayData)
			if err != nil {
				return fmt.Errorf("parsing slurm-cluster %s overlay: %w", profile.Kubernetes.Distribution, err)
			}
			printer.Debugf("loaded slurm-cluster overlay: %s", overlayPath)
		}
	}

	// Merge site-specific overrides.
	var siteOverrides map[string]interface{}
	if site != nil && site.Overrides != nil {
		siteOverrides = site.Overrides["slurm-cluster"]
	}

	// Merge: common -> profile-specific -> site overrides.
	mergedValues := helm.MergeValues(commonVals, profileVals, siteOverrides)

	hc.SetNamespace(slurmNamespace)

	if err := hc.UpgradeOrInstall(
		slurmClusterRelease,
		chartDir, // local chart path
		mergedValues,
		helm.WithCreateNamespace(),
		helm.WithTimeout(15*time.Minute),
	); err != nil {
		return fmt.Errorf("installing slurm-cluster: %w", err)
	}

	return nil
}
