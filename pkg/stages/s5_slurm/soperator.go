package s5_slurm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
)

// cloneSoperatorRepo clones the soperator repository at the specified tag and
// returns the path to the cloned directory along with a cleanup function.
//
// Clones are cached under ~/.nstack/cache/soperator/<tag>/ so that subsequent
// deploys of the same version skip the git clone and helm dep update steps.
// The cleanup function is a no-op for cached directories and removes the
// directory only when using a temp-dir fallback.
func cloneSoperatorRepo(ctx context.Context, tag string, printer *output.Printer) (string, func(), error) {
	noop := func() {}

	// Try cache path first.
	cacheDir, cacheErr := soperatorCacheDir(tag)
	if cacheErr == nil {
		if isCacheValid(cacheDir, tag) {
			printer.Debugf("using cached soperator %s", tag)
			return cacheDir, noop, nil
		}

		// Cache miss — clone into cache dir.
		os.RemoveAll(cacheDir)
		if err := os.MkdirAll(filepath.Dir(cacheDir), 0755); err == nil {
			if err := gitClone(ctx, tag, cacheDir, printer); err != nil {
				os.RemoveAll(cacheDir)
				// Fall through to temp-dir fallback.
			} else {
				if err := helmDepUpdateAll(cacheDir, printer); err != nil {
					os.RemoveAll(cacheDir)
					return "", noop, fmt.Errorf("helm dep update in cached clone: %w", err)
				}
				return cacheDir, noop, nil
			}
		}
	}

	// Fallback: clone to a temp directory (cleaned up by caller).
	tmpDir, err := os.MkdirTemp("", "nstack-soperator-*")
	if err != nil {
		return "", noop, fmt.Errorf("creating temp dir for soperator clone: %w", err)
	}

	if err := gitClone(ctx, tag, tmpDir, printer); err != nil {
		os.RemoveAll(tmpDir)
		return "", noop, fmt.Errorf("cloning soperator repo: %w", err)
	}
	if err := helmDepUpdateAll(tmpDir, printer); err != nil {
		os.RemoveAll(tmpDir)
		return "", noop, fmt.Errorf("helm dep update in temp clone: %w", err)
	}

	return tmpDir, func() { os.RemoveAll(tmpDir) }, nil
}

// gitClone performs a shallow git clone of the soperator repo at the given tag
// into destDir.
func gitClone(ctx context.Context, tag, destDir string, printer *output.Printer) error {
	printer.Debugf("cloning soperator %s to %s", tag, destDir)

	cloneCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(cloneCtx, "git", "clone",
		"--depth", "1",
		"--branch", tag,
		soperatorGitRepo,
		destDir,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cloning soperator repo: %w", err)
	}
	return nil
}

// soperatorCacheDir returns the cache directory path for a given soperator tag.
func soperatorCacheDir(tag string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".nstack", "cache", "soperator", tag), nil
}

// isCacheValid checks that a cached clone directory looks complete: the git
// repo exists, HEAD is not corrupt, and helm dependencies have been built
// for the soperator chart.
func isCacheValid(dir string, expectedTag string) bool {
	// Verify the cache directory was cloned at the expected tag by checking
	// that a tag ref file exists for it.
	tagRefPath := filepath.Join(dir, ".git", "refs", "tags", expectedTag)
	if _, err := os.Stat(tagRefPath); err != nil {
		// Detached HEAD clones may not have refs/tags — fall back to checking HEAD exists
		headPath := filepath.Join(dir, ".git", "HEAD")
		data, err := os.ReadFile(headPath)
		if err != nil || len(strings.TrimSpace(string(data))) < 10 {
			return false
		}
	}
	// Soperator chart must have its dependencies (kruise) already built.
	chartsDir := filepath.Join(dir, "helm", "soperator", "charts")
	if _, err := os.Stat(chartsDir); err != nil {
		return false
	}
	return true
}

// helmDepUpdateAll runs helm dep update on every chart sub-directory in the
// cloned soperator repo that has a Chart.yaml. This is called once per fresh
// clone so that cached repos are fully ready to use.
func helmDepUpdateAll(repoDir string, printer *output.Printer) error {
	chartDirs := []string{
		filepath.Join(repoDir, "helm", "soperator"),
		filepath.Join(repoDir, "helm", "slurm-cluster"),
		filepath.Join(repoDir, "helm", "nodesets"),
	}
	for _, dir := range chartDirs {
		// Only run if the chart directory exists and has a Chart.yaml.
		if _, err := os.Stat(filepath.Join(dir, "Chart.yaml")); err != nil {
			continue
		}
		if err := helmDepUpdate(dir); err != nil {
			// nodesets may not have dependencies; log but don't fail for it.
			if filepath.Base(dir) == "nodesets" {
				printer.Debugf("helm dep update for nodesets (non-fatal): %v", err)
				continue
			}
			return fmt.Errorf("helm dep update for %s: %w", filepath.Base(dir), err)
		}
	}
	return nil
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
func installSoperator(ctx context.Context, hc *helm.Client, profile *config.Profile, repoDir string, overrides map[string]interface{}, printer *output.Printer) error {
	chartDir := filepath.Join(repoDir, "helm", "soperator")

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

	return hc.UpgradeOrInstall(
		ctx,
		soperatorRelease,
		chartDir, // local chart path
		soperatorNamespace,
		mergedValues,
		helm.WithCreateNamespace(),
		helm.WithTimeout(10*time.Minute),
	)
}

// helmDepUpdate runs a Helm dependency update on a local chart directory.
// Uses exec to invoke the helm binary, which handles repository config
// and getter initialization correctly across all environments.
func helmDepUpdate(chartDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "helm", "dependency", "update", chartDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm dependency update %s: %s: %w", chartDir, string(out), err)
	}
	return nil
}
