package s6_mlops

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
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
	result := &engine.DetectResult{}
	cs := kc.Clientset()

	// Check MLflow deployment.
	mlDep, err := cs.AppsV1().Deployments(mlflowNamespace).Get(ctx, mlflowName, metav1.GetOptions{})
	if err == nil {
		opStatus := "degraded"
		if mlDep.Status.AvailableReplicas >= 1 {
			opStatus = "running"
		}
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "mlflow",
			Version:   kube.ExtractImageVersion(mlDep.Spec.Template.Spec.Containers),
			Namespace: mlflowNamespace,
			Status:    opStatus,
		})
	} else {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "mlflow",
			Namespace: mlflowNamespace,
			Status:    "not-installed",
		})
	}

	// Check kube-prometheus-stack via Helm release.
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
	_, err := kc.Clientset().CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return fmt.Errorf("cluster not reachable: %w", err)
	}
	return nil
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
	status := &engine.StageStatus{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	cs := kc.Clientset()

	// Check MLflow deployment.
	mlStatus := engine.ComponentStatus{
		Name:      "mlflow",
		Namespace: mlflowNamespace,
	}
	mlDep, err := cs.AppsV1().Deployments(mlflowNamespace).Get(ctx, mlflowName, metav1.GetOptions{})
	if err != nil {
		mlStatus.Status = "not-installed"
	} else {
		mlStatus.Pods = int(mlDep.Status.Replicas)
		mlStatus.Ready = int(mlDep.Status.ReadyReplicas)
		mlStatus.Version = kube.ExtractImageVersion(mlDep.Spec.Template.Spec.Containers)
		if mlDep.Status.AvailableReplicas >= 1 {
			mlStatus.Status = "running"
		} else {
			mlStatus.Status = "degraded"
		}
		status.Version = mlStatus.Version
		status.Applied = mlDep.CreationTimestamp.Time
	}
	status.Components = append(status.Components, mlStatus)

	// Check monitoring components: Prometheus, Grafana, Alertmanager.
	monComponents := []struct {
		name       string
		deployment string
		namespace  string
	}{
		{"prometheus", "prometheus-kube-prometheus-stack-prometheus", monitoringNS},
		{"grafana", "kube-prometheus-stack-grafana", monitoringNS},
		{"alertmanager", "alertmanager-kube-prometheus-stack-alertmanager", monitoringNS},
	}

	for _, mc := range monComponents {
		compStatus := engine.ComponentStatus{
			Name:      mc.name,
			Namespace: mc.namespace,
		}

		// Try as Deployment first, then as StatefulSet.
		var dep *appsv1.Deployment
		dep, err = cs.AppsV1().Deployments(mc.namespace).Get(ctx, mc.deployment, metav1.GetOptions{})
		if err == nil {
			compStatus.Pods = int(dep.Status.Replicas)
			compStatus.Ready = int(dep.Status.ReadyReplicas)
			compStatus.Version = kube.ExtractImageVersion(dep.Spec.Template.Spec.Containers)
			if dep.Status.AvailableReplicas >= 1 {
				compStatus.Status = "running"
			} else {
				compStatus.Status = "degraded"
			}
		} else {
			// Try StatefulSet (Prometheus and Alertmanager use StatefulSets).
			ss, ssErr := cs.AppsV1().StatefulSets(mc.namespace).Get(ctx, mc.deployment, metav1.GetOptions{})
			if ssErr == nil {
				compStatus.Pods = int(ss.Status.Replicas)
				compStatus.Ready = int(ss.Status.ReadyReplicas)
				compStatus.Version = kube.ExtractImageVersion(ss.Spec.Template.Spec.Containers)
				if ss.Status.ReadyReplicas >= 1 {
					compStatus.Status = "running"
				} else {
					compStatus.Status = "degraded"
				}
			} else {
				compStatus.Status = "not-installed"
			}
		}

		status.Components = append(status.Components, compStatus)
	}

	// Determine overall status.
	allRunning := true
	anyNotInstalled := false
	for _, c := range status.Components {
		if c.Status != "running" {
			allRunning = false
		}
		if c.Status == "not-installed" {
			anyNotInstalled = true
		}
	}

	switch {
	case anyNotInstalled:
		status.Status = "not-installed"
	case allRunning:
		status.Status = "deployed"
	default:
		status.Status = "degraded"
	}

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
