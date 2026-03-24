package s6_mlops

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

const soperatorNamespace = "soperator-system"

// deploySoperatorDashboards conditionally installs Soperator Grafana dashboards.
// For v0.1, this is a stub that checks whether the Soperator operator is deployed
// and prints an informational message. The kube-prometheus-stack already includes
// base GPU/node dashboards; specialized Soperator dashboards (cluster health,
// jobs overview, workers) will be added in a future version.
func deploySoperatorDashboards(ctx context.Context, kc *kube.Client, printer *output.Printer) error {
	// Check if soperator is deployed by looking for deployments in soperator-system.
	cs := kc.Clientset()
	deps, err := cs.AppsV1().Deployments(soperatorNamespace).List(ctx, metav1.ListOptions{
		Limit: 1,
	})
	if err != nil || len(deps.Items) == 0 {
		printer.Infof("        → Soperator dashboards: skipped (soperator not installed)")
		return nil
	}

	printer.Infof("        → Soperator dashboards: install from soperator repo if needed")
	return nil
}
