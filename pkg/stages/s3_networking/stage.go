package s3_networking

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

// NetworkingStage implements the Stage interface for deploying the NVIDIA
// Network Operator, Multus CNI, and optionally the DOCA Platform for DPUs.
type NetworkingStage struct{}

// New returns a new NetworkingStage instance.
func New() *NetworkingStage { return &NetworkingStage{} }

func (s *NetworkingStage) Number() int         { return 3 }
func (s *NetworkingStage) Name() string        { return "Networking" }
func (s *NetworkingStage) Dependencies() []int { return nil }

// Detect checks for existing Network Operator and DOCA deployments.
func (s *NetworkingStage) Detect(ctx context.Context, kc *kube.Client) (*engine.DetectResult, error) {
	result := &engine.DetectResult{}
	cs := kc.Clientset()

	// Check Network Operator.
	noDep, err := cs.AppsV1().Deployments(networkOperatorNamespace).Get(ctx, networkOperatorRelease, metav1.GetOptions{})
	if err == nil {
		opStatus := "degraded"
		if noDep.Status.AvailableReplicas >= 1 {
			opStatus = "running"
		}
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "network-operator",
			Version:   kube.ExtractImageVersion(noDep.Spec.Template.Spec.Containers),
			Namespace: networkOperatorNamespace,
			Status:    opStatus,
		})
	} else {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "network-operator",
			Namespace: networkOperatorNamespace,
			Status:    "not-installed",
		})
	}

	// Check DOCA Operator.
	docaDep, err := cs.AppsV1().Deployments(docaNamespace).Get(ctx, docaRelease, metav1.GetOptions{})
	if err == nil {
		opStatus := "degraded"
		if docaDep.Status.AvailableReplicas >= 1 {
			opStatus = "running"
		}
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "doca-platform",
			Version:   kube.ExtractImageVersion(docaDep.Spec.Template.Spec.Containers),
			Namespace: docaNamespace,
			Status:    opStatus,
		})
	} else {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "doca-platform",
			Namespace: docaNamespace,
			Status:    "not-installed",
		})
	}

	return result, nil
}

// Validate verifies the cluster is reachable.
func (s *NetworkingStage) Validate(ctx context.Context, kc *kube.Client, profile *config.Profile) error {
	_, err := kc.Clientset().CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return fmt.Errorf("cluster not reachable: %w", err)
	}
	return nil
}

// Plan builds a StagePlan describing what actions to take for the
// Network Operator and DOCA components.
func (s *NetworkingStage) Plan(ctx context.Context, kc *kube.Client, profile *config.Profile, current *state.StageState) (*engine.StagePlan, error) {
	plan := &engine.StagePlan{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	cs := kc.Clientset()

	// Plan network-operator: skip if no fabric configured, otherwise check deployment.
	if !hasFabric(nil, profile) {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "network-operator",
			Action:    "skip",
			Chart:     networkOperatorChart,
			Version:   networkOperatorVersion,
			Namespace: networkOperatorNamespace,
		})
	} else {
		noDep, noErr := cs.AppsV1().Deployments(networkOperatorNamespace).Get(ctx, networkOperatorRelease, metav1.GetOptions{})
		if noErr == nil {
			noVersion := kube.ExtractImageVersion(noDep.Spec.Template.Spec.Containers)
			plan.Components = append(plan.Components, engine.ComponentPlan{
				Name:      "network-operator",
				Action:    "skip",
				Chart:     networkOperatorChart,
				Version:   networkOperatorVersion,
				Current:   noVersion,
				Namespace: networkOperatorNamespace,
			})
		} else {
			plan.Components = append(plan.Components, engine.ComponentPlan{
				Name:      "network-operator",
				Action:    "install",
				Chart:     networkOperatorChart,
				Version:   networkOperatorVersion,
				Namespace: networkOperatorNamespace,
			})
		}
	}

	// Plan DOCA: skip unless DPU nodes are present.
	// Note: we cannot check site config here (Plan only receives profile),
	// so we check if DOCA is already deployed. Apply() decides whether to
	// install DOCA based on the full site config.
	docaDep, docaErr := cs.AppsV1().Deployments(docaNamespace).Get(ctx, docaRelease, metav1.GetOptions{})
	if docaErr == nil {
		docaVersion := kube.ExtractImageVersion(docaDep.Spec.Template.Spec.Containers)
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "doca-platform",
			Action:    "skip",
			Chart:     docaChart,
			Version:   docaVersion,
			Current:   docaVersion,
			Namespace: docaNamespace,
		})
	} else {
		// Mark as install candidate; Apply() will decide based on site DPU info.
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "doca-platform",
			Action:    "install",
			Chart:     docaChart,
			Version:   docaVersion,
			Namespace: docaNamespace,
		})
	}

	plan.Action = engine.DeterminePlanAction(plan.Components, plan.Patches)

	return plan, nil
}

