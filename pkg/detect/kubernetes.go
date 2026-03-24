package detect

import (
	"context"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// detectKubernetes probes the cluster for distribution, version, cgroup info,
// containerd socket path, and storage classes.
func detectKubernetes(ctx context.Context, clientset kubernetes.Interface) (*kubernetesResult, error) {
	sv, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return nil, err
	}

	distro := detectDistribution(sv.GitVersion)
	cgroupVer := 2 // modern default
	socket := "/run/containerd/containerd.sock"

	if distro == "k3s" {
		socket = "/run/k3s/containerd/containerd.sock"
	}

	// Detect cgroup version from node labels.
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err == nil && len(nodes.Items) > 0 {
		cgroupVer = detectCgroupVersion(nodes.Items[0].Labels, sv.GitVersion)
	}

	// Discover storage classes.
	var storageClasses []StorageClass
	var defaultSC string

	scList, err := clientset.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, sc := range scList.Items {
			isDefault := false
			if sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
				isDefault = true
				defaultSC = sc.Name
			}
			storageClasses = append(storageClasses, StorageClass{
				Name:        sc.Name,
				IsDefault:   isDefault,
				Provisioner: sc.Provisioner,
			})
		}
	}

	return &kubernetesResult{
		info: KubernetesInfo{
			Distribution:        distro,
			Version:             sv.GitVersion,
			CgroupVersion:       cgroupVer,
			ContainerdSocket:    socket,
			DefaultStorageClass: defaultSC,
		},
		storage: storageClasses,
	}, nil
}

// detectDistribution infers the Kubernetes distribution from the GitVersion string.
func detectDistribution(gitVersion string) string {
	switch {
	case strings.Contains(gitVersion, "+k3s"):
		return "k3s"
	case strings.Contains(gitVersion, "-eks"):
		return "eks"
	case strings.Contains(gitVersion, "-gke"):
		return "gke"
	case strings.Contains(gitVersion, "-aks"):
		return "aks"
	case strings.Contains(gitVersion, "+"):
		// Vanilla kubeadm builds use a "+" separator without a known suffix.
		return "kubeadm"
	default:
		return "unknown"
	}
}

// detectCgroupVersion determines the cgroup version from node labels or K8s version.
func detectCgroupVersion(labels map[string]string, gitVersion string) int {
	// NFD (Node Feature Discovery) label for cgroup v2.
	if v, ok := labels["feature.node.kubernetes.io/cpu-cgroup.v2"]; ok {
		if v == "true" {
			return 2
		}
		return 1
	}

	// Fallback: Kubernetes >= 1.25 defaults to cgroup v2.
	minor := parseMinorVersion(gitVersion)
	if minor >= 25 {
		return 2
	}
	return 1
}

// parseMinorVersion extracts the minor version number from a GitVersion string
// such as "v1.31.4+k3s1".
func parseMinorVersion(gitVersion string) int {
	v := strings.TrimPrefix(gitVersion, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	return minor
}
