package engine

import (
	"context"
	"time"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
	"github.com/todd-chamberlain/nstack/pkg/state"
)

// Stage defines the contract every deployment stage must satisfy.
// Each stage knows its ordering number, dependencies, and how to detect,
// validate, plan, apply, report status, and destroy its components.
type Stage interface {
	Number() int
	Name() string
	Dependencies() []int
	Detect(ctx context.Context, kc *kube.Client) (*DetectResult, error)
	Validate(ctx context.Context, kc *kube.Client, profile *config.Profile) error
	Plan(ctx context.Context, kc *kube.Client, profile *config.Profile, current *state.StageState) (*StagePlan, error)
	Apply(ctx context.Context, kc *kube.Client, hc *helm.Client, site *config.Site, profile *config.Profile, plan *StagePlan, printer *output.Printer) error
	Status(ctx context.Context, kc *kube.Client) (*StageStatus, error)
	Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error
}

// StagePlan describes the actions a stage intends to take.
type StagePlan struct {
	Stage      int             `json:"stage"`
	Name       string          `json:"name"`
	Action     string          `json:"action"` // "install", "upgrade", "skip", "destroy"
	Components []ComponentPlan `json:"components"`
	Patches    []PatchPlan     `json:"patches,omitempty"`
}

// ComponentPlan describes a planned action for a single component within a stage.
type ComponentPlan struct {
	Name      string `json:"name"`
	Action    string `json:"action"` // "install", "upgrade", "skip", "no-change"
	Chart     string `json:"chart"`
	Version   string `json:"version"`
	Current   string `json:"current"`
	Namespace string `json:"namespace"`
}

// PatchPlan describes a conditional patch to be applied during a stage.
type PatchPlan struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Condition   string `json:"condition"`
	Applied     bool   `json:"applied"`
}

// DetectResult holds the outcome of detecting existing operators in a cluster.
type DetectResult struct {
	Operators []DetectedOperator `json:"operators"`
}

// DetectedOperator represents a running operator found during detection.
type DetectedOperator struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
}

// StageStatus reports the current runtime status of a deployed stage.
type StageStatus struct {
	Stage      int               `json:"stage"`
	Name       string            `json:"name"`
	Status     string            `json:"status"` // "deployed", "failed", "not-installed", "degraded"
	Version    string            `json:"version"`
	Applied    time.Time         `json:"applied"`
	Components []ComponentStatus `json:"components"`
	Error      string            `json:"error,omitempty"`
}

// ComponentStatus reports the runtime health of a single component.
type ComponentStatus struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Version   string `json:"version"`
	Pods      int    `json:"pods"`
	Ready     int    `json:"ready"`
	Namespace string `json:"namespace"`
}

// DeterminePlanAction derives the overall action for a stage plan
// based on its component actions and patches.
func DeterminePlanAction(components []ComponentPlan, patches []PatchPlan) string {
	hasInstall := false
	allSkip := true
	for _, c := range components {
		if c.Action == "install" || c.Action == "upgrade" {
			hasInstall = true
			allSkip = false
		} else if c.Action != "skip" && c.Action != "no-change" {
			allSkip = false
		}
	}
	if len(patches) > 0 {
		for _, p := range patches {
			if !p.Applied {
				hasInstall = true
				allSkip = false
				break
			}
		}
	}
	if allSkip && !hasInstall {
		return "skip"
	}
	if hasInstall {
		return "install"
	}
	return "no-change"
}
