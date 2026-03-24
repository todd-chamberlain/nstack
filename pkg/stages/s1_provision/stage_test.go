package s1_provision

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/kube"
)

func int32Ptr(i int32) *int32 { return &i }

// makeMetal3Deployment builds a minimal Deployment for the baremetal-operator.
func makeMetal3Deployment(available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      metal3Release,
			Namespace: metal3Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "baremetal-operator"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "baremetal-operator"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "baremetal-operator",
						Image: "quay.io/metal3-io/baremetal-operator:v0.8.0",
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

func TestProvisionStage_Metadata(t *testing.T) {
	stage := New()
	if stage.Number() != 1 {
		t.Errorf("expected Number()=1, got %d", stage.Number())
	}
	if stage.Name() != "Provisioning" {
		t.Errorf("expected Name()=Provisioning, got %s", stage.Name())
	}
	deps := stage.Dependencies()
	if len(deps) != 1 || deps[0] != 0 {
		t.Errorf("expected Dependencies()=[0], got %v", deps)
	}
}

func TestFormatBMCAddress_Redfish(t *testing.T) {
	bmc := &config.BMCConfig{
		IP:       "192.168.1.10",
		Protocol: "redfish",
	}
	got := formatBMCAddress(bmc)
	want := "redfish://192.168.1.10/redfish/v1/Systems/1"
	if got != want {
		t.Errorf("formatBMCAddress(redfish) = %q, want %q", got, want)
	}
}

func TestFormatBMCAddress_IPMI(t *testing.T) {
	bmc := &config.BMCConfig{
		IP:       "10.0.0.5",
		Protocol: "ipmi",
	}
	got := formatBMCAddress(bmc)
	want := "ipmi://10.0.0.5"
	if got != want {
		t.Errorf("formatBMCAddress(ipmi) = %q, want %q", got, want)
	}
}

func TestFormatBMCAddress_Default(t *testing.T) {
	bmc := &config.BMCConfig{
		IP:       "10.0.0.5",
		Protocol: "unknown-proto",
	}
	got := formatBMCAddress(bmc)
	want := "ipmi://10.0.0.5"
	if got != want {
		t.Errorf("formatBMCAddress(unknown) = %q, want %q", got, want)
	}
}

func TestFormatBMCAddress_Nil(t *testing.T) {
	got := formatBMCAddress(nil)
	if got != "" {
		t.Errorf("formatBMCAddress(nil) = %q, want empty string", got)
	}
}

func TestResolveCredentials_EnvVar(t *testing.T) {
	t.Setenv("TEST_BMC_CREDS", "admin:pass123")
	user, pass := resolveCredentials("env://TEST_BMC_CREDS")
	if user != "admin" || pass != "pass123" {
		t.Errorf("resolveCredentials(env://) = (%q, %q), want (admin, pass123)", user, pass)
	}
}

func TestResolveCredentials_Plain(t *testing.T) {
	user, pass := resolveCredentials("root:password")
	if user != "root" || pass != "password" {
		t.Errorf("resolveCredentials(plain) = (%q, %q), want (root, password)", user, pass)
	}
}

func TestResolveCredentials_File(t *testing.T) {
	dir := t.TempDir()
	credFile := filepath.Join(dir, "creds")
	if err := os.WriteFile(credFile, []byte("user:secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	user, pass := resolveCredentials("file://" + credFile)
	if user != "user" || pass != "secret" {
		t.Errorf("resolveCredentials(file://) = (%q, %q), want (user, secret)", user, pass)
	}
}

func TestResolveCredentials_SingleValue(t *testing.T) {
	user, pass := resolveCredentials("mypassword")
	if user != "admin" || pass != "mypassword" {
		t.Errorf("resolveCredentials(single) = (%q, %q), want (admin, mypassword)", user, pass)
	}
}

func TestDetect_NotInstalled(t *testing.T) {
	cs := fake.NewSimpleClientset()
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
	if op.Name != "baremetal-operator" {
		t.Errorf("expected operator name=baremetal-operator, got %s", op.Name)
	}
	if op.Status != "not-installed" {
		t.Errorf("expected status=not-installed, got %s", op.Status)
	}
}

func TestDetect_Installed(t *testing.T) {
	cs := fake.NewSimpleClientset(makeMetal3Deployment(1))
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
	if op.Status != "running" {
		t.Errorf("expected status=running, got %s", op.Status)
	}
	if op.Version == "" {
		t.Error("version should not be empty")
	}
}

func TestValidate(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	stage := New()
	ctx := context.Background()

	err := stage.Validate(ctx, kc, &config.Profile{Name: "test"})
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestPlan_OperatorNotInstalled(t *testing.T) {
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

	opComp := plan.Components[0]
	if opComp.Name != "baremetal-operator" {
		t.Errorf("expected first component name=baremetal-operator, got %s", opComp.Name)
	}
	if opComp.Action != "install" {
		t.Errorf("baremetal-operator: expected action=install, got %s", opComp.Action)
	}

	bmhComp := plan.Components[1]
	if bmhComp.Name != "baremetalhosts" {
		t.Errorf("expected second component name=baremetalhosts, got %s", bmhComp.Name)
	}
	if bmhComp.Action != "install" {
		t.Errorf("baremetalhosts: expected action=install, got %s", bmhComp.Action)
	}

	if plan.Action != "install" {
		t.Errorf("expected plan action=install, got %s", plan.Action)
	}
}

func TestPlan_OperatorAlreadyInstalled(t *testing.T) {
	cs := fake.NewSimpleClientset(makeMetal3Deployment(1))
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

	opComp := plan.Components[0]
	if opComp.Action != "skip" {
		t.Errorf("baremetal-operator: expected action=skip, got %s", opComp.Action)
	}
	if opComp.Current == "" {
		t.Error("baremetal-operator: current version should not be empty when already installed")
	}

	// BMH is still an install candidate (Apply decides per-node).
	bmhComp := plan.Components[1]
	if bmhComp.Action != "install" {
		t.Errorf("baremetalhosts: expected action=install, got %s", bmhComp.Action)
	}
}

func TestStatus_Running(t *testing.T) {
	cs := fake.NewSimpleClientset(makeMetal3Deployment(1))
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
	if status.Components[0].Status != "running" {
		t.Errorf("expected component status=running, got %s", status.Components[0].Status)
	}
}

func TestStatus_NotInstalled(t *testing.T) {
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

func TestNodesWithBMC(t *testing.T) {
	nodes := []config.Node{
		{Name: "worker-1", BMC: &config.BMCConfig{IP: "10.0.0.1", Protocol: "ipmi"}},
		{Name: "worker-2"},
		{Name: "worker-3", BMC: &config.BMCConfig{IP: "10.0.0.3", Protocol: "redfish"}},
		{Name: "worker-4", BMC: &config.BMCConfig{}},
	}
	result := nodesWithBMC(nodes)
	if len(result) != 2 {
		t.Fatalf("expected 2 nodes with BMC, got %d", len(result))
	}
	if result[0].Name != "worker-1" {
		t.Errorf("expected first node=worker-1, got %s", result[0].Name)
	}
	if result[1].Name != "worker-3" {
		t.Errorf("expected second node=worker-3, got %s", result[1].Name)
	}
}
