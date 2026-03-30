package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
	"github.com/todd-chamberlain/nstack/pkg/state"
	"k8s.io/client-go/kubernetes/fake"
)

// recordingStage is a mock stage that records method calls and can be
// configured to return specific plans or errors from Apply.
type recordingStage struct {
	num       int
	name      string
	deps      []int
	plan      *StagePlan
	applyErr  error
	mu        sync.Mutex
	calls     []string
}

func (r *recordingStage) Number() int        { return r.num }
func (r *recordingStage) Name() string        { return r.name }
func (r *recordingStage) Dependencies() []int { return r.deps }

func (r *recordingStage) Detect(_ context.Context, _ *kube.Client) (*DetectResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, "Detect")
	r.mu.Unlock()
	return nil, nil
}

func (r *recordingStage) Validate(_ context.Context, _ *kube.Client, _ *config.Profile) error {
	r.mu.Lock()
	r.calls = append(r.calls, "Validate")
	r.mu.Unlock()
	return nil
}

func (r *recordingStage) Plan(_ context.Context, _ *kube.Client, _ *config.Profile, _ *state.StageState) (*StagePlan, error) {
	r.mu.Lock()
	r.calls = append(r.calls, "Plan")
	r.mu.Unlock()
	if r.plan != nil {
		return r.plan, nil
	}
	return &StagePlan{
		Stage:  r.num,
		Name:   r.name,
		Action: "install",
	}, nil
}

func (r *recordingStage) Apply(_ context.Context, _ *kube.Client, _ *helm.Client, _ *config.Site, _ *config.Profile, _ *StagePlan, _ *output.Printer) error {
	r.mu.Lock()
	r.calls = append(r.calls, "Apply")
	r.mu.Unlock()
	return r.applyErr
}

func (r *recordingStage) Status(_ context.Context, _ *kube.Client) (*StageStatus, error) {
	r.mu.Lock()
	r.calls = append(r.calls, "Status")
	r.mu.Unlock()
	return nil, nil
}

func (r *recordingStage) Destroy(_ context.Context, _ *kube.Client, _ *helm.Client, _ *output.Printer) error {
	r.mu.Lock()
	r.calls = append(r.calls, "Destroy")
	r.mu.Unlock()
	return nil
}

func (r *recordingStage) called(method string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if c == method {
			return true
		}
	}
	return false
}

// newTestPipeline sets up a Pipeline with a fake clientset and the given stages.
func newTestPipeline(t *testing.T, stages ...Stage) (*Pipeline, *Registry) {
	t.Helper()
	cs := fake.NewSimpleClientset()
	store := state.NewStore(cs, "")
	ctx := context.Background()
	if err := store.EnsureNamespace(ctx); err != nil {
		t.Fatalf("EnsureNamespace: %v", err)
	}
	printer := output.New("text", true, false)
	reg := NewRegistry()
	for _, s := range stages {
		reg.Register(s)
	}
	return NewPipeline(reg, store, printer), reg
}

