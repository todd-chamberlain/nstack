package s6_mlops

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
	prometheusRepo     = "https://prometheus-community.github.io/helm-charts"
	prometheusRepoName = "prometheus-community"
	prometheusChart    = "prometheus-community/kube-prometheus-stack"
	monitoringNS       = "monitoring"
	monitoringRelease  = "kube-prometheus-stack"
	monitoringVersion  = "82.4.0"
)

// deployMonitoring installs the kube-prometheus-stack Helm chart (Prometheus,
// Grafana, Alertmanager, node-exporter, kube-state-metrics) into the
// monitoring namespace.
func deployMonitoring(ctx context.Context, hc *helm.Client, site *config.Site, profile *config.Profile, printer *output.Printer) error {
	// 1. Add the prometheus-community Helm repository.
	if err := hc.AddRepo(prometheusRepoName, prometheusRepo); err != nil {
		return fmt.Errorf("adding prometheus-community repo: %w", err)
	}

	// 2. Load the common base values.
	commonData, err := assets.FS.ReadFile("charts/monitoring/common.yaml")
	if err != nil {
		return fmt.Errorf("reading monitoring common values: %w", err)
	}
	commonVals, err := helm.LoadValuesFile(commonData)
	if err != nil {
		return fmt.Errorf("parsing monitoring common values: %w", err)
	}

	// 3. Try to load the distribution-specific overlay (e.g., k3s.yaml).
	var profileVals map[string]interface{}
	if profile != nil && profile.Kubernetes.Distribution != "" {
		overlayPath := fmt.Sprintf("charts/monitoring/%s.yaml", profile.Kubernetes.Distribution)
		overlayData, readErr := assets.FS.ReadFile(overlayPath)
		if readErr == nil {
			profileVals, err = helm.LoadValuesFile(overlayData)
			if err != nil {
				return fmt.Errorf("parsing monitoring %s overlay: %w", profile.Kubernetes.Distribution, err)
			}
			printer.Debugf("loaded monitoring overlay: %s", overlayPath)
		}
	}

	// 4. Merge: common -> profile-specific -> site overrides.
	var overrides map[string]interface{}
	if site != nil && site.Overrides != nil {
		overrides = site.Overrides["monitoring"]
	}
	mergedValues := helm.MergeValues(commonVals, profileVals, overrides)

	// 5. Install or upgrade the chart.
	hc.SetNamespace(monitoringNS)

	if err := hc.UpgradeOrInstall(
		monitoringRelease,
		prometheusChart,
		mergedValues,
		helm.WithVersion(monitoringVersion),
		helm.WithCreateNamespace(),
		helm.WithTimeout(10*time.Minute),
	); err != nil {
		return fmt.Errorf("installing kube-prometheus-stack: %w", err)
	}

	return nil
}

// destroyMonitoring removes the kube-prometheus-stack Helm release.
func destroyMonitoring(ctx context.Context, hc *helm.Client, printer *output.Printer) error {
	hc.SetNamespace(monitoringNS)

	installed, _, err := hc.IsInstalled(monitoringRelease)
	if err != nil {
		return fmt.Errorf("checking monitoring release: %w", err)
	}
	if !installed {
		printer.Debugf("monitoring release not installed, skipping")
		return nil
	}

	if err := hc.Uninstall(monitoringRelease); err != nil {
		return fmt.Errorf("uninstalling kube-prometheus-stack: %w", err)
	}

	return nil
}
