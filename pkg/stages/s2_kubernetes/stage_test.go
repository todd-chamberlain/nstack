package s2_kubernetes

import (
	"bytes"
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

// makeNode builds a minimal Node object with the given name and Ready condition.
func makeNode(name string, ready bool) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			CreationTimestamp: metav1.Now(),
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: status,
				},
			},
		},
	}
}

func TestKubernetesStage_Metadata(t *testing.T) {
	stage := New()
	if stage.Number() != 2 {
		t.Errorf("expected Number()=2, got %d", stage.Number())
	}
	if stage.Name() != "Kubernetes" {
		t.Errorf("expected Name()=Kubernetes, got %s", stage.Name())
	}
	deps := stage.Dependencies()
	if len(deps) != 1 || deps[0] != 1 {
		t.Errorf("expected Dependencies()=[1], got %v", deps)
	}
}

func TestKubernetesStage_Detect_ClusterExists(t *testing.T) {
	cs := fake.NewSimpleClientset(makeNode("node-1", true))
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	result, err := stage.Detect(ctx, kc)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if len(result.Operators) != 1 {
		t.Fatalf("expected 1 operator, got %d", len(result.Operators))
	}

	op := result.Operators[0]
	if op.Name != "kubernetes" {
		t.Errorf("expected operator name=kubernetes, got %s", op.Name)
	}
	if op.Status != "running" {
		t.Errorf("expected status=running, got %s", op.Status)
	}
	if op.Version == "" {
		t.Error("expected non-empty version when cluster is running")
	}
}

func TestKubernetesStage_Detect_NoCluster(t *testing.T) {
	stage := New()
	ctx := context.Background()

	// nil kube client — no cluster connection.
	result, err := stage.Detect(ctx, nil)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if len(result.Operators) != 1 {
		t.Fatalf("expected 1 operator, got %d", len(result.Operators))
	}

	op := result.Operators[0]
	if op.Name != "kubernetes" {
		t.Errorf("expected operator name=kubernetes, got %s", op.Name)
	}
	if op.Status != "not-installed" {
		t.Errorf("expected status=not-installed, got %s", op.Status)
	}
}

func TestKubernetesStage_Plan_Existing(t *testing.T) {
	cs := fake.NewSimpleClientset(makeNode("node-1", true))
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	profile := &config.Profile{
		Name: "test",
		Kubernetes: config.ProfileKubernetes{
			Distribution: "k3s",
		},
	}

	plan, err := stage.Plan(ctx, kc, profile, nil)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if plan.Action != "skip" {
		t.Errorf("expected plan action=skip when cluster exists, got %s", plan.Action)
	}

	// All components should be skip.
	for _, comp := range plan.Components {
		if comp.Action != "skip" {
			t.Errorf("component %s: expected action=skip, got %s", comp.Name, comp.Action)
		}
	}
}

func TestKubernetesStage_Plan_NeedBootstrap(t *testing.T) {
	stage := New()
	ctx := context.Background()

	profile := &config.Profile{
		Name: "test",
		Kubernetes: config.ProfileKubernetes{
			Distribution: "k3s",
		},
	}

	// nil kube client — no cluster.
	plan, err := stage.Plan(ctx, nil, profile, nil)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if plan.Action != "install" {
		t.Errorf("expected plan action=install when no cluster, got %s", plan.Action)
	}

	// All components should be install.
	for _, comp := range plan.Components {
		if comp.Action != "install" {
			t.Errorf("component %s: expected action=install, got %s", comp.Name, comp.Action)
		}
	}

	// Verify distribution-specific bootstrap component.
	if len(plan.Components) < 2 {
		t.Fatal("expected at least 2 components")
	}
	if plan.Components[1].Name != "k3s-bootstrap" {
		t.Errorf("expected second component name=k3s-bootstrap, got %s", plan.Components[1].Name)
	}
}

