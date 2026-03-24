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
	gpuOperatorChart     = "nvidia/gpu-operator"
	gpuOperatorNamespace = "gpu-operator"
	gpuOperatorRelease   = "gpu-operator"
	gpuOperatorVersion   = "v25.10.1"
)

// installGPUOperator deploys the NVIDIA GPU Operator via its Helm chart.
// Values are loaded from embedded assets (common + distribution overlay)
// and merged with any user-provided site overrides.
func installGPUOperator(ctx context.Context, hc *helm.Client, profile *config.Profile, overrides map[string]interface{}, printer *output.Printer) error {
	printer.Debugf("installing %s", gpuOperatorRelease)

	if err := hc.AddRepo(helm.NVIDIARepoName, helm.NVIDIARepoURL); err != nil {
		return fmt.Errorf("adding gpu-operator repo: %w", err)
	}

	// Load and merge values: common -> distribution overlay -> site overrides.
	var distribution string
	if profile != nil {
		distribution = profile.Kubernetes.Distribution
	}
	mergedValues, err := helm.LoadChartValues("gpu-operator", distribution, overrides)
	if err != nil {
		return fmt.Errorf("loading gpu-operator values: %w", err)
	}

	if err := hc.UpgradeOrInstall(
		ctx,
		gpuOperatorRelease,
		gpuOperatorChart,
		gpuOperatorNamespace,
		mergedValues,
		helm.WithVersion(gpuOperatorVersion),
		helm.WithCreateNamespace(),
		helm.WithWait(),
		helm.WithTimeout(10*time.Minute),
	); err != nil {
		return fmt.Errorf("installing gpu-operator: %w", err)
	}

	return nil
}
