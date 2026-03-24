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
			Namespace: mlflowNamespace,
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

func TestMLOpsStage_Detect_Found(t *testing.T) {
	cs := fake.NewSimpleClientset(
		makeMLflowDeployment(1),
		makePrometheusStatefulSet(1),
	)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	result, err := stage.Detect(ctx, kc)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}

	// The Detect method checks MLflow via deployment and kube-prometheus-stack via Helm.
	// Since we can't fake Helm, the prometheus check will return not-installed.
	// But MLflow should be detected.
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

func TestMLOpsStage_Detect_NotFound(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	result, err := stage.Detect(ctx, kc)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}

	// MLflow (K8s-based detection) should be not-installed.
	// kube-prometheus-stack uses Helm IsInstalled which may behave differently with fake clients.
	for _, op := range result.Operators {
		if op.Name == "mlflow" && op.Status != "not-installed" {
			t.Errorf("mlflow: expected status=not-installed, got %s", op.Status)
		}
	}
}

func TestMLOpsStage_Plan_AllNew(t *testing.T) {
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

	// MLflow (K8s-based) should be install. Monitoring uses Helm detection
	// which may vary with fake clients. Just verify mlflow and dashboards.
	mlflowComp := plan.Components[0]
	if mlflowComp.Name != "mlflow" || mlflowComp.Action != "install" {
		t.Errorf("mlflow: expected action=install, got %s", mlflowComp.Action)
	}

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

	// MLflow should be skip, monitoring and dashboards should be install.
	if len(plan.Components) != 3 {
		t.Fatalf("expected 3 components, got %d", len(plan.Components))
	}

	mlflowComp := plan.Components[0]
	if mlflowComp.Name != "mlflow" {
		t.Errorf("expected first component=mlflow, got %s", mlflowComp.Name)
	}
	if mlflowComp.Action != "skip" {
		t.Errorf("mlflow: expected action=skip, got %s", mlflowComp.Action)
	}

	// Monitoring uses Helm-based detection which can't be reliably faked.
	// Just verify it exists in the plan.
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
}

func TestMLOpsStage_Status_Partial(t *testing.T) {
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

	// MLflow is running but grafana and alertmanager are not installed,
	// so overall should not be "deployed".
	if status.Status == "deployed" {
		t.Error("status should not be 'deployed' with missing monitoring components")
	}

	// Check MLflow component.
	if len(status.Components) < 1 {
		t.Fatal("expected at least 1 component")
	}
	if status.Components[0].Status != "running" {
		t.Errorf("mlflow: expected status=running, got %s", status.Components[0].Status)
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
