package s6_mlops

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/todd-chamberlain/nstack/pkg/kube"
)

func int32Ptr(i int32) *int32 { return &i }

// makeMLflowDeployment builds a minimal MLflow Deployment.
func makeMLflowDeployment(available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mlflowName,
			Namespace: "slurm",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "mlflow"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "mlflow"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  mlflowName,
						Image: "ghcr.io/mlflow/mlflow:2.18.0",
					}},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: available,
			Replicas:          1,
			ReadyReplicas:     available,
		},
	}
}

// makePrometheusStatefulSet builds a minimal Prometheus StatefulSet.
func makePrometheusStatefulSet(ready int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prometheus-kube-prometheus-stack-prometheus",
			Namespace: monitoringNS,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "prometheus"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "prometheus"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "prometheus",
						Image: "quay.io/prometheus/prometheus:v2.55.0",
					}},
				},
			},
		},
		Status: appsv1.StatefulSetStatus{
			Replicas:      1,
			ReadyReplicas: ready,
		},
	}
}

// makeGrafanaDeployment builds a minimal Grafana Deployment.
func makeGrafanaDeployment(available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-prometheus-stack-grafana",
			Namespace: monitoringNS,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "grafana"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "grafana"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "grafana",
						Image: "grafana/grafana:10.4.0",
					}},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: available,
			Replicas:          1,
			ReadyReplicas:     available,
		},
	}
}

// makeAlertmanagerStatefulSet builds a minimal Alertmanager StatefulSet.
func makeAlertmanagerStatefulSet(ready int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "alertmanager-kube-prometheus-stack-alertmanager",
			Namespace: monitoringNS,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "alertmanager"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "alertmanager"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "alertmanager",
						Image: "quay.io/prometheus/alertmanager:v0.27.0",
					}},
				},
			},
		},
		Status: appsv1.StatefulSetStatus{
			Replicas:      1,
			ReadyReplicas: ready,
		},
	}
}

// Note: Detect() and Plan() internally call helm.NewClient(kc.Kubeconfig())
// which uses the real kubeconfig and can find real Helm releases on this host.
// Tests for those methods focus on the K8s-clientset-driven logic (MLflow)
// and accept that the Helm-checked monitoring component may vary by env.

func TestMLOpsStage_Detect_MLflowFound(t *testing.T) {
	cs := fake.NewSimpleClientset(makeMLflowDeployment(1))
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	result, err := stage.Detect(ctx, kc)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}

	if len(result.Operators) < 1 {
		t.Fatal("expected at least 1 operator")
	}

	mlflowOp := result.Operators[0]
	if mlflowOp.Name != "mlflow" {
		t.Errorf("expected first operator name=mlflow, got %s", mlflowOp.Name)
	}
	if mlflowOp.Status != "running" {
		t.Errorf("mlflow: expected status=running, got %s", mlflowOp.Status)
	}
	if mlflowOp.Version == "" {
		t.Error("mlflow version should not be empty")
	}
}

func TestMLOpsStage_Detect_MLflowNotFound(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	result, err := stage.Detect(ctx, kc)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}

	if len(result.Operators) < 1 {
		t.Fatal("expected at least 1 operator")
	}

	// MLflow (index 0, K8s-based detection) should be not-installed.
	mlflowOp := result.Operators[0]
	if mlflowOp.Name != "mlflow" {
		t.Errorf("expected first operator name=mlflow, got %s", mlflowOp.Name)
	}
	if mlflowOp.Status != "not-installed" {
		t.Errorf("mlflow: expected status=not-installed, got %s", mlflowOp.Status)
	}
}

func TestMLOpsStage_Detect_MLflowDegraded(t *testing.T) {
	cs := fake.NewSimpleClientset(makeMLflowDeployment(0))
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	result, err := stage.Detect(ctx, kc)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}

	mlflowOp := result.Operators[0]
	if mlflowOp.Status != "degraded" {
		t.Errorf("mlflow: expected status=degraded, got %s", mlflowOp.Status)
	}
}

func TestMLOpsStage_Plan_MLflowNew(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	plan, err := stage.Plan(ctx, kc, nil, nil)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	// Should have 3 components: mlflow, monitoring, soperator-dashboards.
	if len(plan.Components) != 3 {
		t.Fatalf("expected 3 components, got %d", len(plan.Components))
	}

	// MLflow (K8s-based) should be install.
	mlflowComp := plan.Components[0]
	if mlflowComp.Name != "mlflow" || mlflowComp.Action != "install" {
		t.Errorf("mlflow: expected action=install, got %s", mlflowComp.Action)
	}

	// soperator-dashboards is always action=install.
	dashComp := plan.Components[2]
	if dashComp.Name != "soperator-dashboards" || dashComp.Action != "install" {
		t.Errorf("dashboards: expected action=install, got %s", dashComp.Action)
	}
}

