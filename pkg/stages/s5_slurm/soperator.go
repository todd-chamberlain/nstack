package s5_slurm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/engine"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

const (
	soperatorVersion   = "3.0.2"
	soperatorRelease   = "soperator"
	soperatorNamespace = engine.SoperatorNamespace
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

	cloneCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(cloneCtx, "git", "clone",
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
	if err := helmDepUpdate(chartDir); err != nil {
		return fmt.Errorf("helm dep update for soperator: %w", err)
	}

	// Load and merge values: common -> distribution overlay -> site overrides.
	var distribution string
	if profile != nil {
		distribution = profile.Kubernetes.Distribution
	}
	mergedValues, err := helm.LoadChartValues("soperator", distribution, overrides)
	if err != nil {
		return fmt.Errorf("loading soperator values: %w", err)
	}

	// Override image registry if the profile specifies a custom one.
	applyRegistryOverride(mergedValues, profile)

	if err := hc.UpgradeOrInstall(
		ctx,
		soperatorRelease,
		chartDir, // local chart path
		soperatorNamespace,
		mergedValues,
		helm.WithCreateNamespace(),
		helm.WithTimeout(10*time.Minute),
	); err != nil {
		return fmt.Errorf("installing soperator: %w", err)
	}

	return nil
}

// helmDepUpdate runs a Helm dependency update on a local chart directory.
// Uses exec to invoke the helm binary, which handles repository config
// and getter initialization correctly across all environments.
func helmDepUpdate(chartDir string) error {
	cmd := exec.Command("helm", "dependency", "update", chartDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm dependency update %s: %s: %w", chartDir, string(out), err)
	}
	return nil
}
