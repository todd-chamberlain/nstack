package detect

import (
	"context"
	"fmt"

	"k8s.io/client-go/kubernetes"
)

// Result holds the full detection output for a cluster.
type Result struct {
	Kubernetes KubernetesInfo     `json:"kubernetes"`
	GPUs       []DetectedGPU      `json:"gpus"`
	Operators  []DetectedOperator `json:"operators"`
	Storage    []StorageClass     `json:"storage"`
}

// KubernetesInfo describes the discovered Kubernetes environment.
type KubernetesInfo struct {
	Distribution        string `json:"distribution"`        // k3s, kubeadm, eks, gke, aks, nebius, unknown
	Version             string `json:"version"`
	CgroupVersion       int    `json:"cgroupVersion"`       // 1 or 2
	ContainerdSocket    string `json:"containerdSocket"`
	DefaultStorageClass string `json:"defaultStorageClass"`
}

// DetectedGPU represents a GPU discovered on a cluster node.
type DetectedGPU struct {
	Model    string `json:"model"`
	VRAM     string `json:"vram"`
	UUID     string `json:"uuid"`
	NodeName string `json:"nodeName"`
	Count    int    `json:"count"`
}

// DetectedOperator represents a known operator and its install status.
type DetectedOperator struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"` // running, degraded, not-installed
}

// StorageClass represents a Kubernetes storage class.
type StorageClass struct {
	Name        string `json:"name"`
	IsDefault   bool   `json:"isDefault"`
	Provisioner string `json:"provisioner"`
}

// kubernetesResult is the internal return from detectKubernetes,
// carrying both the info and discovered storage classes.
type kubernetesResult struct {
	info    KubernetesInfo
	storage []StorageClass
}

// Run executes all sub-detectors and assembles the combined Result.
func Run(ctx context.Context, clientset kubernetes.Interface) (*Result, error) {
	k8s, err := detectKubernetes(ctx, clientset)
	if err != nil {
		return nil, fmt.Errorf("kubernetes detection: %w", err)
	}

	gpus, err := detectGPUs(ctx, clientset)
	if err != nil {
		return nil, fmt.Errorf("gpu detection: %w", err)
	}

	operators, err := detectOperators(ctx, clientset)
	if err != nil {
		return nil, fmt.Errorf("operator detection: %w", err)
	}

	return &Result{
		Kubernetes: k8s.info,
		GPUs:       gpus,
		Operators:  operators,
		Storage:    k8s.storage,
	}, nil
}
