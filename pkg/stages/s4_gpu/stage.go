package s4_gpu

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	result := &engine.DetectResult{}
	cs := kc.Clientset()

	// Check cert-manager.
	cmDep, err := cs.AppsV1().Deployments(certManagerNamespace).Get(ctx, certManagerRelease, metav1.GetOptions{})
	if err == nil {
		opStatus := "degraded"
		if cmDep.Status.AvailableReplicas >= 1 {
			opStatus = "running"
		}
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "cert-manager",
			Version:   kube.ExtractImageVersion(cmDep.Spec.Template.Spec.Containers),
			Namespace: certManagerNamespace,
			Status:    opStatus,
		})
	} else {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "cert-manager",
			Namespace: certManagerNamespace,
			Status:    "not-installed",
		})
	}

	// Check GPU Operator.
	gpuDep, err := cs.AppsV1().Deployments(gpuOperatorNamespace).Get(ctx, gpuOperatorRelease, metav1.GetOptions{})
	if err == nil {
		opStatus := "degraded"
		if gpuDep.Status.AvailableReplicas >= 1 {
			opStatus = "running"
		}
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "gpu-operator",
			Version:   kube.ExtractImageVersion(gpuDep.Spec.Template.Spec.Containers),
			Namespace: gpuOperatorNamespace,
			Status:    opStatus,
		})
	} else {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "gpu-operator",
			Namespace: gpuOperatorNamespace,
			Status:    "not-installed",
		})
	}

	// Check KAI Scheduler
	kaiInstalled, kaiVersion := isKAISchedulerInstalled(ctx, kc)
	if kaiInstalled {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "kai-scheduler",
			Version:   kaiVersion,
			Namespace: kaiSchedulerNamespace,
			Status:    "running",
		})
	} else {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "kai-scheduler",
			Namespace: kaiSchedulerNamespace,
			Status:    "not-installed",
		})
	}

	return result, nil
}

// Validate verifies the cluster is reachable.
func (s *GPUStage) Validate(ctx context.Context, kc *kube.Client, profile *config.Profile) error {
	_, err := kc.Clientset().CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return fmt.Errorf("cluster not reachable: %w", err)
	}
	return nil
}

