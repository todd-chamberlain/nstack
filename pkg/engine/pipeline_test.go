package engine

import (
	"context"
	"errors"
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
	calls     []string
}

func (r *recordingStage) Number() int        { return r.num }
func (r *recordingStage) Name() string        { return r.name }
func (r *recordingStage) Dependencies() []int { return r.deps }

func (r *recordingStage) Detect(_ context.Context, _ *kube.Client) (*DetectResult, error) {
	r.calls = append(r.calls, "Detect")
	return nil, nil
}

func (r *recordingStage) Validate(_ context.Context, _ *kube.Client, _ *config.Profile) error {
	r.calls = append(r.calls, "Validate")
	return nil
}

func (r *recordingStage) Plan(_ context.Context, _ *kube.Client, _ *config.Profile, _ *state.StageState) (*StagePlan, error) {
	r.calls = append(r.calls, "Plan")
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
	r.calls = append(r.calls, "Apply")
	return r.applyErr
}

func (r *recordingStage) Status(_ context.Context, _ *kube.Client) (*StageStatus, error) {
	r.calls = append(r.calls, "Status")
	return nil, nil
}

func (r *recordingStage) Destroy(_ context.Context, _ *kube.Client, _ *helm.Client, _ *output.Printer) error {
	r.calls = append(r.calls, "Destroy")
	return nil
}

func (r *recordingStage) called(method string) bool {
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
	store := state.NewStore(cs)
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