func TestPipeline_RunAll(t *testing.T) {
	s4 := &recordingStage{num: 4, name: "gpu-operator"}
	s5 := &recordingStage{num: 5, name: "slurm", deps: []int{4}}
	s6 := &recordingStage{num: 6, name: "monitoring", deps: []int{4, 5}}

	p, _ := newTestPipeline(t, s4, s5, s6)

	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)

	err := p.Run(context.Background(), RunOpts{
		KubeClient: kc,
		Profile:    &config.Profile{Name: "test"},
		Site:       &config.Site{Name: "test"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, s := range []*recordingStage{s4, s5, s6} {
		if !s.called("Apply") {
			t.Errorf("stage %d (%s): Apply not called", s.num, s.name)
		}
	}
}

func TestPipeline_SkipDeployed(t *testing.T) {
	s4 := &recordingStage{num: 4, name: "gpu-operator"}
	s5 := &recordingStage{num: 5, name: "slurm", deps: []int{4}}

	p, _ := newTestPipeline(t, s4, s5)

	// Pre-populate state with stage 4 deployed.
	ctx := context.Background()
	st, _ := p.store.Load(ctx)
	st.Stages[4] = &state.StageState{
		Status:  "deployed",
		Version: "1.0.0",
		Applied: time.Now(),
	}
	if err := p.store.Save(ctx, st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)

	err := p.Run(ctx, RunOpts{
		KubeClient: kc,
		Profile:    &config.Profile{Name: "test"},
		Site:       &config.Site{Name: "test"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if s4.called("Apply") {
		t.Error("stage 4: Apply should NOT have been called (already deployed)")
	}
	if !s5.called("Apply") {
		t.Error("stage 5: Apply should have been called")
	}
}

func TestPipeline_Force(t *testing.T) {
	s4 := &recordingStage{num: 4, name: "gpu-operator"}

	p, _ := newTestPipeline(t, s4)

	// Pre-populate state with stage 4 deployed.
	ctx := context.Background()
	st, _ := p.store.Load(ctx)
	st.Stages[4] = &state.StageState{
		Status:  "deployed",
		Version: "1.0.0",
		Applied: time.Now(),
	}
	if err := p.store.Save(ctx, st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)

	err := p.Run(ctx, RunOpts{
		KubeClient: kc,
		Profile:    &config.Profile{Name: "test"},
		Site:       &config.Site{Name: "test"},
		Force:      true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !s4.called("Apply") {
		t.Error("stage 4: Apply should have been called (Force=true)")
	}
}

func TestPipeline_StopOnFailure(t *testing.T) {
	s4 := &recordingStage{num: 4, name: "gpu-operator"}
	s5 := &recordingStage{num: 5, name: "slurm", deps: []int{4}, applyErr: errors.New("install failed")}
	s6 := &recordingStage{num: 6, name: "monitoring", deps: []int{4, 5}}

	p, _ := newTestPipeline(t, s4, s5, s6)

	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)

	err := p.Run(context.Background(), RunOpts{
		KubeClient: kc,
		Profile:    &config.Profile{Name: "test"},
		Site:       &config.Site{Name: "test"},
	})
	if err == nil {
		t.Fatal("expected error from Run, got nil")
	}

	if !s5.called("Apply") {
		t.Error("stage 5: Apply should have been called")
	}
	if s6.called("Apply") {
		t.Error("stage 6: Apply should NOT have been called (pipeline stopped)")
	}

	// Verify state shows stage 5 as failed.
	ctx := context.Background()
	st, loadErr := p.store.Load(ctx)
	if loadErr != nil {
		t.Fatalf("Load: %v", loadErr)
	}
	stage5, ok := st.Stages[5]
	if !ok {
		t.Fatal("stage 5 not found in state")
	}
	if stage5.Status != "failed" {
		t.Errorf("expected stage 5 status=failed, got %s", stage5.Status)
	}
	if stage5.Error == "" {
		t.Error("expected stage 5 to have an error message")
	}
}

func TestAssignLevels(t *testing.T) {
	tests := []struct {
		name   string
		stages []Stage
		state  *state.State
		want   map[int]int
	}{
		{
			name: "no deps all level 0",
			stages: []Stage{
				&recordingStage{num: 1, name: "a"},
				&recordingStage{num: 2, name: "b"},
				&recordingStage{num: 3, name: "c"},
			},
			state: &state.State{Stages: map[int]*state.StageState{}},
			want:  map[int]int{1: 0, 2: 0, 3: 0},
		},
		{
			name: "linear chain",
			stages: []Stage{
				&recordingStage{num: 1, name: "a"},
				&recordingStage{num: 2, name: "b", deps: []int{1}},
				&recordingStage{num: 3, name: "c", deps: []int{2}},
			},
			state: &state.State{Stages: map[int]*state.StageState{}},
			want:  map[int]int{1: 0, 2: 1, 3: 2},
		},
		{
			name: "diamond dependency",
			stages: []Stage{
				&recordingStage{num: 1, name: "a"},
				&recordingStage{num: 2, name: "b", deps: []int{1}},
				&recordingStage{num: 3, name: "c", deps: []int{1}},
				&recordingStage{num: 4, name: "d", deps: []int{2, 3}},
			},
			state: &state.State{Stages: map[int]*state.StageState{}},
			want:  map[int]int{1: 0, 2: 1, 3: 1, 4: 2},
		},
		{
			name: "dep not in set treated as satisfied",
			stages: []Stage{
				&recordingStage{num: 5, name: "b", deps: []int{4}},
				&recordingStage{num: 6, name: "c", deps: []int{5}},
			},
			state: &state.State{Stages: map[int]*state.StageState{
				4: {Status: "deployed"},
			}},
			want: map[int]int{5: 0, 6: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := assignLevels(tt.stages, tt.state)
			if len(got) != len(tt.want) {
				t.Fatalf("len(levels) = %d, want %d", len(got), len(tt.want))
			}
			for num, wantLvl := range tt.want {
				if gotLvl, ok := got[num]; !ok {
					t.Errorf("stage %d: not in levels map", num)
				} else if gotLvl != wantLvl {
					t.Errorf("stage %d: level = %d, want %d", num, gotLvl, wantLvl)
				}
			}
		})
	}
}

func TestPipeline_Parallel(t *testing.T) {
	// Build a diamond: s4 (no deps), s5 and s6 depend on s4, s7 depends on s5 and s6.
	s4 := &recordingStage{num: 4, name: "base"}
	s5 := &recordingStage{num: 5, name: "left", deps: []int{4}}
	s6 := &recordingStage{num: 6, name: "right", deps: []int{4}}
	s7 := &recordingStage{num: 7, name: "top", deps: []int{5, 6}}

	p, _ := newTestPipeline(t, s4, s5, s6, s7)

	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)

	err := p.Run(context.Background(), RunOpts{
		KubeClient: kc,
		Profile:    &config.Profile{Name: "test"},
		Site:       &config.Site{Name: "test"},
		Parallel:   true,
	})
	if err != nil {
		t.Fatalf("Run (parallel): %v", err)
	}

	for _, s := range []*recordingStage{s4, s5, s6, s7} {
		if !s.called("Apply") {
			t.Errorf("stage %d (%s): Apply not called", s.num, s.name)
		}
	}

	// Verify all stages recorded as deployed in state.
	ctx := context.Background()
	st, loadErr := p.store.Load(ctx)
	if loadErr != nil {
		t.Fatalf("Load: %v", loadErr)
	}
	for _, num := range []int{4, 5, 6, 7} {
		ss, ok := st.Stages[num]
		if !ok {
			t.Errorf("stage %d: not found in state", num)
		} else if ss.Status != "deployed" {
			t.Errorf("stage %d: status = %q, want deployed", num, ss.Status)
		}
	}
}

func TestPipeline_ParallelStopOnFailure(t *testing.T) {
	// s4 (no deps), s5 depends on s4 and fails, s6 depends on s4.
	// s7 depends on s5 and s6 — should not run because s5 fails.
	s4 := &recordingStage{num: 4, name: "base"}
	s5 := &recordingStage{num: 5, name: "fail", deps: []int{4}, applyErr: errors.New("boom")}
	s6 := &recordingStage{num: 6, name: "ok", deps: []int{4}}
	s7 := &recordingStage{num: 7, name: "top", deps: []int{5, 6}}

	p, _ := newTestPipeline(t, s4, s5, s6, s7)

	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)

	err := p.Run(context.Background(), RunOpts{
		KubeClient: kc,
		Profile:    &config.Profile{Name: "test"},
		Site:       &config.Site{Name: "test"},
		Parallel:   true,
	})
	if err == nil {
		t.Fatal("expected error from parallel Run, got nil")
	}

	// Stage 7 should NOT have been applied since level 1 had a failure.
	if s7.called("Apply") {
		t.Error("stage 7: Apply should NOT have been called (earlier level failed)")
	}
}
