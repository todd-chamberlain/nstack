package engine

import (
	"context"
	"fmt"
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

	// Resolve which stages to run.
	stages, err := p.registry.Resolve(opts.ResolveOpts, currentState)
	if err != nil {
		return fmt.Errorf("resolving stages: %w", err)
	}

	for _, s := range stages {
		num := s.Number()
		name := s.Name()

		p.printer.StageHeader(num, name)

		// Check if already deployed and not forced.
		if ss, ok := currentState.Stages[num]; ok && ss.Status == "deployed" && !opts.Force {
			p.printer.Infof("  Stage %d already deployed, skipping (use --force to re-apply)", num)
			continue
		}

		// Get the current stage state (may be nil).
		var currentStageState *state.StageState
		if ss, ok := currentState.Stages[num]; ok {
			currentStageState = ss
		}

		// Plan.
		plan, err := s.Plan(ctx, opts.KubeClient, opts.Profile, currentStageState)
		if err != nil {
			return fmt.Errorf("planning stage %d (%s): %w", num, name, err)
		}

		// Dry run: print plan and move on.
		if opts.DryRun {
			p.printer.Infof("  Plan: %s (dry-run)", plan.Action)
			continue
		}

		// Skip if the plan says so.
		if plan.Action == "skip" {
			p.printer.Infof("  Stage %d: no changes needed, skipping", num)
			continue
		}

		// Validate before applying.
		if err := s.Validate(ctx, opts.KubeClient, opts.Profile); err != nil {
			return fmt.Errorf("validating stage %d (%s): %w", num, name, err)
		}

		// Apply.
		if err := s.Apply(ctx, opts.KubeClient, opts.HelmClient, opts.Site, opts.Profile, plan, p.printer); err != nil {
			// Record failure in state.
			currentState.Stages[num] = &state.StageState{
				Status:  "failed",
				Version: plan.Name,
				Applied: time.Now(),
				Error:   err.Error(),
			}
			_ = p.store.Save(ctx, currentState)
			return fmt.Errorf("applying stage %d (%s): %w", num, name, err)
		}

		// Record success in state.
		currentState.Stages[num] = &state.StageState{
			Status:  "deployed",
			Version: plan.Name,
			Applied: time.Now(),
		}
		if err := p.store.Save(ctx, currentState); err != nil {
			return fmt.Errorf("saving state after stage %d: %w", num, err)
		}
	}

	return nil
}
