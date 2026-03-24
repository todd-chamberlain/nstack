package engine

import (
	"fmt"
	"sort"

	"github.com/todd-chamberlain/nstack/pkg/state"
)

// Registry holds all registered stages and provides dependency-aware resolution.
type Registry struct {
	stages map[int]Stage
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		stages: make(map[int]Stage),
	}
}

// Register adds a stage to the registry, keyed by its Number().
func (r *Registry) Register(s Stage) {
	r.stages[s.Number()] = s
}

// Get retrieves a stage by number. Returns false if not found.
func (r *Registry) Get(num int) (Stage, bool) {
	s, ok := r.stages[num]
	return s, ok
}

// All returns every registered stage sorted by Number() in ascending order.
func (r *Registry) All() []Stage {
	stages := make([]Stage, 0, len(r.stages))
	for _, s := range r.stages {
		stages = append(stages, s)
	}
	sort.Slice(stages, func(i, j int) bool {
		return stages[i].Number() < stages[j].Number()
	})
	return stages
}

// ResolveOpts controls which stages are selected and in what order.
type ResolveOpts struct {
	From   int   // --from: start here, include all stages with number >= From
	Only   int   // --only: just this one stage (0 means not set)
	Stages []int // --stages: cherry-pick list of stage numbers
}

// Resolve returns an ordered list of stages based on the given options.
// It validates that every dependency for each resolved stage is either
// included in the resolved set or already deployed in currentState.
func (r *Registry) Resolve(opts ResolveOpts, currentState *state.State) ([]Stage, error) {
	var selected []Stage

	switch {
	case opts.Only > 0:
		s, ok := r.stages[opts.Only]
		if !ok {
			return nil, fmt.Errorf("stage %d not found in registry", opts.Only)
		}
		selected = []Stage{s}

	case len(opts.Stages) > 0:
		for _, num := range opts.Stages {
			s, ok := r.stages[num]
			if !ok {
				return nil, fmt.Errorf("stage %d not found in registry", num)
			}
			selected = append(selected, s)
		}
		sort.Slice(selected, func(i, j int) bool {
			return selected[i].Number() < selected[j].Number()
		})

	case opts.From > 0:
		all := r.All()
		for _, s := range all {
			if s.Number() >= opts.From {
				selected = append(selected, s)
			}
		}

	default:
		selected = r.All()
	}

	// Build a set of stage numbers in the resolved set for fast lookup.
	resolvedSet := make(map[int]bool, len(selected))
	for _, s := range selected {
		resolvedSet[s.Number()] = true
	}

	// Validate dependencies: each dep must be in the resolved set or deployed in state.
	for _, s := range selected {
		for _, dep := range s.Dependencies() {
			if resolvedSet[dep] {
				continue
			}
			if ss, ok := currentState.Stages[dep]; ok && ss.Status == "deployed" {
				continue
			}
			return nil, fmt.Errorf("stage %d (%s) depends on stage %d which is neither included nor deployed", s.Number(), s.Name(), dep)
		}
	}

	return selected, nil
}
