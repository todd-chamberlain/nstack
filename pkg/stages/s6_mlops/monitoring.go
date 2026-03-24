package s6_mlops

import (
	"context"
	"fmt"
	"time"

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

	// 2. Load and merge values: common -> distribution overlay -> site overrides.
	var distribution string
	if profile != nil {
		distribution = profile.Kubernetes.Distribution
	}
	var overrides map[string]interface{}
	if site != nil && site.Overrides != nil {
		overrides = site.Overrides["monitoring"]
	}
	mergedValues, err := helm.LoadChartValues("monitoring", distribution, overrides)
	if err != nil {
		return fmt.Errorf("loading monitoring values: %w", err)
	}

	// 3. Install or upgrade the chart.
	if err := hc.UpgradeOrInstall(
		ctx,
		monitoringRelease,
		prometheusChart,
		monitoringNS,
		mergedValues,
		helm.WithVersion(monitoringVersion),
		helm.WithCreateNamespace(),
		helm.WithTimeout(10*time.Minute),
	); err != nil {
		return fmt.Errorf("installing kube-prometheus-stack: %w", err)
	}

	return nil
}