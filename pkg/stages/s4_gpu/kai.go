package s4_gpu

import (
	"context"
	"time"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

const (
	kaiSchedulerChart     = "oci://ghcr.io/kai-scheduler/kai-scheduler/kai-scheduler"
	kaiSchedulerNamespace = "kai-scheduler"
	kaiSchedulerRelease   = "kai-scheduler"
	kaiSchedulerVersion   = "v0.14.0"
)

// installKAIScheduler deploys the NVIDIA KAI Scheduler for GPU-aware
// multi-tenant workload scheduling.
func installKAIScheduler(ctx context.Context, hc *helm.Client, site *config.Site, overrides map[string]interface{}, printer *output.Printer) error {
	printer.Debugf("installing %s", kaiSchedulerRelease)

	// KAI Scheduler is an OCI chart from GHCR — no repo add needed.

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
