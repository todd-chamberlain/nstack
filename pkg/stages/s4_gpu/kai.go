package s4_gpu

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

const (
	kaiSchedulerChart     = "nvidia/kai-scheduler"
	kaiSchedulerNamespace = "kai-scheduler"
	kaiSchedulerRelease   = "kai-scheduler"
	kaiSchedulerVersion   = "0.3.2"
)

// isKAISchedulerInstalled checks whether the KAI Scheduler is deployed.
func isKAISchedulerInstalled(ctx context.Context, kc *kube.Client) (bool, string) {
	cs := kc.Clientset()
	dep, err := cs.AppsV1().Deployments(kaiSchedulerNamespace).Get(ctx, "kai-scheduler", metav1.GetOptions{})
	if err != nil {
		return false, ""
	}
	return true, kube.ExtractImageVersion(dep.Spec.Template.Spec.Containers)
}

// installKAIScheduler deploys the NVIDIA KAI Scheduler for GPU-aware
// multi-tenant workload scheduling.
func installKAIScheduler(ctx context.Context, hc *helm.Client, overrides map[string]interface{}, printer *output.Printer) error {
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

	err := hc.UpgradeOrInstall(
		ctx,
		kaiSchedulerRelease,
		kaiSchedulerChart,
		kaiSchedulerNamespace,
		values,
		helm.WithVersion(kaiSchedulerVersion),
		helm.WithCreateNamespace(),
		helm.WithWait(),
		helm.WithTimeout(5*time.Minute),
	)
	if err != nil {
		return fmt.Errorf("installing KAI Scheduler: %w", err)
	}

	return nil
}
