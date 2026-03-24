package s3_networking

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

// makeNetworkOperatorDeployment builds a minimal Deployment for the network-operator.
func makeNetworkOperatorDeployment(available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      networkOperatorRelease,
			Namespace: networkOperatorNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "network-operator"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "network-operator"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "network-operator",
						Image: "nvcr.io/nvidia/cloud-native/network-operator:v25.7.0",
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

// makeDOCADeployment builds a minimal Deployment for the DOCA platform operator.
func makeDOCADeployment(available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      docaRelease,
			Namespace: docaNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "doca-platform"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "doca-platform"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "doca-platform",
						Image: "nvcr.io/nvidia/doca:2.9.1",
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

func TestNetworkingStage_Metadata(t *testing.T) {
	stage := New()
	if stage.Number() != 3 {
		t.Errorf("expected Number()=3, got %d", stage.Number())
	}
	if stage.Name() != "Networking" {
		t.Errorf("expected Name()=Networking, got %s", stage.Name())
	}
	if len(stage.Dependencies()) != 0 {
		t.Errorf("expected no dependencies, got %v", stage.Dependencies())
	}
}

func TestNetworkingStage_Detect_Found(t *testing.T) {
	cs := fake.NewSimpleClientset(
		makeNetworkOperatorDeployment(1),
		makeDOCADeployment(1),
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

	no := result.Operators[0]
	if no.Name != "network-operator" {
		t.Errorf("expected first operator name=network-operator, got %s", no.Name)
	}
	if no.Version == "" {
		t.Error("network-operator version should not be empty")
	}

	doca := result.Operators[1]
	if doca.Name != "doca-platform" {
		t.Errorf("expected second operator name=doca-platform, got %s", doca.Name)
	}
	if doca.Version == "" {
		t.Error("doca-platform version should not be empty")
	}
}

func TestNetworkingStage_Detect_NotFound(t *testing.T) {
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

func TestNetworkingStage_Plan_WithInfiniBand(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	profile := &config.Profile{
		Name: "test",
		Networking: config.ProfileNetworking{
			Fabric: "infiniband",
		},
	}

	plan, err := stage.Plan(ctx, kc, profile, nil)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(plan.Components) != 2 {
		t.Fatalf("expected 2 components, got %d", len(plan.Components))
	}

	noComp := plan.Components[0]
	if noComp.Name != "network-operator" {
		t.Errorf("expected first component name=network-operator, got %s", noComp.Name)
	}
	if noComp.Action != "install" {
		t.Errorf("network-operator: expected action=install, got %s", noComp.Action)
	}

	if plan.Action != "install" {
		t.Errorf("expected plan action=install, got %s", plan.Action)
	}
}

func TestNetworkingStage_Plan_NoFabric(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	profile := &config.Profile{
		Name: "test",
		Networking: config.ProfileNetworking{
			Fabric: "none",
		},
	}

	plan, err := stage.Plan(ctx, kc, profile, nil)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	noComp := plan.Components[0]
	if noComp.Name != "network-operator" {
		t.Errorf("expected first component name=network-operator, got %s", noComp.Name)
	}
	if noComp.Action != "skip" {
		t.Errorf("network-operator: expected action=skip, got %s", noComp.Action)
	}
}

func TestNetworkingStage_Plan_WithDPU(t *testing.T) {
	// When DOCA is not deployed and DPU check happens at Plan level,
	// the Plan marks DOCA as install candidate. Apply() makes the final decision.
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	profile := &config.Profile{
		Name: "test",
		Networking: config.ProfileNetworking{
			Fabric: "infiniband",
		},
	}

	plan, err := stage.Plan(ctx, kc, profile, nil)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	if len(plan.Components) != 2 {
		t.Fatalf("expected 2 components, got %d", len(plan.Components))
	}

	docaComp := plan.Components[1]
	if docaComp.Name != "doca-platform" {
		t.Errorf("expected second component name=doca-platform, got %s", docaComp.Name)
	}
	// DOCA is an install candidate; Apply() checks for DPU nodes.
	if docaComp.Action != "install" {
		t.Errorf("doca-platform: expected action=install, got %s", docaComp.Action)
	}
}

func TestNetworkingStage_Validate(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	err := stage.Validate(ctx, kc, &config.Profile{Name: "test"})
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestNetworkingStage_Status_Running(t *testing.T) {
	cs := fake.NewSimpleClientset(
		makeNetworkOperatorDeployment(1),
		makeDOCADeployment(1),
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

func TestNetworkingStage_Status_NotInstalled(t *testing.T) {
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

func TestHasFabric(t *testing.T) {
	tests := []struct {
		name    string
		site    *config.Site
		profile *config.Profile
		want    bool
	}{
		{
			name: "infiniband from profile",
			profile: &config.Profile{
				Networking: config.ProfileNetworking{Fabric: "infiniband"},
			},
			want: true,
		},
		{
			name: "roce from site",
			site: &config.Site{
				Fabric: &config.FabricConfig{Type: "roce"},
			},
			profile: &config.Profile{},
			want:    true,
		},
		{
			name:    "none from profile",
			profile: &config.Profile{Networking: config.ProfileNetworking{Fabric: "none"}},
			want:    false,
		},
		{
			name:    "ethernet from profile",
			profile: &config.Profile{Networking: config.ProfileNetworking{Fabric: "ethernet"}},
			want:    false,
		},
		{
			name:    "empty fabric",
			profile: &config.Profile{},
			want:    false,
		},
		{
			name: "site overrides profile",
			site: &config.Site{
				Fabric: &config.FabricConfig{Type: "infiniband"},
			},
			profile: &config.Profile{Networking: config.ProfileNetworking{Fabric: "none"}},
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasFabric(tt.site, tt.profile)
			if got != tt.want {
				t.Errorf("hasFabric() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasDPUs(t *testing.T) {
	tests := []struct {
		name string
		site *config.Site
		want bool
	}{
		{
			name: "nil site",
			site: nil,
			want: false,
		},
		{
			name: "no nodes",
			site: &config.Site{},
			want: false,
		},
		{
			name: "nodes without DPUs",
			site: &config.Site{
				Nodes: []config.Node{
					{Name: "worker-1", GPUs: []config.GPU{{Model: "A100", Count: 8}}},
				},
			},
			want: false,
		},
		{
			name: "nodes with DPUs",
			site: &config.Site{
				Nodes: []config.Node{
					{Name: "worker-1", DPUs: []config.DPU{{Model: "BlueField-3", Count: 1}}},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasDPUs(tt.site)
			if got != tt.want {
				t.Errorf("hasDPUs() = %v, want %v", got, tt.want)
			}
		})
	}
}
