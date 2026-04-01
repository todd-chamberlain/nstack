package detect

import (
	"context"
	"testing"

	storagev1 "k8s.io/api/storage/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDetectK3s(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "k3s-node",
		},
	}
	fakeClient := fake.NewSimpleClientset(node)
	fakeClient.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{
		GitVersion: "v1.31.4+k3s1",
	}

	result, err := detectKubernetes(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.info.Distribution != "k3s" {
		t.Errorf("expected distribution=k3s, got %q", result.info.Distribution)
	}
	if result.info.Version != "v1.31.4+k3s1" {
		t.Errorf("expected version=v1.31.4+k3s1, got %q", result.info.Version)
	}
	if result.info.ContainerdSocket != "/run/k3s/containerd/containerd.sock" {
		t.Errorf("expected k3s containerd socket, got %q", result.info.ContainerdSocket)
	}
	if result.info.CgroupVersion != 2 {
		t.Errorf("expected cgroupVersion=2, got %d", result.info.CgroupVersion)
	}
}

func TestDetectKubeadm(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubeadm-node",
		},
	}
	fakeClient := fake.NewSimpleClientset(node)
	fakeClient.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{
		GitVersion: "v1.31.4",
	}

	result, err := detectKubernetes(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.info.Distribution != "unknown" {
		t.Errorf("expected distribution=unknown for bare version, got %q", result.info.Distribution)
	}
	if result.info.ContainerdSocket != "/run/containerd/containerd.sock" {
		t.Errorf("expected default containerd socket, got %q", result.info.ContainerdSocket)
	}
}

func TestDetectKubeadmWithPlus(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubeadm-node",
		},
	}
	fakeClient := fake.NewSimpleClientset(node)
	fakeClient.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{
		GitVersion: "v1.31.4+build123",
	}

	result, err := detectKubernetes(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.info.Distribution != "kubeadm" {
		t.Errorf("expected distribution=kubeadm for +unknown suffix, got %q", result.info.Distribution)
	}
}

func TestDetectEKS(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "eks-node",
		},
	}
	fakeClient := fake.NewSimpleClientset(node)
	fakeClient.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{
		GitVersion: "v1.29.0-eks-abc123",
	}

	result, err := detectKubernetes(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.info.Distribution != "eks" {
		t.Errorf("expected distribution=eks, got %q", result.info.Distribution)
	}
}

func TestDetectCgroupV2FromLabel(t *testing.T) {
	labels := map[string]string{
		"feature.node.kubernetes.io/cpu-cgroup.v2": "true",
	}
	v := detectCgroupVersion(labels, "v1.24.0")
	if v != 2 {
		t.Errorf("expected cgroup v2 from label, got %d", v)
	}
}

func TestDetectCgroupV1FromLabel(t *testing.T) {
	labels := map[string]string{
		"feature.node.kubernetes.io/cpu-cgroup.v2": "false",
	}
	v := detectCgroupVersion(labels, "v1.30.0")
	if v != 1 {
		t.Errorf("expected cgroup v1 from false label, got %d", v)
	}
}

func TestDetectCgroupV1FromOldVersion(t *testing.T) {
	v := detectCgroupVersion(map[string]string{}, "v1.23.0")
	if v != 1 {
		t.Errorf("expected cgroup v1 for K8s 1.23, got %d", v)
	}
}

func TestStorageClassDetection(t *testing.T) {
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "local-path",
			Annotations: map[string]string{
				"storageclass.kubernetes.io/is-default-class": "true",
			},
		},
		Provisioner: "rancher.io/local-path",
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
		},
	}

	fakeClient := fake.NewSimpleClientset(sc, node)
	fakeClient.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{
		GitVersion: "v1.31.4+k3s1",
	}

	result, err := detectKubernetes(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.info.DefaultStorageClass != "local-path" {
		t.Errorf("expected default storage class=local-path, got %q", result.info.DefaultStorageClass)
	}
	if len(result.storage) != 1 {
		t.Fatalf("expected 1 storage class, got %d", len(result.storage))
	}
	if !result.storage[0].IsDefault {
		t.Error("expected storage class to be marked as default")
	}
	if result.storage[0].Provisioner != "rancher.io/local-path" {
		t.Errorf("expected provisioner=rancher.io/local-path, got %q", result.storage[0].Provisioner)
	}
}

func TestDetectNebius(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nebius-gpu-node-0",
			Labels: map[string]string{
				"nebius.com/node-group-id": "ng-abc123",
				"nebius.com/platform":      "gpu-h100-sxm",
			},
		},
	}
	fakeClient := fake.NewSimpleClientset(node)
	fakeClient.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{
		GitVersion: "v1.31.2",
	}

	result, err := detectKubernetes(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.info.Distribution != "nebius" {
		t.Errorf("expected distribution=nebius for node with nebius.com/ labels, got %q", result.info.Distribution)
	}
	if result.info.ContainerdSocket != "/run/containerd/containerd.sock" {
		t.Errorf("expected default containerd socket for nebius, got %q", result.info.ContainerdSocket)
	}
}

func TestDetectNebiusNotFalsePositive(t *testing.T) {
	// Standard node without nebius labels should not detect as nebius.
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "generic-node",
			Labels: map[string]string{
				"kubernetes.io/os": "linux",
			},
		},
	}
	fakeClient := fake.NewSimpleClientset(node)
	fakeClient.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{
		GitVersion: "v1.31.2",
	}

	result, err := detectKubernetes(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.info.Distribution == "nebius" {
		t.Errorf("expected non-nebius distribution for node without nebius labels, got %q", result.info.Distribution)
	}
}

func TestParseMinorVersion(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"v1.31.4+k3s1", 31},
		{"v1.25.0", 25},
		{"v1.24.0-eks-abc", 24},
		{"invalid", 0},
	}
	for _, tt := range tests {
		got := parseMinorVersion(tt.input)
		if got != tt.want {
			t.Errorf("parseMinorVersion(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
