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
	cs := kc.Clientset()
	return &engine.DetectResult{
		Operators: []engine.DetectedOperator{
			engine.DetectDeployment(ctx, cs, networkOperatorNamespace, networkOperatorRelease, "network-operator"),
			engine.DetectDeployment(ctx, cs, docaNamespace, docaRelease, "doca-platform"),
		},
	}, nil
}

// Validate verifies the cluster is reachable.
func (s *NetworkingStage) Validate(ctx context.Context, kc *kube.Client, profile *config.Profile) error {
	return engine.ValidateClusterReachable(ctx, kc.Clientset())
}

// Plan builds a StagePlan describing what actions to take for the overlay,
// Network Operator, and DOCA components.
func (s *NetworkingStage) Plan(ctx context.Context, kc *kube.Client, profile *config.Profile, current *state.StageState) (*engine.StagePlan, error) {
	plan := &engine.StagePlan{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	cs := kc.Clientset()

	// Plan overlay component (prepended before network-operator).
	overlayType := "none"
	if profile != nil && profile.Networking.Overlay != "" {
		overlayType = profile.Networking.Overlay
	}
	overlayAction := "skip"
	if overlayType != "none" {
		overlayAction = "install"
	}
	plan.Components = append(plan.Components, engine.ComponentPlan{
		Name:      "overlay",
		Action:    overlayAction,
		Namespace: "kube-system",
	})

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
		plan.Components = append(plan.Components,
			engine.PlanDeploymentComponent(ctx, cs, "network-operator", networkOperatorChart, networkOperatorVersion, networkOperatorNamespace, networkOperatorRelease))
	}

	// Plan DOCA: skip unless DPU nodes are present.
	// Note: we cannot check site config here (Plan only receives profile),
	// so we check if DOCA is already deployed. Apply() decides whether to
	// install DOCA based on the full site config.
	plan.Components = append(plan.Components,
		engine.PlanDeploymentComponent(ctx, cs, "doca-platform", docaChart, docaVersion, docaNamespace, docaRelease))

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
			case "overlay":
				printer.ComponentStart(idx, total, comp.Name, comp.Version, "configuring")
				err = configureOverlay(ctx, kc, hc, site, profile, printer)

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
	cs := kc.Clientset()

	status := &engine.StageStatus{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	// Check Network Operator deployment.
	networkOpStatus := engine.CheckDeploymentStatus(ctx, cs, networkOperatorNamespace, networkOperatorRelease, "network-operator")
	status.Version = networkOpStatus.Version
	status.Components = append(status.Components, networkOpStatus)

	// Only check DOCA if it appears to have been deployed (namespace exists).
	// On clusters without DPUs, DOCA is never installed, so we treat it as
	// not-applicable rather than not-installed.
	_, nsErr := cs.CoreV1().Namespaces().Get(ctx, docaNamespace, metav1.GetOptions{})
	if nsErr == nil {
		status.Components = append(status.Components,
			engine.CheckDeploymentStatus(ctx, cs, docaNamespace, docaRelease, "doca-platform"))
	}

	status.Status = engine.DetermineOverallStatus(status.Components)

	return status, nil
}

// Destroy removes the DOCA Platform, Network Operator, and overlay from the cluster.
// DOCA is removed first since it may depend on Network Operator CRDs.
// Overlay is removed last since it is independent infrastructure.
func (s *NetworkingStage) Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error {
	total := 3

	releases := []struct {
		name, release, namespace string
	}{
		{"doca-platform", docaRelease, docaNamespace},
		{"network-operator", networkOperatorRelease, networkOperatorNamespace},
		{"overlay", "tailscale-operator", "tailscale-system"},
	}

	for i, r := range releases {
		if err := engine.DestroyHelmRelease(hc, r.name, r.release, r.namespace, i+1, total, printer); err != nil {
			return err
		}
	}

	return nil
}
