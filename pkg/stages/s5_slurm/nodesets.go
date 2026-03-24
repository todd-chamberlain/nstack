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
	nodesetsRelease = "slurm-nodesets"
)

// installNodeSets deploys the nodesets Helm chart from the cloned soperator
// repository. Values are loaded from embedded common.yaml and merged with
// any site overrides.
func installNodeSets(ctx context.Context, hc *helm.Client, site *config.Site, profile *config.Profile, repoDir string, printer *output.Printer) error {
	// Run helm dependency update on the nodesets chart.
	chartDir := filepath.Join(repoDir, "helm", "nodesets")
	if err := helmDepUpdate(ctx, chartDir, printer); err != nil {
		// nodesets may not have dependencies; log but don't fail.
		printer.Debugf("helm dep update for nodesets (non-fatal): %v", err)
	}

	// Load the common base values from embedded assets.
	commonData, err := assets.FS.ReadFile("charts/nodesets/common.yaml")
	if err != nil {
		return fmt.Errorf("reading nodesets common values: %w", err)
	}
	commonVals, err := helm.LoadValuesFile(commonData)
	if err != nil {
		return fmt.Errorf("parsing nodesets common values: %w", err)
	}

	// Try to load the distribution-specific overlay.
	var profileVals map[string]interface{}
	if profile != nil && profile.Kubernetes.Distribution != "" {
		overlayPath := fmt.Sprintf("charts/nodesets/%s.yaml", profile.Kubernetes.Distribution)
		overlayData, readErr := assets.FS.ReadFile(overlayPath)
		if readErr == nil {
			profileVals, err = helm.LoadValuesFile(overlayData)
			if err != nil {
				return fmt.Errorf("parsing nodesets %s overlay: %w", profile.Kubernetes.Distribution, err)
			}
			printer.Debugf("loaded nodesets overlay: %s", overlayPath)
		}
	}

	// Merge site-specific overrides.
	var siteOverrides map[string]interface{}
	if site != nil && site.Overrides != nil {
		siteOverrides = site.Overrides["nodesets"]
	}

	// Merge: common -> profile-specific -> site overrides.
	mergedValues := helm.MergeValues(commonVals, profileVals, siteOverrides)

	hc.SetNamespace(slurmNamespace)

	if err := hc.UpgradeOrInstall(
		nodesetsRelease,
		chartDir, // local chart path
		mergedValues,
		helm.WithTimeout(10*time.Minute),
	); err != nil {
		return fmt.Errorf("installing nodesets: %w", err)
	}

	return nil
}
