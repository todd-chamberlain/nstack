package s6_mlops

import (
	"context"
	"fmt"
	"time"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

// applyFederationTelemetry injects Thanos/Prometheus remote_write configuration
// and external labels into the monitoring values when the site has federation
// telemetry configured.
func applyFederationTelemetry(mergedValues map[string]interface{}, site *config.Site) {
	if site == nil || site.Federation == nil || site.Federation.Telemetry == nil {
		return
	}
	tel := site.Federation.Telemetry
	if tel.RemoteWriteURL == "" {
		return
	}

	cluster := config.ResolveCluster(site)

	telemetryValues := map[string]interface{}{
		"prometheus": map[string]interface{}{
			"prometheusSpec": map[string]interface{}{
				"remoteWrite": []interface{}{
					map[string]interface{}{
						"url": tel.RemoteWriteURL,
					},
				},
				"externalLabels": map[string]interface{}{
					"cluster": cluster.Name,
					"site":    site.Name,
				},
			},
		},
	}

	helm.MergeValuesInto(mergedValues, telemetryValues)
}

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

	// 3. Inject federation telemetry (remote_write) if configured.
	applyFederationTelemetry(mergedValues, site)

	// 4. Install or upgrade the chart.
	return hc.UpgradeOrInstall(
		ctx,
		monitoringRelease,
		prometheusChart,
		monitoringNS,
		mergedValues,
		helm.WithVersion(config.ResolveVersion(site, "monitoring", monitoringVersion)),
		helm.WithCreateNamespace(),
		helm.WithTimeout(10*time.Minute),
	)
}