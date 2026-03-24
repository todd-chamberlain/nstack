package s5_slurm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/todd-chamberlain/nstack/internal/assets"
	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

const (
	soperatorVersion   = "3.0.2"
	soperatorRelease   = "soperator"
	soperatorNamespace = "soperator-system"
	soperatorGitRepo   = "https://github.com/nebius/soperator.git"
	soperatorGitTag    = "3.0.2"
)

// cloneSoperatorRepo clones the soperator repository to a temporary directory at
// the specified tag. Returns the path to the cloned repository directory.
// The caller is responsible for cleaning up the returned directory with os.RemoveAll.
func cloneSoperatorRepo(ctx context.Context, printer *output.Printer) (string, error) {
	tmpDir, err := os.MkdirTemp("", "nstack-soperator-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir for soperator clone: %w", err)
	}

	printer.Debugf("cloning soperator %s to %s", soperatorGitTag, tmpDir)

	cmd := exec.CommandContext(ctx, "git", "clone",
		"--depth", "1",
		"--branch", soperatorGitTag,
		soperatorGitRepo,
		tmpDir,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("cloning soperator repo: %w", err)
	}

	return tmpDir, nil
}

// installSoperatorCRDs applies CRD definitions from the cloned soperator repo.
// It reads all YAML files from helm/soperator-crds/templates/ and applies them.
func installSoperatorCRDs(ctx context.Context, kc *kube.Client, repoDir string, printer *output.Printer) error {
	crdsDir := filepath.Join(repoDir, "helm", "soperator-crds", "templates")

	entries, err := os.ReadDir(crdsDir)
	if err != nil {
		return fmt.Errorf("reading CRD templates dir %s: %w", crdsDir, err)
	}

	totalApplied := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".yaml" && filepath.Ext(name) != ".yml" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(crdsDir, name))
		if err != nil {
			return fmt.Errorf("reading CRD file %s: %w", name, err)
		}

		applied, err := kc.ApplyCRDs(ctx, data)
		if err != nil {
			return fmt.Errorf("applying CRDs from %s: %w", name, err)
		}
		totalApplied += applied
		printer.Debugf("applied %d CRDs from %s", applied, name)
	}

	printer.Debugf("total CRDs applied: %d", totalApplied)
	return nil
}

// installSoperator deploys the soperator controller from the local chart in the
// cloned repository. Values are loaded from embedded assets (common + distribution
// overlay) and merged with any site overrides.
func installSoperator(ctx context.Context, hc *helm.Client, kc *kube.Client, profile *config.Profile, repoDir string, overrides map[string]interface{}, printer *output.Printer) error {
	// Run helm dependency update on the soperator chart.
	chartDir := filepath.Join(repoDir, "helm", "soperator")
	if err := helmDepUpdate(ctx, chartDir, printer); err != nil {
		return fmt.Errorf("helm dep update for soperator: %w", err)
	}

	// Load the common base values from embedded assets.
	commonData, err := assets.FS.ReadFile("charts/soperator/common.yaml")
	if err != nil {
		return fmt.Errorf("reading soperator common values: %w", err)
	}
	commonVals, err := helm.LoadValuesFile(commonData)
	if err != nil {
		return fmt.Errorf("parsing soperator common values: %w", err)
	}

	// Try to load the distribution-specific overlay (e.g., k3s.yaml).
	var profileVals map[string]interface{}
	if profile != nil && profile.Kubernetes.Distribution != "" {
		overlayPath := fmt.Sprintf("charts/soperator/%s.yaml", profile.Kubernetes.Distribution)
		overlayData, readErr := assets.FS.ReadFile(overlayPath)
		if readErr == nil {
			profileVals, err = helm.LoadValuesFile(overlayData)
			if err != nil {
				return fmt.Errorf("parsing soperator %s overlay: %w", profile.Kubernetes.Distribution, err)
			}
			printer.Debugf("loaded soperator overlay: %s", overlayPath)
		}
	}

	// Merge: common -> profile-specific -> site overrides.
	mergedValues := helm.MergeValues(commonVals, profileVals, overrides)

	hc.SetNamespace(soperatorNamespace)

	if err := hc.UpgradeOrInstall(
		soperatorRelease,
		chartDir, // local chart path
		mergedValues,
		helm.WithCreateNamespace(),
		helm.WithTimeout(10*time.Minute),
	); err != nil {
		return fmt.Errorf("installing soperator: %w", err)
	}

	return nil
}

// helmDepUpdate runs `helm dependency update` on a local chart directory.
func helmDepUpdate(ctx context.Context, chartDir string, printer *output.Printer) error {
	printer.Debugf("running helm dependency update in %s", chartDir)

	cmd := exec.CommandContext(ctx, "helm", "dependency", "update", chartDir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helm dependency update in %s: %w", chartDir, err)
	}

	return nil
}