// Apply executes the stage plan, installing the Network Operator and
// optionally DOCA as needed.
func (s *NetworkingStage) Apply(ctx context.Context, kc *kube.Client, hc *helm.Client, site *config.Site, profile *config.Profile, plan *engine.StagePlan, printer *output.Printer) error {
	total := len(plan.Components)

	for i, comp := range plan.Components {
		idx := i + 1

		switch comp.Action {
		case "skip":
			printer.ComponentSkipped(idx, total, comp.Name, comp.Current, "already installed")
			continue

		case "install":
			var err error

			switch comp.Name {
			case "network-operator":
				if !hasFabric(site, profile) {
					printer.ComponentSkipped(idx, total, comp.Name, "", "no fabric configured")
					continue
				}
				printer.ComponentStart(idx, total, comp.Name, comp.Version, "installing")
				err = installNetworkOperator(ctx, hc, site, profile, printer)

			case "doca-platform":
				if !hasDPUs(site) {
					printer.ComponentSkipped(idx, total, comp.Name, "", "no DPUs detected")
					continue
				}
				printer.ComponentStart(idx, total, comp.Name, comp.Version, "installing")
				err = installDOCA(ctx, hc, site, profile, printer)

			default:
				printer.ComponentStart(idx, total, comp.Name, comp.Version, "installing")
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

// Status reports the current runtime health of the Networking stage.
func (s *NetworkingStage) Status(ctx context.Context, kc *kube.Client) (*engine.StageStatus, error) {
	status := &engine.StageStatus{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	cs := kc.Clientset()

	// Check Network Operator deployment.
	noStatus := engine.ComponentStatus{
		Name:      "network-operator",
		Namespace: networkOperatorNamespace,
	}
	noDep, err := cs.AppsV1().Deployments(networkOperatorNamespace).Get(ctx, networkOperatorRelease, metav1.GetOptions{})
	if err != nil {
		noStatus.Status = "not-installed"
	} else {
		noStatus.Pods = int(noDep.Status.Replicas)
		noStatus.Ready = int(noDep.Status.ReadyReplicas)
		noStatus.Version = kube.ExtractImageVersion(noDep.Spec.Template.Spec.Containers)
		if noDep.Status.AvailableReplicas >= 1 {
			noStatus.Status = "running"
		} else {
			noStatus.Status = "degraded"
		}
		status.Version = noStatus.Version
		status.Applied = noDep.CreationTimestamp.Time
	}
	status.Components = append(status.Components, noStatus)

	// Check DOCA deployment.
	docaStatus := engine.ComponentStatus{
		Name:      "doca-platform",
		Namespace: docaNamespace,
	}
	docaDep, err := cs.AppsV1().Deployments(docaNamespace).Get(ctx, docaRelease, metav1.GetOptions{})
	if err != nil {
		docaStatus.Status = "not-installed"
	} else {
		docaStatus.Pods = int(docaDep.Status.Replicas)
		docaStatus.Ready = int(docaDep.Status.ReadyReplicas)
		docaStatus.Version = kube.ExtractImageVersion(docaDep.Spec.Template.Spec.Containers)
		if docaDep.Status.AvailableReplicas >= 1 {
			docaStatus.Status = "running"
		} else {
			docaStatus.Status = "degraded"
		}
	}
	status.Components = append(status.Components, docaStatus)

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

// Destroy removes the DOCA Platform and Network Operator from the cluster.
// DOCA is removed first since it may depend on Network Operator CRDs.
func (s *NetworkingStage) Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error {
	// Uninstall DOCA first.
	installed, version, err := hc.IsInstalled(docaRelease, docaNamespace)
	if err != nil {
		return fmt.Errorf("checking doca: %w", err)
	}
	if installed {
		printer.ComponentStart(1, 2, "doca-platform", version, "destroying")
		err = hc.Uninstall(docaRelease, docaNamespace)
		printer.ComponentDone("doca-platform", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped(1, 2, "doca-platform", "", "not installed")
	}

	// Uninstall Network Operator.
	installed, version, err = hc.IsInstalled(networkOperatorRelease, networkOperatorNamespace)
	if err != nil {
		return fmt.Errorf("checking network-operator: %w", err)
	}
	if installed {
		printer.ComponentStart(2, 2, "network-operator", version, "destroying")
		err = hc.Uninstall(networkOperatorRelease, networkOperatorNamespace)
		printer.ComponentDone("network-operator", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped(2, 2, "network-operator", "", "not installed")
	}

	return nil
}
