package s4_gpu

import (
	"context"
	"fmt"
	"time"

	"github.com/todd-chamberlain/nstack/internal/assets"
	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

const (
	gpuOperatorRepo      = "https://helm.ngc.nvidia.com/nvidia"
	gpuOperatorRepoName  = "nvidia"
	gpuOperatorChart     = "nvidia/gpu-operator"
	gpuOperatorNamespace = "gpu-operator"
	gpuOperatorRelease   = "gpu-operator"
	gpuOperatorVersion   = "v25.10.1"
)

// isGPUOperatorInstalled checks whether the GPU Operator is already deployed.
func isGPUOperatorInstalled(ctx context.Context, hc *helm.Client) (bool, string, error) {
	hc.SetNamespace(gpuOperatorNamespace)
	return hc.IsInstalled(gpuOperatorRelease)
}

// installGPUOperator deploys the NVIDIA GPU Operator via its Helm chart.
// Values are loaded from embedded assets (common + distribution overlay)
// and merged with any user-provided site overrides.
func installGPUOperator(ctx context.Context, hc *helm.Client, profile *config.Profile, overrides map[string]interface{}, printer *output.Printer) error {
	if err := hc.AddRepo(gpuOperatorRepoName, gpuOperatorRepo); err != nil {
		return fmt.Errorf("adding gpu-operator repo: %w", err)
	}

	// Load the common base values.
	commonData, err := assets.FS.ReadFile("charts/gpu-operator/common.yaml")
	if err != nil {
		return fmt.Errorf("reading gpu-operator common values: %w", err)
	}
	commonVals, err := helm.LoadValuesFile(commonData)
	if err != nil {
		return fmt.Errorf("parsing gpu-operator common values: %w", err)
	}

	// Try to load the distribution-specific overlay (e.g., k3s.yaml).
	var profileVals map[string]interface{}
	if profile != nil && profile.Kubernetes.Distribution != "" {
		overlayPath := fmt.Sprintf("charts/gpu-operator/%s.yaml", profile.Kubernetes.Distribution)
		overlayData, readErr := assets.FS.ReadFile(overlayPath)
		if readErr == nil {
			profileVals, err = helm.LoadValuesFile(overlayData)
			if err != nil {
				return fmt.Errorf("parsing gpu-operator %s overlay: %w", profile.Kubernetes.Distribution, err)
			}
			printer.Debugf("loaded gpu-operator overlay: %s", overlayPath)
		}
	}

	// Merge: common -> profile-specific -> site overrides.
	mergedValues := helm.MergeValues(commonVals, profileVals, overrides)

	hc.SetNamespace(gpuOperatorNamespace)

	if err := hc.UpgradeOrInstall(
		gpuOperatorRelease,
		gpuOperatorChart,
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
