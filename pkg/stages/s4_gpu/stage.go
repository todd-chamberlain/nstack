package s4_gpu

import (
	"context"
	"fmt"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/engine"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
	"github.com/todd-chamberlain/nstack/pkg/state"
)

// GPUStage implements the Stage interface for deploying cert-manager and
// the NVIDIA GPU Operator.
type GPUStage struct{}

// New returns a new GPUStage instance.
func New() *GPUStage { return &GPUStage{} }

func (s *GPUStage) Number() int         { return 4 }
func (s *GPUStage) Name() string        { return "GPU Stack" }
func (s *GPUStage) Dependencies() []int { return nil }

// Detect checks for existing cert-manager and GPU Operator deployments.
func (s *GPUStage) Detect(ctx context.Context, kc *kube.Client) (*engine.DetectResult, error) {
	cs := kc.Clientset()
	return &engine.DetectResult{
		Operators: []engine.DetectedOperator{
			engine.DetectDeployment(ctx, cs, certManagerNamespace, certManagerRelease, "cert-manager"),
			engine.DetectDeployment(ctx, cs, gpuOperatorNamespace, gpuOperatorRelease, "gpu-operator"),
			engine.DetectDeployment(ctx, cs, kaiSchedulerNamespace, "kai-scheduler", "kai-scheduler"),
		},
	}, nil
}

// Validate verifies the cluster is reachable.
func (s *GPUStage) Validate(ctx context.Context, kc *kube.Client, profile *config.Profile) error {
	return engine.ValidateClusterReachable(ctx, kc.Clientset())
}

// Plan builds a StagePlan describing what actions to take for cert-manager
// and the GPU Operator.
func (s *GPUStage) Plan(ctx context.Context, kc *kube.Client, profile *config.Profile, current *state.StageState) (*engine.StagePlan, error) {
	plan := &engine.StagePlan{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	cs := kc.Clientset()

	// Plan cert-manager, GPU Operator, and KAI Scheduler.
	plan.Components = append(plan.Components,
		engine.PlanDeploymentComponent(ctx, cs, "cert-manager", certManagerChart, certManagerVersion, certManagerNamespace, certManagerRelease),
		engine.PlanDeploymentComponent(ctx, cs, "gpu-operator", gpuOperatorChart, gpuOperatorVersion, gpuOperatorNamespace, gpuOperatorRelease),
		engine.PlanDeploymentComponent(ctx, cs, "kai-scheduler", kaiSchedulerChart, kaiSchedulerVersion, kaiSchedulerNamespace, "kai-scheduler"),
	)

	// Determine overall stage action.
	plan.Action = engine.DeterminePlanAction(plan.Components, plan.Patches)

	return plan, nil
}

// Apply executes the stage plan, installing cert-manager and the GPU Operator
// as needed.
func (s *GPUStage) Apply(ctx context.Context, kc *kube.Client, hc *helm.Client, site *config.Site, profile *config.Profile, plan *engine.StagePlan, printer *output.Printer) error {
	total := len(plan.Components)

	for i, comp := range plan.Components {
		idx := i + 1

		switch comp.Action {
		case "skip":
			printer.ComponentSkipped(idx, total, comp.Name, comp.Current, "already installed")
			continue

		case "install":
			printer.ComponentStart(idx, total, comp.Name, comp.Version, "installing")
			var err error

			switch comp.Name {
			case "cert-manager":
				err = installCertManager(ctx, hc, site, printer)
			case "gpu-operator":
				var overrides map[string]interface{}
				if site != nil && site.Overrides != nil {
					overrides = site.Overrides["gpu-operator"]
				}
				err = installGPUOperator(ctx, hc, site, profile, overrides, printer)
			case "kai-scheduler":
				if site == nil || site.Overrides == nil {
					printer.ComponentSkipped(idx, total, comp.Name, "", "no site overrides for kai-scheduler")
					continue
				}
				if _, hasKAI := site.Overrides["kai-scheduler"]; !hasKAI {
					printer.ComponentSkipped(idx, total, comp.Name, "", "kai-scheduler not requested in site overrides")
					continue
				}
				overrides := site.Overrides["kai-scheduler"]
				err = installKAIScheduler(ctx, hc, site, overrides, printer)
			default:
				err = fmt.Errorf("unknown component: %s", comp.Name)
			}

			printer.ComponentDone(comp.Name, err)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// Status reports the current runtime health of the GPU Stack.
func (s *GPUStage) Status(ctx context.Context, kc *kube.Client) (*engine.StageStatus, error) {
	cs := kc.Clientset()

	cmStatus := engine.CheckDeploymentStatus(ctx, cs, certManagerNamespace, certManagerRelease, "cert-manager")
	gpuStatus := engine.CheckDeploymentStatus(ctx, cs, gpuOperatorNamespace, gpuOperatorRelease, "gpu-operator")

	status := &engine.StageStatus{
		Stage:   s.Number(),
		Name:    s.Name(),
		Version: gpuStatus.Version,
		Components: []engine.ComponentStatus{
			cmStatus,
			gpuStatus,
		},
	}

	// Check KAI Scheduler (optional -- only include if deployed).
	kaiStatus := engine.CheckDeploymentStatus(ctx, cs, kaiSchedulerNamespace, "kai-scheduler", "kai-scheduler")
	if kaiStatus.Status != "not-installed" {
		status.Components = append(status.Components, kaiStatus)
	}

	status.Status = engine.DetermineOverallStatus(status.Components)

	return status, nil
}

// Destroy removes the KAI Scheduler, GPU Operator, and cert-manager from
// the cluster. KAI Scheduler is removed first, then GPU Operator (which may
// depend on cert-manager CRDs), then cert-manager.
func (s *GPUStage) Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error {
	total := 3

	releases := []struct {
		name, release, namespace string
	}{
		{"kai-scheduler", kaiSchedulerRelease, kaiSchedulerNamespace},
		{"gpu-operator", gpuOperatorRelease, gpuOperatorNamespace},
		{"cert-manager", certManagerRelease, certManagerNamespace},
	}

	for i, r := range releases {
		if err := engine.DestroyHelmRelease(hc, r.name, r.release, r.namespace, i+1, total, printer); err != nil {
			return err
		}
	}

	return nil
}
