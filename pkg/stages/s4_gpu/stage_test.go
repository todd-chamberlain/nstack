package s4_gpu

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/kube"
)

func int32Ptr(i int32) *int32 { return &i }

// makeGPUDeployment builds a minimal Deployment for the gpu-operator.
func makeGPUDeployment(available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gpuOperatorRelease,
			Namespace: gpuOperatorNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "gpu-operator"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "gpu-operator"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "gpu-operator",
						Image: "nvcr.io/nvidia/gpu-operator:v25.10.1",
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

// makeCertManagerDeployment builds a minimal Deployment for cert-manager.
func makeCertManagerDeployment(available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      certManagerRelease,
			Namespace: certManagerNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "cert-manager"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "cert-manager"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "cert-manager",
						Image: "quay.io/jetstack/cert-manager-controller:v1.17.2",
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

func TestGPUStage_Detect_Found(t *testing.T) {
	cs := fake.NewSimpleClientset(
		makeCertManagerDeployment(1),
		makeGPUDeployment(1),
	)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	result, err := stage.Detect(ctx, kc)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if len(result.Operators) != 2 {
		t.Fatalf("expected 2 operators, got %d", len(result.Operators))
	}

	for _, op := range result.Operators {
		if op.Status != "running" {
			t.Errorf("operator %s: expected status=running, got %s", op.Name, op.Status)
		}
	}

	// Verify cert-manager was detected with its version.
	cm := result.Operators[0]
	if cm.Name != "cert-manager" {
		t.Errorf("expected first operator name=cert-manager, got %s", cm.Name)
	}
	if cm.Version == "" {
		t.Error("cert-manager version should not be empty")
	}

	// Verify gpu-operator was detected with its version.
	gpu := result.Operators[1]
	if gpu.Name != "gpu-operator" {
		t.Errorf("expected second operator name=gpu-operator, got %s", gpu.Name)
	}
	if gpu.Version == "" {
		t.Error("gpu-operator version should not be empty")
	}
}

func TestGPUStage_Detect_NotFound(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	result, err := stage.Detect(ctx, kc)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if len(result.Operators) != 2 {
		t.Fatalf("expected 2 operators, got %d", len(result.Operators))
	}

	for _, op := range result.Operators {
		if op.Status != "not-installed" {
			t.Errorf("operator %s: expected status=not-installed, got %s", op.Name, op.Status)
		}
	}
}

func TestGPUStage_Plan_BothNew(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	plan, err := stage.Plan(ctx, kc, &config.Profile{Name: "test"}, nil)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(plan.Components) != 2 {
		t.Fatalf("expected 2 components, got %d", len(plan.Components))
	}

	for _, comp := range plan.Components {
		if comp.Action != "install" {
			t.Errorf("component %s: expected action=install, got %s", comp.Name, comp.Action)
		}
	}

	if plan.Action != "install" {
		t.Errorf("expected plan action=install, got %s", plan.Action)
	}
}

func TestGPUStage_Plan_BothExist(t *testing.T) {
	cs := fake.NewSimpleClientset(
		makeCertManagerDeployment(1),
		makeGPUDeployment(1),
	)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	plan, err := stage.Plan(ctx, kc, &config.Profile{Name: "test"}, nil)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(plan.Components) != 2 {
		t.Fatalf("expected 2 components, got %d", len(plan.Components))
	}

	for _, comp := range plan.Components {
		if comp.Action != "skip" {
			t.Errorf("component %s: expected action=skip, got %s", comp.Name, comp.Action)
		}
	}

	if plan.Action != "skip" {
		t.Errorf("expected plan action=skip, got %s", plan.Action)
	}
}

func TestGPUStage_Validate(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	err := stage.Validate(ctx, kc, &config.Profile{Name: "test"})
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestGPUStage_Status_Running(t *testing.T) {
	cs := fake.NewSimpleClientset(
		makeCertManagerDeployment(1),
		makeGPUDeployment(1),
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
	if len(status.Components) != 2 {
		t.Fatalf("expected 2 components, got %d", len(status.Components))
	}
	for _, c := range status.Components {
		if c.Status != "running" {
			t.Errorf("component %s: expected status=running, got %s", c.Name, c.Status)
		}
	}
}

func TestGPUStage_Status_NotInstalled(t *testing.T) {
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

func TestGPUStage_Metadata(t *testing.T) {
	stage := New()
	if stage.Number() != 4 {
		t.Errorf("expected Number()=4, got %d", stage.Number())
	}
	if stage.Name() != "GPU Stack" {
		t.Errorf("expected Name()=GPU Stack, got %s", stage.Name())
	}
	if len(stage.Dependencies()) != 0 {
		t.Errorf("expected no dependencies, got %v", stage.Dependencies())
	}
}
