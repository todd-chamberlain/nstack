package s4_gpu

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/engine"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
	"github.com/todd-chamberlain/nstack/pkg/state"
)

// extractVersion returns the image tag from the first container, or "unknown".
func extractVersion(containers []corev1.Container) string {
	if len(containers) == 0 {
		return "unknown"
	}
	img := containers[0].Image
	if idx := strings.LastIndex(img, ":"); idx >= 0 {
		return img[idx+1:]
	}
	return "unknown"
}

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
			Version:   extractVersion(cmDep.Spec.Template.Spec.Containers),
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
			Version:   extractVersion(gpuDep.Spec.Template.Spec.Containers),
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
		cmVersion := extractVersion(cmDep.Spec.Template.Spec.Containers)
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
		gpuVersion := extractVersion(gpuDep.Spec.Template.Spec.Containers)
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

	// Determine overall stage action.
	hasInstall := false
	allSkip := true
	for _, c := range plan.Components {
		if c.Action == "install" {
			hasInstall = true
			allSkip = false
		}
	}
	if allSkip {
		plan.Action = "skip"
	} else if hasInstall {
		plan.Action = "install"
	}

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
			printer.ComponentSkipped(comp.Name, comp.Current, "already installed")
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
		cmStatus.Version = extractVersion(cmDep.Spec.Template.Spec.Containers)
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
		gpuStatus.Version = extractVersion(gpuDep.Spec.Template.Spec.Containers)
		if gpuDep.Status.AvailableReplicas >= 1 {
			gpuStatus.Status = "running"
		} else {
			gpuStatus.Status = "degraded"
		}
		status.Version = gpuStatus.Version
		status.Applied = gpuDep.CreationTimestamp.Time
	}
	status.Components = append(status.Components, gpuStatus)

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

// Destroy removes the GPU Operator and cert-manager from the cluster.
// The GPU Operator is removed first since it may depend on cert-manager CRDs.
func (s *GPUStage) Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error {
	// Uninstall GPU Operator first.
	hc.SetNamespace(gpuOperatorNamespace)
	installed, version, err := hc.IsInstalled(gpuOperatorRelease)
	if err != nil {
		return fmt.Errorf("checking gpu-operator: %w", err)
	}
	if installed {
		printer.ComponentStart(1, 2, "gpu-operator", version, "destroying")
		err = hc.Uninstall(gpuOperatorRelease)
		printer.ComponentDone("gpu-operator", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped("gpu-operator", "", "not installed")
	}

	// Uninstall cert-manager.
	hc.SetNamespace(certManagerNamespace)
	installed, version, err = hc.IsInstalled(certManagerRelease)
	if err != nil {
		return fmt.Errorf("checking cert-manager: %w", err)
	}
	if installed {
		printer.ComponentStart(2, 2, "cert-manager", version, "destroying")
		err = hc.Uninstall(certManagerRelease)
		printer.ComponentDone("cert-manager", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped("cert-manager", "", "not installed")
	}

	return nil
}
