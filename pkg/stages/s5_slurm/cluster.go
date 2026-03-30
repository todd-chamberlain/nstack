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

	// Inject federation accounting config when configured.
	applyFederationValues(mergedValues, site, cluster, printer)

	return hc.UpgradeOrInstall(
		ctx,
		slurmClusterRelease,
		chartDir, // local chart path
		cluster.Namespace,
		mergedValues,
		helm.WithCreateNamespace(),
		helm.WithTimeout(15*time.Minute),
	)
}

// applyFederationValues injects Slurm accounting and federation parameters into
// the merged Helm values when site.Federation is configured. This enables
// slurmdbd-backed accounting and federation job routing.
func applyFederationValues(mergedValues map[string]interface{}, site *config.Site, cluster config.ClusterConfig, printer *output.Printer) {
	if site == nil || site.Federation == nil || site.Federation.Accounting == nil {
		return
	}

	acct := site.Federation.Accounting

	// Enable accounting in chart values.
	accountingValues := map[string]interface{}{
		"slurmNodes": map[string]interface{}{
			"accounting": map[string]interface{}{
				"enabled": true,
			},
		},
	}
	helm.MergeValuesInto(mergedValues, accountingValues)

	// Build customSlurmConfig additions for accounting and federation.
	host := acct.Host
	port := 6819
	if acct.Port > 0 {
		port = acct.Port
	}

	federationConfig := fmt.Sprintf("\nAccountingStorageType=accounting_storage/slurmdbd\n"+
		"AccountingStorageHost=%s\n"+
		"AccountingStoragePort=%d\n"+
		"AccountingStorageTRES=gres/gpu\n"+
		"FederationParameters=fed_display\n", host, port)

	// Append to existing customSlurmConfig if present.
	existing := ""
	if cs, ok := mergedValues["customSlurmConfig"].(string); ok {
		existing = cs
	}
	mergedValues["customSlurmConfig"] = existing + federationConfig

	printer.Debugf("injected federation accounting config (host=%s, port=%d)", host, port)
}
