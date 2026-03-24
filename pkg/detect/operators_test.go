package detect

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/todd-chamberlain/nstack/pkg/engine"
)

func TestDetectOperators_Found(t *testing.T) {
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gpu-operator",
			Namespace: "gpu-operator",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "gpu-operator"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "gpu-operator"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "gpu-operator",
							Image: "nvcr.io/nvidia/gpu-operator:v24.9.0",
						},
					},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 1,
		},
	}

	fakeClient := fake.NewSimpleClientset(dep)
	ops, err := detectOperators(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have entries for all known operators.
	if len(ops) != len(operators) {
		t.Fatalf("expected %d operator results, got %d", len(operators), len(ops))
	}

	// gpu-operator should be found and running.
	var gpuOp *engine.DetectedOperator
	for i := range ops {
		if ops[i].Name == "gpu-operator" {
			gpuOp = &ops[i]
			break
		}
	}
	if gpuOp == nil {
		t.Fatal("gpu-operator not found in results")
	}
	if gpuOp.Status != "running" {
		t.Errorf("expected status=running, got %q", gpuOp.Status)
	}
	if gpuOp.Version != "v24.9.0" {
		t.Errorf("expected version=v24.9.0, got %q", gpuOp.Version)
	}

	// Others should be not-installed.
	for _, op := range ops {
		if op.Name != "gpu-operator" && op.Status != "not-installed" {
			t.Errorf("expected %s status=not-installed, got %q", op.Name, op.Status)
		}
	}
}

func TestDetectOperators_Degraded(t *testing.T) {
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cert-manager",
			Namespace: "cert-manager",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "cert-manager"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "cert-manager"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "cert-manager",
							Image: "quay.io/jetstack/cert-manager-controller:v1.14.0",
						},
					},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 0, // not yet ready
		},
	}

	fakeClient := fake.NewSimpleClientset(dep)
	ops, err := detectOperators(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var certMgr *engine.DetectedOperator
	for i := range ops {
		if ops[i].Name == "cert-manager" {
			certMgr = &ops[i]
			break
		}
	}
	if certMgr == nil {
		t.Fatal("cert-manager not found in results")
	}
	if certMgr.Status != "degraded" {
		t.Errorf("expected status=degraded for 0 available replicas, got %q", certMgr.Status)
	}
	if certMgr.Version != "v1.14.0" {
		t.Errorf("expected version=v1.14.0, got %q", certMgr.Version)
	}
}

func TestDetectOperators_NotInstalled(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	ops, err := detectOperators(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ops) != len(operators) {
		t.Fatalf("expected %d operator results, got %d", len(operators), len(ops))
	}

	for _, op := range ops {
		if op.Status != "not-installed" {
			t.Errorf("expected %s status=not-installed, got %q", op.Name, op.Status)
		}
		if op.Version != "" {
			t.Errorf("expected empty version for not-installed %s, got %q", op.Name, op.Version)
		}
	}
}
