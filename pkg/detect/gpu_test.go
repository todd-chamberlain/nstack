package detect

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDetectGPUs_FromLabels(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-1",
			Labels: map[string]string{
				"nvidia.com/gpu.product": "Tesla-T400",
				"nvidia.com/gpu.memory":  "4096",
				"nvidia.com/gpu.count":   "2",
				"nvidia.com/gpu.uuid":    "GPU-abc123",
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(node)
	gpus, err := detectGPUs(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gpus) != 1 {
		t.Fatalf("expected 1 GPU entry, got %d", len(gpus))
	}

	gpu := gpus[0]
	if gpu.Model != "Tesla-T400" {
		t.Errorf("expected model=Tesla-T400, got %q", gpu.Model)
	}
	if gpu.VRAM != "4096" {
		t.Errorf("expected vram=4096, got %q", gpu.VRAM)
	}
	if gpu.Count != 2 {
		t.Errorf("expected count=2, got %d", gpu.Count)
	}
	if gpu.UUID != "GPU-abc123" {
		t.Errorf("expected uuid=GPU-abc123, got %q", gpu.UUID)
	}
	if gpu.NodeName != "gpu-node-1" {
		t.Errorf("expected nodeName=gpu-node-1, got %q", gpu.NodeName)
	}
}

func TestDetectGPUs_FromAllocatable(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-2",
			Labels: map[string]string{
				"nvidia.com/gpu.product": "NVIDIA-A100",
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): newGPUQuantity(4),
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(node)
	gpus, err := detectGPUs(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gpus) != 1 {
		t.Fatalf("expected 1 GPU entry, got %d", len(gpus))
	}

	if gpus[0].Count != 4 {
		t.Errorf("expected count=4 from allocatable, got %d", gpus[0].Count)
	}
	if gpus[0].Model != "NVIDIA-A100" {
		t.Errorf("expected model=NVIDIA-A100, got %q", gpus[0].Model)
	}
}

func TestDetectGPUs_NoGPU(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "cpu-node",
			Labels: map[string]string{},
		},
	}

	fakeClient := fake.NewSimpleClientset(node)
	gpus, err := detectGPUs(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gpus) != 0 {
		t.Errorf("expected no GPUs, got %d", len(gpus))
	}
}

func TestDetectGPUs_MultipleNodes(t *testing.T) {
	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-a",
			Labels: map[string]string{
				"nvidia.com/gpu.product": "Tesla-T400",
				"nvidia.com/gpu.count":   "2",
			},
		},
	}
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-b",
			Labels: map[string]string{
				"nvidia.com/gpu.product": "RTX-A2000",
				"nvidia.com/gpu.count":   "1",
			},
		},
	}
	node3 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "cpu-only-node",
			Labels: map[string]string{},
		},
	}

	fakeClient := fake.NewSimpleClientset(node1, node2, node3)
	gpus, err := detectGPUs(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gpus) != 2 {
		t.Fatalf("expected 2 GPU entries, got %d", len(gpus))
	}
}
