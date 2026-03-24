package s6_mlops

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/engine"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
	"github.com/todd-chamberlain/nstack/pkg/state"
)

// MLOpsStage implements the Stage interface for deploying MLflow and
// the kube-prometheus-stack monitoring suite.
type MLOpsStage struct{}

// New returns a new MLOpsStage instance.
func New() *MLOpsStage { return &MLOpsStage{} }

func (s *MLOpsStage) Number() int         { return 6 }
func (s *MLOpsStage) Name() string        { return "MLOps & Monitoring" }
func (s *MLOpsStage) Dependencies() []int { return nil }

// Detect checks for existing MLflow and kube-prometheus-stack deployments.
func (s *MLOpsStage) Detect(ctx context.Context, kc *kube.Client) (*engine.DetectResult, error) {
	result := &engine.DetectResult{
		Operators: []engine.DetectedOperator{
			engine.DetectDeployment(ctx, kc.Clientset(), mlflowNamespace, mlflowName, "mlflow"),
		},
	}

	// Check kube-prometheus-stack via Helm release (not a Deployment).
	hc := helm.NewClient(kc.Kubeconfig())
	installed, version, _ := hc.IsInstalled(monitoringRelease, monitoringNS)
	if installed {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "kube-prometheus-stack",
			Version:   version,
			Namespace: monitoringNS,
			Status:    "running",
		})
	} else {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "kube-prometheus-stack",
			Namespace: monitoringNS,
			Status:    "not-installed",
		})
	}

	return result, nil
}

// Validate verifies the cluster is reachable.
func (s *MLOpsStage) Validate(ctx context.Context, kc *kube.Client, profile *config.Profile) error {
	return engine.ValidateClusterReachable(ctx, kc.Clientset())
}

// Plan builds a StagePlan describing what actions to take for MLflow
// and the monitoring stack.
func (s *MLOpsStage) Plan(ctx context.Context, kc *kube.Client, profile *config.Profile, current *state.StageState) (*engine.StagePlan, error) {
	plan := &engine.StagePlan{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	cs := kc.Clientset()

	// Plan MLflow: check if its deployment exists.
	mlDep, mlErr := cs.AppsV1().Deployments(mlflowNamespace).Get(ctx, mlflowName, metav1.GetOptions{})
	if mlErr == nil {
		mlVersion := kube.ExtractImageVersion(mlDep.Spec.Template.Spec.Containers)
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "mlflow",
			Action:    "skip",
			Version:   mlVersion,
			Current:   mlVersion,
			Namespace: mlflowNamespace,
		})
	} else {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "mlflow",
			Action:    "install",
			Namespace: mlflowNamespace,
		})
	}

	// Plan monitoring: check if the Helm release exists.
	hc := helm.NewClient(kc.Kubeconfig())
	monInstalled, monVersion, _ := hc.IsInstalled(monitoringRelease, monitoringNS)
	if monInstalled {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "monitoring",
			Action:    "skip",
			Chart:     prometheusChart,
			Version:   monitoringVersion,
			Current:   monVersion,
			Namespace: monitoringNS,
		})
	} else {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "monitoring",
			Action:    "install",
			Chart:     prometheusChart,
			Version:   monitoringVersion,
			Namespace: monitoringNS,
		})
	}

	// Plan soperator dashboards (always present as a component, conditionally applied).
	plan.Components = append(plan.Components, engine.ComponentPlan{
		Name:      "soperator-dashboards",
		Action:    "install",
		Namespace: monitoringNS,
	})

	// Determine overall stage action.
	plan.Action = engine.DeterminePlanAction(plan.Components, plan.Patches)

	return plan, nil
}

// Apply executes the stage plan, installing MLflow, the monitoring stack,
// and Soperator dashboards as needed.
func (s *MLOpsStage) Apply(ctx context.Context, kc *kube.Client, hc *helm.Client, site *config.Site, profile *config.Profile, plan *engine.StagePlan, printer *output.Printer) error {
	total := len(plan.Components)

	for i, comp := range plan.Components {
		idx := i + 1

		switch comp.Action {
		case "skip":
			printer.ComponentSkipped(idx, total, comp.Name, comp.Current, "already installed")
			continue

		case "install":
			printer.ComponentStart(idx, total, comp.Name, comp.Version, "installing")
			var err error

			switch comp.Name {
			case "mlflow":
				err = deployMLflow(ctx, kc, site, profile, printer)
			case "monitoring":
				err = deployMonitoring(ctx, hc, site, profile, printer)
			case "soperator-dashboards":
				err = deploySoperatorDashboards(ctx, kc, printer)
			default:
				err = fmt.Errorf("unknown component: %s", comp.Name)
			}

			printer.ComponentDone(comp.Name, err)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// Status reports the current runtime health of the MLOps & Monitoring stage.
func (s *MLOpsStage) Status(ctx context.Context, kc *kube.Client) (*engine.StageStatus, error) {
	cs := kc.Clientset()

	mlStatus := engine.CheckDeploymentStatus(ctx, cs, mlflowNamespace, mlflowName, "mlflow")

	status := &engine.StageStatus{
		Stage:   s.Number(),
		Name:    s.Name(),
		Version: mlStatus.Version,
		Components: []engine.ComponentStatus{
			mlStatus,
		},
	}

	// Check monitoring components: Prometheus, Grafana, Alertmanager.
	// Each is tried as a Deployment first, then as a StatefulSet (Prometheus
	// and Alertmanager use StatefulSets in kube-prometheus-stack).
	monComponents := []struct {
		name       string
		resource   string
		namespace  string
	}{
		{"prometheus", "prometheus-kube-prometheus-stack-prometheus", monitoringNS},
		{"grafana", "kube-prometheus-stack-grafana", monitoringNS},
		{"alertmanager", "alertmanager-kube-prometheus-stack-alertmanager", monitoringNS},
	}

	for _, mc := range monComponents {
		compStatus := engine.CheckDeploymentStatus(ctx, cs, mc.namespace, mc.resource, mc.name)
		if compStatus.Status == "not-installed" {
			// Fall back to StatefulSet (Prometheus and Alertmanager).
			compStatus = engine.CheckStatefulSetStatus(ctx, cs, mc.namespace, mc.resource, mc.name)
		}
		status.Components = append(status.Components, compStatus)
	}

	status.Status = engine.DetermineOverallStatus(status.Components)

	return status, nil
}

// Destroy removes all MLOps & Monitoring stage components from the cluster
// in reverse order: dashboards (no-op), monitoring, then MLflow.
func (s *MLOpsStage) Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error {
	totalComponents := 2

	// 1. Uninstall kube-prometheus-stack.
	installed, version, err := hc.IsInstalled(monitoringRelease, monitoringNS)
	if err != nil {
		return fmt.Errorf("checking monitoring: %w", err)
	}
	if installed {
		printer.ComponentStart(1, totalComponents, "monitoring", version, "destroying")
		err = hc.Uninstall(monitoringRelease, monitoringNS)
		printer.ComponentDone("monitoring", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped(1, totalComponents, "monitoring", "", "not installed")
	}

	// 2. Remove MLflow resources.
	printer.ComponentStart(2, totalComponents, "mlflow", "", "destroying")
	err = destroyMLflow(ctx, kc, printer)
	printer.ComponentDone("mlflow", err)
	if err != nil {
		return err
	}

	return nil
}
