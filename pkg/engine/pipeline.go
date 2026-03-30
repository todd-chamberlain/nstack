package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
	"github.com/todd-chamberlain/nstack/pkg/state"
)

// Pipeline orchestrates the execution of stages in dependency order.
type Pipeline struct {
	registry *Registry
	store    *state.Store
	printer  *output.Printer
}

// NewPipeline creates a Pipeline wired to the given registry, state store, and printer.
func NewPipeline(r *Registry, s *state.Store, p *output.Printer) *Pipeline {
	return &Pipeline{
		registry: r,
		store:    s,
		printer:  p,
	}
}

// RunOpts configures a single pipeline execution.
type RunOpts struct {
	ResolveOpts
	Force      bool
	DryRun     bool
	Parallel   bool
	KubeClient *kube.Client
	HelmClient *helm.Client
	Site       *config.Site
	Profile    *config.Profile
}

// Run executes the pipeline: resolves stages, then validates, plans, and applies
// each one in order. State is persisted after every stage.
func (p *Pipeline) Run(ctx context.Context, opts RunOpts) error {
	// Ensure the nstack-system namespace exists for state storage.
	if err := p.store.EnsureNamespace(ctx); err != nil {
		return fmt.Errorf("ensuring namespace: %w", err)
	}

	// Load current state.
	currentState, err := p.store.Load(ctx)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	// Populate top-level site and profile identifiers for status tracking.
	if opts.Site != nil {
		currentState.Site = opts.Site.Name
	}
	if opts.Profile != nil {
		currentState.Profile = opts.Profile.Name
	}

	// Resolve which stages to run.
	stages, err := p.registry.Resolve(opts.ResolveOpts, currentState)
	if err != nil {
		return fmt.Errorf("resolving stages: %w", err)
	}

	if opts.Parallel {
		return p.runParallel(ctx, opts, stages, currentState)
	}

	for _, s := range stages {
		if err := p.executeStage(ctx, opts, s, currentState, nil); err != nil {
			return err
		}
	}

	return nil
}

// executeStage runs a single stage through the plan/validate/apply lifecycle.
// If mu is non-nil, it is used to synchronize access to currentState.
func (p *Pipeline) executeStage(ctx context.Context, opts RunOpts, s Stage, currentState *state.State, mu *sync.Mutex) error {
	num := s.Number()
	name := s.Name()

	p.printer.StageHeader(num, name)

	// Read the current stage state (may be nil) and check if already deployed.
	if mu != nil {
		mu.Lock()
	}
	currentStageState := currentState.Stages[num]
	if mu != nil {
		mu.Unlock()
	}
	if currentStageState != nil && currentStageState.Status == "deployed" && !opts.Force {
		p.printer.Infof("  Stage %d already deployed, skipping (use --force to re-apply)", num)
		return nil
	}

	// Plan.
	plan, err := s.Plan(ctx, opts.KubeClient, opts.Profile, currentStageState)
	if err != nil {
		return fmt.Errorf("planning stage %d (%s): %w", num, name, err)
	}

	// Dry run: print plan and move on.
	if opts.DryRun {
		p.printer.Infof("  Plan: %s (dry-run)", plan.Action)
		return nil
	}

	// Skip if the plan says so.
	if plan.Action == "skip" {
		p.printer.Infof("  Stage %d: no changes needed, skipping", num)
		return nil
	}

	// Validate before applying.
	if err := s.Validate(ctx, opts.KubeClient, opts.Profile); err != nil {
		return fmt.Errorf("validating stage %d (%s): %w", num, name, err)
	}

	// Apply.
	if err := s.Apply(ctx, opts.KubeClient, opts.HelmClient, opts.Site, opts.Profile, plan, p.printer); err != nil {
		// Record failure in state.
		if mu != nil {
			mu.Lock()
		}
		currentState.Stages[num] = &state.StageState{
			Status:  "failed",
			Version: plan.Name,
			Applied: time.Now(),
			Error:   err.Error(),
		}
		if err := p.store.Save(ctx, currentState); err != nil {
			p.printer.Debugf("saving state after stage %d failure: %v", num, err)
		}
		if mu != nil {
			mu.Unlock()
		}
		return fmt.Errorf("applying stage %d (%s): %w", num, name, err)
	}

	// Record success in state, including per-component versions.
	components := make(map[string]*state.ComponentState)
	for _, comp := range plan.Components {
		components[comp.Name] = &state.ComponentState{
			Version: comp.Version,
			Status:  comp.Action,
		}
	}
	if mu != nil {
		mu.Lock()
	}
	currentState.Stages[num] = &state.StageState{
		Status:     "deployed",
		Version:    plan.Name,
		Applied:    time.Now(),
		Components: components,
	}
	if err := p.store.Save(ctx, currentState); err != nil {
		if mu != nil {
			mu.Unlock()
		}
		return fmt.Errorf("saving state after stage %d: %w", num, err)
	}
	if mu != nil {
		mu.Unlock()
	}

	return nil
}

// runParallel executes stages grouped by dependency level. Stages within the
// same level run concurrently; levels are processed in order.
func (p *Pipeline) runParallel(ctx context.Context, opts RunOpts, stages []Stage, currentState *state.State) error {
	levels := assignLevels(stages)
	maxLevel := 0
	for _, lvl := range levels {
		if lvl > maxLevel {
			maxLevel = lvl
		}
	}

	var mu sync.Mutex

	for lvl := 0; lvl <= maxLevel; lvl++ {
		var levelStages []Stage
		for _, s := range stages {
			if levels[s.Number()] == lvl {
				levelStages = append(levelStages, s)
			}
		}

		if len(levelStages) == 0 {
			continue
		}

		errs := make(chan error, len(levelStages))
		var wg sync.WaitGroup

		for _, s := range levelStages {
			wg.Add(1)
			go func(stage Stage) {
				defer wg.Done()
				if err := p.executeStage(ctx, opts, stage, currentState, &mu); err != nil {
					errs <- fmt.Errorf("stage %d (%s): %w", stage.Number(), stage.Name(), err)
				}
			}(s)
		}

		wg.Wait()
		close(errs)

		// Return first error, if any.
		for err := range errs {
			return err
		}
	}
	return nil
}

// assignLevels computes a dependency-based level for each stage. Level 0
// stages have no unsatisfied dependencies; level N stages depend on at least
// one stage at level N-1.
func assignLevels(stages []Stage) map[int]int {
	levels := make(map[int]int)
	stageSet := make(map[int]bool)
	for _, s := range stages {
		stageSet[s.Number()] = true
	}

	changed := true
	for changed {
		changed = false
		for _, s := range stages {
			if _, ok := levels[s.Number()]; ok {
				continue
			}
			maxDep := -1
			allResolved := true
			for _, dep := range s.Dependencies() {
				if !stageSet[dep] {
					// Dep not in our set — assumed already deployed.
					continue
				}
				depLvl, ok := levels[dep]
				if !ok {
					allResolved = false
					break
				}
				if depLvl > maxDep {
					maxDep = depLvl
				}
			}
			if allResolved {
				levels[s.Number()] = maxDep + 1
				changed = true
			}
		}
	}
	return levels
}
