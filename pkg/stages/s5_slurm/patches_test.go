package s5_slurm

import (
	"context"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

func TestApplyK3sPatches_NilProfile(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, false)
	ctx := context.Background()
	cluster := config.ClusterConfig{Name: "slurm1", Namespace: "slurm"}

	err := applyK3sPatches(ctx, kc, nil, cluster, printer)
	if err != nil {
		t.Fatalf("applyK3sPatches with nil profile should not error: %v", err)
	}
}

func TestApplyK3sPatches_MinimalProfile(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, false)
	ctx := context.Background()
	cluster := config.ClusterConfig{Name: "slurm1", Namespace: "slurm"}

	profile := &config.Profile{
		Name: "k3s-single",
		Patches: config.ProfilePatches{
			ContainerdSocketBind: false, // Can't test bind-mount in unit tests
		},
	}

	err := applyK3sPatches(ctx, kc, profile, cluster, printer)
	if err != nil {
		t.Fatalf("applyK3sPatches: %v", err)
	}
}