func TestMLOpsStage_Plan_MLflowExists(t *testing.T) {
	cs := fake.NewSimpleClientset(makeMLflowDeployment(1))
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	plan, err := stage.Plan(ctx, kc, nil, nil)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	if len(plan.Components) != 3 {
		t.Fatalf("expected 3 components, got %d", len(plan.Components))
	}

	// MLflow should be skip.
	mlflowComp := plan.Components[0]
	if mlflowComp.Name != "mlflow" {
		t.Errorf("expected first component=mlflow, got %s", mlflowComp.Name)
	}
	if mlflowComp.Action != "skip" {
		t.Errorf("mlflow: expected action=skip, got %s", mlflowComp.Action)
	}

	// Monitoring uses Helm-based detection; just verify it exists in the plan.
	monComp := plan.Components[1]
	if monComp.Name != "monitoring" {
		t.Errorf("expected second component=monitoring, got %s", monComp.Name)
	}
}

func TestMLOpsStage_Status_NotInstalled(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	status, err := stage.Status(ctx, kc)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Status != "not-installed" {
		t.Errorf("expected status=not-installed, got %s", status.Status)
	}

	// All components should be not-installed.
	for _, c := range status.Components {
		if c.Status != "not-installed" {
			t.Errorf("component %s: expected status=not-installed, got %s", c.Name, c.Status)
		}
	}
}

func TestMLOpsStage_Status_AllRunning(t *testing.T) {
	cs := fake.NewSimpleClientset(
		makeMLflowDeployment(1),
		makePrometheusStatefulSet(1),
		makeGrafanaDeployment(1),
		makeAlertmanagerStatefulSet(1),
	)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	status, err := stage.Status(ctx, kc)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Status != "deployed" {
		t.Errorf("expected status=deployed, got %s", status.Status)
	}

	// Verify all 4 components are running.
	if len(status.Components) != 4 {
		t.Fatalf("expected 4 components, got %d", len(status.Components))
	}
	for _, c := range status.Components {
		if c.Status != "running" {
			t.Errorf("component %s: expected status=running, got %s", c.Name, c.Status)
		}
	}
}

func TestMLOpsStage_Status_Partial(t *testing.T) {
	// Only MLflow and Prometheus running, Grafana and Alertmanager missing.
	cs := fake.NewSimpleClientset(
		makeMLflowDeployment(1),
		makePrometheusStatefulSet(1),
	)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	status, err := stage.Status(ctx, kc)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}

	// Some not-installed components means overall is "not-installed".
	if status.Status != "not-installed" {
		t.Errorf("expected status=not-installed (missing components), got %s", status.Status)
	}

	// Check MLflow component is running.
	if status.Components[0].Name != "mlflow" {
		t.Errorf("expected first component=mlflow, got %s", status.Components[0].Name)
	}
	if status.Components[0].Status != "running" {
		t.Errorf("mlflow: expected status=running, got %s", status.Components[0].Status)
	}

	// Check Prometheus component is running.
	if status.Components[1].Name != "prometheus" {
		t.Errorf("expected second component=prometheus, got %s", status.Components[1].Name)
	}
	if status.Components[1].Status != "running" {
		t.Errorf("prometheus: expected status=running, got %s", status.Components[1].Status)
	}
}

func TestMLOpsStage_Status_Degraded(t *testing.T) {
	// All components exist but MLflow has 0 available replicas.
	cs := fake.NewSimpleClientset(
		makeMLflowDeployment(0), // degraded
		makePrometheusStatefulSet(1),
		makeGrafanaDeployment(1),
		makeAlertmanagerStatefulSet(1),
	)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	status, err := stage.Status(ctx, kc)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Status != "degraded" {
		t.Errorf("expected status=degraded, got %s", status.Status)
	}
}

func TestMLOpsStage_Validate(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	err := stage.Validate(ctx, kc, nil)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestMLOpsStage_Metadata(t *testing.T) {
	stage := New()
	if stage.Number() != 6 {
		t.Errorf("expected Number()=6, got %d", stage.Number())
	}
	if stage.Name() != "MLOps & Monitoring" {
		t.Errorf("expected Name()=MLOps & Monitoring, got %s", stage.Name())
	}
	if len(stage.Dependencies()) != 0 {
		t.Errorf("expected no dependencies, got %v", stage.Dependencies())
	}
}
