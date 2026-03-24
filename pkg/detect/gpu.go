package detect

import (
	"context"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	labelGPUProduct = "nvidia.com/gpu.product"
	labelGPUMemory  = "nvidia.com/gpu.memory"
	labelGPUCount   = "nvidia.com/gpu.count"
	labelGPUUUID    = "nvidia.com/gpu.uuid"
	resourceNVGPU   = "nvidia.com/gpu"
)

// detectGPUs discovers NVIDIA GPUs by inspecting node labels and allocatable resources.
func detectGPUs(ctx context.Context, clientset kubernetes.Interface) ([]DetectedGPU, error) {
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var gpus []DetectedGPU
	for _, node := range nodes.Items {
		gpu := detectGPUFromNode(node)
		if gpu != nil {
			gpus = append(gpus, *gpu)
		}
	}
	return gpus, nil
}

// detectGPUFromNode checks a single node for GPU indicators.
func detectGPUFromNode(node corev1.Node) *DetectedGPU {
	labels := node.Labels
	if labels == nil {
		labels = map[string]string{}
	}

	model := labels[labelGPUProduct]
	vram := labels[labelGPUMemory]
	uuid := labels[labelGPUUUID]
	countStr := labels[labelGPUCount]

	count := 0
	if countStr != "" {
		if c, err := strconv.Atoi(countStr); err == nil {
			count = c
		}
	}

	// Fallback: check allocatable resources for GPU count.
	if count == 0 {
		if qty, ok := node.Status.Allocatable[corev1.ResourceName(resourceNVGPU)]; ok {
			count = int(qty.Value())
		}
	}

	// Also use allocatable to detect GPUs even without labels.
	if model == "" && count == 0 {
		if qty, ok := node.Status.Allocatable[corev1.ResourceName(resourceNVGPU)]; ok {
			count = int(qty.Value())
			if count > 0 {
				model = "unknown-nvidia"
			}
		}
	}

	if count == 0 && model == "" {
		return nil
	}

	if count == 0 {
		count = 1
	}

	return &DetectedGPU{
		Model:    model,
		VRAM:     vram,
		UUID:     uuid,
		NodeName: node.Name,
		Count:    count,
	}
}
