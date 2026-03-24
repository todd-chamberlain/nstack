package state

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestStore_SaveAndLoad(t *testing.T) {
	cs := fake.NewSimpleClientset()
	store := NewStore(cs)
	ctx := context.Background()

	// Ensure the namespace exists first.
	if err := store.EnsureNamespace(ctx); err != nil {
		t.Fatalf("EnsureNamespace: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	st := NewState("prod", "gpu-full")
	st.Stages[4] = &StageState{
		Status:  "deployed",
		Version: "1.2.0",
		Applied: now,
		Components: map[string]*ComponentState{
			"gpu-operator": {Version: "24.3.0", Status: "running"},
		},
	}

	if err := store.Save(ctx, st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Site != "prod" {
		t.Errorf("expected site=prod, got %s", loaded.Site)
	}
	if loaded.Profile != "gpu-full" {
		t.Errorf("expected profile=gpu-full, got %s", loaded.Profile)
	}

	stage4, ok := loaded.Stages[4]
	if !ok {
		t.Fatal("stage 4 not found in loaded state")
	}
	if stage4.Status != "deployed" {
		t.Errorf("expected status=deployed, got %s", stage4.Status)
	}
	if stage4.Version != "1.2.0" {
		t.Errorf("expected version=1.2.0, got %s", stage4.Version)
	}
	if !stage4.Applied.Equal(now) {
		t.Errorf("expected applied=%v, got %v", now, stage4.Applied)
	}

	comp, ok := stage4.Components["gpu-operator"]
	if !ok {
		t.Fatal("component gpu-operator not found")
	}
	if comp.Version != "24.3.0" {
		t.Errorf("expected component version=24.3.0, got %s", comp.Version)
	}
	if comp.Status != "running" {
		t.Errorf("expected component status=running, got %s", comp.Status)
	}
}

func TestStore_UpdateStage(t *testing.T) {
	cs := fake.NewSimpleClientset()
	store := NewStore(cs)
	ctx := context.Background()

	if err := store.EnsureNamespace(ctx); err != nil {
		t.Fatalf("EnsureNamespace: %v", err)
	}

	st := NewState("dev", "minimal")
	st.Stages[4] = &StageState{
		Status:  "deployed",
		Version: "1.0.0",
		Applied: time.Now().Truncate(time.Second),
	}
	if err := store.Save(ctx, st); err != nil {
		t.Fatalf("first Save: %v", err)
	}

	// Add stage 5 and save again.
	st.Stages[5] = &StageState{
		Status:  "deployed",
		Version: "2.0.0",
		Applied: time.Now().Truncate(time.Second),
	}
	if err := store.Save(ctx, st); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loaded.Stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(loaded.Stages))
	}
	if _, ok := loaded.Stages[4]; !ok {
		t.Error("stage 4 missing after update")
	}
	if _, ok := loaded.Stages[5]; !ok {
		t.Error("stage 5 missing after update")
	}
}

func TestStore_EmptyState(t *testing.T) {
	cs := fake.NewSimpleClientset()
	store := NewStore(cs)
	ctx := context.Background()

	// Load without any ConfigMap existing — should return empty state.
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Stages == nil {
		t.Fatal("expected non-nil Stages map, got nil")
	}
	if len(loaded.Stages) != 0 {
		t.Errorf("expected 0 stages, got %d", len(loaded.Stages))
	}
}

func TestStore_EnsureNamespace(t *testing.T) {
	cs := fake.NewSimpleClientset()
	store := NewStore(cs)
	ctx := context.Background()

	if err := store.EnsureNamespace(ctx); err != nil {
		t.Fatalf("EnsureNamespace: %v", err)
	}

	ns, err := cs.CoreV1().Namespaces().Get(ctx, Namespace, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("namespace not found: %v", err)
	}
	if ns.Name != Namespace {
		t.Errorf("expected namespace=%s, got %s", Namespace, ns.Name)
	}

	// Calling again should not error (idempotent).
	if err := store.EnsureNamespace(ctx); err != nil {
		t.Fatalf("second EnsureNamespace: %v", err)
	}
}
