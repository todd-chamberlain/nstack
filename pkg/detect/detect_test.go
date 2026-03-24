package detect

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRun_Full(t *testing.T) {
	// GPU node.
	gpuNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-worker",
			Labels: map[string]string{
				"nvidia.com/gpu.product": "Tesla-T400",
				"nvidia.com/gpu.memory":  "4096",
				"nvidia.com/gpu.count":   "2",
			},
		},
	}

	// Storage class.
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "local-path",
			Annotations: map[string]string{
				"storageclass.kubernetes.io/is-default-class": "true",
			},
		},
		Provisioner: "rancher.io/local-path",
	}

	// GPU operator deployment.
	replicas := int32(1)
	gpuOpDep := &appsv1.Deployment{
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

	fakeClient := fake.NewSimpleClientset(gpuNode, sc, gpuOpDep)
	fakeClient.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{
		GitVersion: "v1.31.4+k3s1",
	}

	result, err := Run(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Kubernetes info.
	if result.Kubernetes.Distribution != "k3s" {
		t.Errorf("expected distribution=k3s, got %q", result.Kubernetes.Distribution)
	}
	if result.Kubernetes.Version != "v1.31.4+k3s1" {
		t.Errorf("expected version=v1.31.4+k3s1, got %q", result.Kubernetes.Version)
	}
	if result.Kubernetes.DefaultStorageClass != "local-path" {
		t.Errorf("expected defaultStorageClass=local-path, got %q", result.Kubernetes.DefaultStorageClass)
	}

	// GPUs.
	if len(result.GPUs) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(result.GPUs))
	}
	if result.GPUs[0].Model != "Tesla-T400" {
		t.Errorf("expected GPU model=Tesla-T400, got %q", result.GPUs[0].Model)
	}
	if result.GPUs[0].Count != 2 {
		t.Errorf("expected GPU count=2, got %d", result.GPUs[0].Count)
	}

	// Operators.
	if len(result.Operators) != len(operators) {
		t.Fatalf("expected %d operators, got %d", len(operators), len(result.Operators))
	}
	var gpuOp *DetectedOperator
	for i := range result.Operators {
		if result.Operators[i].Name == "gpu-operator" {
			gpuOp = &result.Operators[i]
			break
		}
	}
	if gpuOp == nil {
		t.Fatal("gpu-operator not in results")
	}
	if gpuOp.Status != "running" {
		t.Errorf("expected gpu-operator status=running, got %q", gpuOp.Status)
	}
	if gpuOp.Version != "v24.9.0" {
		t.Errorf("expected gpu-operator version=v24.9.0, got %q", gpuOp.Version)
	}

	// Storage.
	if len(result.Storage) != 1 {
		t.Fatalf("expected 1 storage class, got %d", len(result.Storage))
	}
	if !result.Storage[0].IsDefault {
		t.Error("expected storage class to be default")
	}
}
