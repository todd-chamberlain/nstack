package engine

import (
	"context"
	"testing"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
	"github.com/todd-chamberlain/nstack/pkg/state"
)

// mockStage is a minimal Stage implementation for registry tests.
type mockStage struct {
	num  int
	name string
	deps []int
}

func (m *mockStage) Number() int            { return m.num }
func (m *mockStage) Name() string            { return m.name }
func (m *mockStage) Dependencies() []int     { return m.deps }

func (m *mockStage) Detect(_ context.Context, _ *kube.Client) (*DetectResult, error) {
	return nil, nil
}
func (m *mockStage) Validate(_ context.Context, _ *kube.Client, _ *config.Profile) error {
	return nil
}
func (m *mockStage) Plan(_ context.Context, _ *kube.Client, _ *config.Profile, _ *state.StageState) (*StagePlan, error) {
	return nil, nil
}
func (m *mockStage) Apply(_ context.Context, _ *kube.Client, _ *helm.Client, _ *config.Site, _ *config.Profile, _ *StagePlan, _ *output.Printer) error {
	return nil
}
func (m *mockStage) Status(_ context.Context, _ *kube.Client) (*StageStatus, error) {
	return nil, nil
}
func (m *mockStage) Destroy(_ context.Context, _ *kube.Client, _ *helm.Client, _ *output.Printer) error {
	return nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	s := &mockStage{num: 4, name: "gpu-operator"}
	r.Register(s)

	got, ok := r.Get(4)
	if !ok {
		t.Fatal("expected to find stage 4")
	}
	if got.Number() != 4 {
		t.Errorf("expected Number()=4, got %d", got.Number())
	}
	if got.Name() != "gpu-operator" {
		t.Errorf("expected Name()=gpu-operator, got %s", got.Name())
	}
}

func TestRegistry_All_Sorted(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockStage{num: 6, name: "monitoring"})
	r.Register(&mockStage{num: 4, name: "gpu-operator"})
	r.Register(&mockStage{num: 5, name: "slurm"})

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(all))
	}
	expected := []int{4, 5, 6}
	for i, s := range all {
		if s.Number() != expected[i] {
			t.Errorf("all[%d]: expected Number()=%d, got %d", i, expected[i], s.Number())
		}
	}
}

func TestRegistry_Resolve_Default(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockStage{num: 4, name: "gpu-operator"})
	r.Register(&mockStage{num: 5, name: "slurm", deps: []int{4}})
	r.Register(&mockStage{num: 6, name: "monitoring", deps: []int{4, 5}})

	stages, err := r.Resolve(ResolveOpts{}, &state.State{Stages: make(map[int]*state.StageState)})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(stages))
	}
	for i, expected := range []int{4, 5, 6} {
		if stages[i].Number() != expected {
			t.Errorf("stages[%d]: expected %d, got %d", i, expected, stages[i].Number())
		}
	}
}

func TestRegistry_Resolve_From(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockStage{num: 4, name: "gpu-operator"})
	r.Register(&mockStage{num: 5, name: "slurm", deps: []int{4}})
	r.Register(&mockStage{num: 6, name: "monitoring", deps: []int{4, 5}})

	st := &state.State{
		Stages: map[int]*state.StageState{
			4: {Status: "deployed"},
		},
	}

	stages, err := r.Resolve(ResolveOpts{From: 5}, st)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(stages))
	}
	if stages[0].Number() != 5 {
		t.Errorf("expected first stage=5, got %d", stages[0].Number())
	}
	if stages[1].Number() != 6 {
		t.Errorf("expected second stage=6, got %d", stages[1].Number())
	}
}

func TestRegistry_Resolve_Only(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockStage{num: 4, name: "gpu-operator"})
	r.Register(&mockStage{num: 5, name: "slurm", deps: []int{4}})

	st := &state.State{
		Stages: map[int]*state.StageState{
			4: {Status: "deployed"},
		},
	}

	stages, err := r.Resolve(ResolveOpts{Only: 5}, st)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(stages))
	}
	if stages[0].Number() != 5 {
		t.Errorf("expected stage 5, got %d", stages[0].Number())
	}
}

func TestRegistry_Resolve_Only_MissingDep(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockStage{num: 4, name: "gpu-operator"})
	r.Register(&mockStage{num: 5, name: "slurm", deps: []int{4}})

	st := &state.State{
		Stages: make(map[int]*state.StageState),
	}

	_, err := r.Resolve(ResolveOpts{Only: 5}, st)
	if err == nil {
		t.Fatal("expected error for missing dependency, got nil")
	}
}

func TestRegistry_Resolve_Stages(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockStage{num: 4, name: "gpu-operator"})
	r.Register(&mockStage{num: 5, name: "slurm", deps: []int{4}})
	r.Register(&mockStage{num: 6, name: "monitoring", deps: []int{4, 5}})

	st := &state.State{
		Stages: map[int]*state.StageState{
			5: {Status: "deployed"},
		},
	}

	stages, err := r.Resolve(ResolveOpts{Stages: []int{4, 6}}, st)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(stages))
	}
	if stages[0].Number() != 4 {
		t.Errorf("expected first stage=4, got %d", stages[0].Number())
	}
	if stages[1].Number() != 6 {
		t.Errorf("expected second stage=6, got %d", stages[1].Number())
	}
}