// Plan builds a StagePlan describing what actions to take for cert-manager
// and the GPU Operator.
func (s *GPUStage) Plan(ctx context.Context, kc *kube.Client, profile *config.Profile, current *state.StageState) (*engine.StagePlan, error) {
	plan := &engine.StagePlan{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	cs := kc.Clientset()

	// Plan cert-manager: check if its deployment exists.
	cmDep, cmErr := cs.AppsV1().Deployments(certManagerNamespace).Get(ctx, certManagerRelease, metav1.GetOptions{})
	if cmErr == nil {
		cmVersion := kube.ExtractImageVersion(cmDep.Spec.Template.Spec.Containers)
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "cert-manager",
			Action:    "skip",
			Chart:     certManagerChart,
			Version:   certManagerVersion,
			Current:   cmVersion,
			Namespace: certManagerNamespace,
		})
	} else {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "cert-manager",
			Action:    "install",
			Chart:     certManagerChart,
			Version:   certManagerVersion,
			Namespace: certManagerNamespace,
		})
	}

	// Plan GPU Operator: check if its deployment exists.
	gpuDep, gpuErr := cs.AppsV1().Deployments(gpuOperatorNamespace).Get(ctx, gpuOperatorRelease, metav1.GetOptions{})
	if gpuErr == nil {
		gpuVersion := kube.ExtractImageVersion(gpuDep.Spec.Template.Spec.Containers)
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "gpu-operator",
			Action:    "skip",
			Chart:     gpuOperatorChart,
			Version:   gpuOperatorVersion,
			Current:   gpuVersion,
			Namespace: gpuOperatorNamespace,
		})
	} else {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "gpu-operator",
			Action:    "install",
			Chart:     gpuOperatorChart,
			Version:   gpuOperatorVersion,
			Namespace: gpuOperatorNamespace,
		})
	}

	// KAI Scheduler (optional — install candidate if already deployed,
	// or if it will be requested via site overrides at Apply time).
	kaiInstalled, kaiVersion := isKAISchedulerInstalled(ctx, kc)
	if kaiInstalled {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "kai-scheduler",
			Action:    "skip",
			Chart:     kaiSchedulerChart,
			Version:   kaiSchedulerVersion,
			Current:   kaiVersion,
			Namespace: kaiSchedulerNamespace,
		})
	} else {
		// Mark as install candidate; Apply() decides based on site overrides.
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "kai-scheduler",
			Action:    "install",
			Chart:     kaiSchedulerChart,
			Version:   kaiSchedulerVersion,
			Namespace: kaiSchedulerNamespace,
		})
	}

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
				err = installCertManager(ctx, hc, printer)
			case "gpu-operator":
				var overrides map[string]interface{}
				if site != nil && site.Overrides != nil {
					overrides = site.Overrides["gpu-operator"]
				}
				err = installGPUOperator(ctx, hc, profile, overrides, printer)
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
				err = installKAIScheduler(ctx, hc, overrides, printer)
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
	status := &engine.StageStatus{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	cs := kc.Clientset()

	// Check cert-manager deployment.
	cmStatus := engine.ComponentStatus{
		Name:      "cert-manager",
		Namespace: certManagerNamespace,
	}
	cmDep, err := cs.AppsV1().Deployments(certManagerNamespace).Get(ctx, certManagerRelease, metav1.GetOptions{})
	if err != nil {
		cmStatus.Status = "not-installed"
	} else {
		cmStatus.Pods = int(cmDep.Status.Replicas)
		cmStatus.Ready = int(cmDep.Status.ReadyReplicas)
		cmStatus.Version = kube.ExtractImageVersion(cmDep.Spec.Template.Spec.Containers)
		if cmDep.Status.AvailableReplicas >= 1 {
			cmStatus.Status = "running"
		} else {
			cmStatus.Status = "degraded"
		}
	}
	status.Components = append(status.Components, cmStatus)

	// Check GPU Operator deployment.
	gpuStatus := engine.ComponentStatus{
		Name:      "gpu-operator",
		Namespace: gpuOperatorNamespace,
	}
	gpuDep, err := cs.AppsV1().Deployments(gpuOperatorNamespace).Get(ctx, gpuOperatorRelease, metav1.GetOptions{})
	if err != nil {
		gpuStatus.Status = "not-installed"
	} else {
		gpuStatus.Pods = int(gpuDep.Status.Replicas)
		gpuStatus.Ready = int(gpuDep.Status.ReadyReplicas)
		gpuStatus.Version = kube.ExtractImageVersion(gpuDep.Spec.Template.Spec.Containers)
		if gpuDep.Status.AvailableReplicas >= 1 {
			gpuStatus.Status = "running"
		} else {
			gpuStatus.Status = "degraded"
		}
		status.Version = gpuStatus.Version
		status.Applied = gpuDep.CreationTimestamp.Time
	}
	status.Components = append(status.Components, gpuStatus)

	// Check KAI Scheduler (optional — only include if deployed).
	kaiInstalled, kaiVersion := isKAISchedulerInstalled(ctx, kc)
	if kaiInstalled {
		kaiDep, kaiErr := cs.AppsV1().Deployments(kaiSchedulerNamespace).Get(ctx, "kai-scheduler", metav1.GetOptions{})
		kaiStatus := engine.ComponentStatus{
			Name:      "kai-scheduler",
			Namespace: kaiSchedulerNamespace,
			Version:   kaiVersion,
		}
		if kaiErr != nil {
			kaiStatus.Status = "degraded"
		} else {
			kaiStatus.Pods = int(kaiDep.Status.Replicas)
			kaiStatus.Ready = int(kaiDep.Status.ReadyReplicas)
			if kaiDep.Status.AvailableReplicas >= 1 {
				kaiStatus.Status = "running"
			} else {
				kaiStatus.Status = "degraded"
			}
		}
		status.Components = append(status.Components, kaiStatus)
	}

	// Determine overall status.
	allRunning := true
	anyNotInstalled := false
	for _, c := range status.Components {
		if c.Status != "running" {
			allRunning = false
		}
		if c.Status == "not-installed" {
			anyNotInstalled = true
		}
	}

	switch {
	case anyNotInstalled:
		status.Status = "not-installed"
	case allRunning:
		status.Status = "deployed"
	default:
		status.Status = "degraded"
	}

	return status, nil
}

// Destroy removes the KAI Scheduler, GPU Operator, and cert-manager from
// the cluster. KAI Scheduler is removed first, then GPU Operator (which may
// depend on cert-manager CRDs), then cert-manager.
func (s *GPUStage) Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error {
	total := 3

	// Uninstall KAI Scheduler first (depends on GPU Operator).
	installed, version, err := hc.IsInstalled(kaiSchedulerRelease, kaiSchedulerNamespace)
	if err != nil {
		return fmt.Errorf("checking kai-scheduler: %w", err)
	}
	if installed {
		printer.ComponentStart(1, total, "kai-scheduler", version, "destroying")
		err = hc.Uninstall(kaiSchedulerRelease, kaiSchedulerNamespace)
		printer.ComponentDone("kai-scheduler", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped(1, total, "kai-scheduler", "", "not installed")
	}

	// Uninstall GPU Operator.
	installed, version, err = hc.IsInstalled(gpuOperatorRelease, gpuOperatorNamespace)
	if err != nil {
		return fmt.Errorf("checking gpu-operator: %w", err)
	}
	if installed {
		printer.ComponentStart(2, total, "gpu-operator", version, "destroying")
		err = hc.Uninstall(gpuOperatorRelease, gpuOperatorNamespace)
		printer.ComponentDone("gpu-operator", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped(2, total, "gpu-operator", "", "not installed")
	}

	// Uninstall cert-manager.
	installed, version, err = hc.IsInstalled(certManagerRelease, certManagerNamespace)
	if err != nil {
		return fmt.Errorf("checking cert-manager: %w", err)
	}
	if installed {
		printer.ComponentStart(3, total, "cert-manager", version, "destroying")
		err = hc.Uninstall(certManagerRelease, certManagerNamespace)
		printer.ComponentDone("cert-manager", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped(3, total, "cert-manager", "", "not installed")
	}

	return nil
}