func TestKubernetesStage_Status_Healthy(t *testing.T) {
	cs := fake.NewSimpleClientset(
		makeNode("node-1", true),
		makeNode("node-2", true),
		makeNode("node-3", true),
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
	if len(status.Components) != 1 {
		t.Fatalf("expected 1 component, got %d", len(status.Components))
	}

	comp := status.Components[0]
	if comp.Name != "kubernetes" {
		t.Errorf("expected component name=kubernetes, got %s", comp.Name)
	}
	if comp.Status != "running" {
		t.Errorf("expected component status=running, got %s", comp.Status)
	}
	if comp.Pods != 3 {
		t.Errorf("expected 3 total nodes, got %d", comp.Pods)
	}
	if comp.Ready != 3 {
		t.Errorf("expected 3 ready nodes, got %d", comp.Ready)
	}
	if status.Version == "" {
		t.Error("expected non-empty version")
	}
}

func TestKubernetesStage_Status_Degraded(t *testing.T) {
	cs := fake.NewSimpleClientset(
		makeNode("node-1", true),
		makeNode("node-2", false),
		makeNode("node-3", true),
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

	comp := status.Components[0]
	if comp.Ready != 2 {
		t.Errorf("expected 2 ready nodes, got %d", comp.Ready)
	}
	if comp.Pods != 3 {
		t.Errorf("expected 3 total nodes, got %d", comp.Pods)
	}
}

func TestKubernetesStage_Status_NoCluster(t *testing.T) {
	stage := New()
	ctx := context.Background()

	status, err := stage.Status(ctx, nil)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Status != "not-installed" {
		t.Errorf("expected status=not-installed, got %s", status.Status)
	}
}

func TestKubernetesStage_Validate_ClusterReachable(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	err := stage.Validate(ctx, kc, &config.Profile{Name: "test"})
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestKubernetesStage_Validate_NoClusterWithProfile(t *testing.T) {
	stage := New()
	ctx := context.Background()

	profile := &config.Profile{
		Name: "test",
		Kubernetes: config.ProfileKubernetes{
			Distribution: "k3s",
		},
	}

	// nil kube client, but profile provides bootstrap info.
	err := stage.Validate(ctx, nil, profile)
	if err != nil {
		t.Fatalf("Validate should pass with a profile: %v", err)
	}
}

func TestKubernetesStage_Validate_NoClusterNoProfile(t *testing.T) {
	stage := New()
	ctx := context.Background()

	err := stage.Validate(ctx, nil, nil)
	if err == nil {
		t.Fatal("Validate should fail with no cluster and no profile")
	}
}

func TestKubernetesStage_Validate_NoClusterEmptyDistribution(t *testing.T) {
	stage := New()
	ctx := context.Background()

	profile := &config.Profile{Name: "test"}
	err := stage.Validate(ctx, nil, profile)
	if err == nil {
		t.Fatal("Validate should fail with no cluster and empty distribution")
	}
}

func TestValidateExistingCluster(t *testing.T) {
	cs := fake.NewSimpleClientset(
		makeNode("node-1", true),
		makeNode("node-2", true),
	)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	ctx := context.Background()

	var buf bytes.Buffer
	printer := output.NewWithWriter(&buf, "text", false, false)

	err := validateExistingCluster(ctx, kc, printer)
	if err != nil {
		t.Fatalf("validateExistingCluster returned error: %v", err)
	}
}

func TestValidateExistingCluster_NilClient(t *testing.T) {
	ctx := context.Background()

	var buf bytes.Buffer
	printer := output.NewWithWriter(&buf, "text", false, false)

	err := validateExistingCluster(ctx, nil, printer)
	if err == nil {
		t.Fatal("expected error with nil client")
	}
}

func TestValidateManagedCluster(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	ctx := context.Background()

	var buf bytes.Buffer
	printer := output.NewWithWriter(&buf, "text", false, false)

	err := validateManagedCluster(ctx, kc, printer)
	if err != nil {
		t.Fatalf("validateManagedCluster returned error: %v", err)
	}
}

func TestValidateManagedCluster_NilClient(t *testing.T) {
	ctx := context.Background()

	var buf bytes.Buffer
	printer := output.NewWithWriter(&buf, "text", false, false)

	err := validateManagedCluster(ctx, nil, printer)
	if err == nil {
		t.Fatal("expected error with nil client")
	}
}

func TestKubernetesStage_Destroy_NoCluster(t *testing.T) {
	stage := New()
	ctx := context.Background()

	var buf bytes.Buffer
	printer := output.NewWithWriter(&buf, "text", false, false)

	err := stage.Destroy(ctx, nil, nil, printer)
	if err != nil {
		t.Fatalf("Destroy with nil client returned error: %v", err)
	}
}

func TestKubernetesStage_Destroy_NoNodes(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	var buf bytes.Buffer
	printer := output.NewWithWriter(&buf, "text", false, false)

	err := stage.Destroy(ctx, kc, nil, printer)
	if err != nil {
		t.Fatalf("Destroy with no nodes returned error: %v", err)
	}
}

func TestKubernetesStage_Destroy_CordonsNodes(t *testing.T) {
	cs := fake.NewSimpleClientset(
		makeNode("node-1", true),
		makeNode("node-2", true),
	)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	var buf bytes.Buffer
	printer := output.NewWithWriter(&buf, "text", false, false)

	err := stage.Destroy(ctx, kc, nil, printer)
	if err != nil {
		t.Fatalf("Destroy returned error: %v", err)
	}

	// Verify nodes were cordoned.
	nodes, _ := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	for _, node := range nodes.Items {
		if !node.Spec.Unschedulable {
			t.Errorf("node %s should be cordoned (unschedulable), but is not", node.Name)
		}
	}
}

func TestBootstrapCluster_NilProfile(t *testing.T) {
	ctx := context.Background()

	var buf bytes.Buffer
	printer := output.NewWithWriter(&buf, "text", false, false)

	err := bootstrapCluster(ctx, nil, nil, nil, printer)
	if err == nil {
		t.Fatal("expected error with nil profile")
	}
}

func TestBootstrapCluster_UnsupportedDistribution(t *testing.T) {
	ctx := context.Background()

	var buf bytes.Buffer
	printer := output.NewWithWriter(&buf, "text", false, false)

	profile := &config.Profile{
		Kubernetes: config.ProfileKubernetes{
			Distribution: "unsupported",
		},
	}

	err := bootstrapCluster(ctx, nil, nil, profile, printer)
	if err == nil {
		t.Fatal("expected error with unsupported distribution")
	}
}

func TestBootstrapCluster_UnsupportedWithKubeconfig(t *testing.T) {
	cs := fake.NewSimpleClientset(makeNode("node-1", true))
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	ctx := context.Background()

	var buf bytes.Buffer
	printer := output.NewWithWriter(&buf, "text", false, false)

	site := &config.Site{
		Kubeconfig: "/some/kubeconfig",
	}
	profile := &config.Profile{
		Kubernetes: config.ProfileKubernetes{
			Distribution: "custom",
		},
	}

	// Should fall through to validateExistingCluster.
	err := bootstrapCluster(ctx, kc, site, profile, printer)
	if err != nil {
		t.Fatalf("expected no error when kubeconfig provided with unknown distribution: %v", err)
	}
}

func TestBootstrapK3s_NoCluster(t *testing.T) {
	ctx := context.Background()

	var buf bytes.Buffer
	printer := output.NewWithWriter(&buf, "text", false, false)

	profile := &config.Profile{
		Kubernetes: config.ProfileKubernetes{
			Distribution: "k3s",
			MultiNode:    true,
		},
		Networking: config.ProfileNetworking{
			Overlay: "wireguard",
		},
	}

	err := bootstrapK3s(ctx, nil, profile, printer)
	if err != nil {
		t.Fatalf("bootstrapK3s returned error: %v", err)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("expected output with install instructions")
	}
}

func TestBootstrapKubeadm_NoCluster(t *testing.T) {
	ctx := context.Background()

	var buf bytes.Buffer
	printer := output.NewWithWriter(&buf, "text", false, false)

	profile := &config.Profile{
		Kubernetes: config.ProfileKubernetes{
			Distribution: "kubeadm",
		},
	}

	err := bootstrapKubeadm(ctx, nil, profile, printer)
	if err != nil {
		t.Fatalf("bootstrapKubeadm returned error: %v", err)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("expected output with install instructions")
	}
}

func TestBootstrapCluster_ManagedDistributions(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	ctx := context.Background()

	distributions := []string{"managed", "eks", "gke", "aks", "nebius"}
	for _, dist := range distributions {
		t.Run(dist, func(t *testing.T) {
			var buf bytes.Buffer
			printer := output.NewWithWriter(&buf, "text", false, false)

			profile := &config.Profile{
				Kubernetes: config.ProfileKubernetes{
					Distribution: dist,
				},
			}

			err := bootstrapCluster(ctx, kc, nil, profile, printer)
			if err != nil {
				t.Fatalf("bootstrapCluster(%s) returned error: %v", dist, err)
			}
		})
	}
}
