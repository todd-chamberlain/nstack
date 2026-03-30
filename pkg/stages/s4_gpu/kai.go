package s4_gpu

import (
	"context"
	"fmt"
	"time"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

const (
	kaiSchedulerChart     = "nvidia/kai-scheduler"
	kaiSchedulerNamespace = "kai-scheduler"
	kaiSchedulerRelease   = "kai-scheduler"
	kaiSchedulerVersion   = "0.3.2"
)

// installKAIScheduler deploys the NVIDIA KAI Scheduler for GPU-aware
// multi-tenant workload scheduling.
func installKAIScheduler(ctx context.Context, hc *helm.Client, site *config.Site, overrides map[string]interface{}, printer *output.Printer) error {
	printer.Debugf("installing %s", kaiSchedulerRelease)

	if err := hc.AddRepo(helm.NVIDIARepoName, helm.NVIDIARepoURL); err != nil {
		return fmt.Errorf("adding nvidia repo: %w", err)
	}

	values := map[string]interface{}{
		"scheduler": map[string]interface{}{
			"enabled": true,
		},
	}

	if overrides != nil {
		values = helm.MergeValues(values, overrides)
	}

	return hc.UpgradeOrInstall(
		ctx,
		kaiSchedulerRelease,
		kaiSchedulerChart,
		kaiSchedulerNamespace,
		values,
		helm.WithVersion(config.ResolveVersion(site, "kai-scheduler", kaiSchedulerVersion)),
		helm.WithCreateNamespace(),
		helm.WithWait(),
		helm.WithTimeout(5*time.Minute),
	)
}
