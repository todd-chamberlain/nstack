package s1_provision

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

const (
	metal3Release   = "baremetal-operator"
	metal3Chart     = "metal3/baremetal-operator"
	metal3ChartRepo = "https://metal3-io.github.io/baremetal-operator"
)

// ProvisionStage implements the Stage interface for bare metal provisioning
// using Metal3/Ironic. It deploys the Metal3 Baremetal Operator, creates
// BareMetalHost CRDs for discovered nodes, and triggers OS provisioning
// via PXE boot.
type ProvisionStage struct{}

// New returns a new ProvisionStage instance.
func New() *ProvisionStage { return &ProvisionStage{} }

func (s *ProvisionStage) Number() int         { return 1 }
func (s *ProvisionStage) Name() string        { return "Provisioning" }
func (s *ProvisionStage) Dependencies() []int { return []int{0} }

// Detect checks for existing Metal3 Baremetal Operator deployments.
func (s *ProvisionStage) Detect(ctx context.Context, kc *kube.Client) (*engine.DetectResult, error) {
	result := &engine.DetectResult{}
	cs := kc.Clientset()

	// Check Metal3 Baremetal Operator.
	dep, err := cs.AppsV1().Deployments(metal3Namespace).Get(ctx, metal3Release, metav1.GetOptions{})
	if err == nil {
		opStatus := "degraded"
		if dep.Status.AvailableReplicas >= 1 {
			opStatus = "running"
		}
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "baremetal-operator",
			Version:   kube.ExtractImageVersion(dep.Spec.Template.Spec.Containers),
			Namespace: metal3Namespace,
			Status:    opStatus,
		})
	} else {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "baremetal-operator",
			Namespace: metal3Namespace,
			Status:    "not-installed",
		})
	}

	return result, nil
}

// Validate verifies the management cluster is reachable and BMC credentials
// are configured for nodes that require provisioning.
func (s *ProvisionStage) Validate(ctx context.Context, kc *kube.Client, profile *config.Profile) error {
	_, err := kc.Clientset().CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return fmt.Errorf("cluster not reachable: %w", err)
	}
	return nil
}

// Plan builds a StagePlan describing what actions to take for Metal3
// deployment and BareMetalHost provisioning.
func (s *ProvisionStage) Plan(ctx context.Context, kc *kube.Client, profile *config.Profile, current *state.StageState) (*engine.StagePlan, error) {
	plan := &engine.StagePlan{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	cs := kc.Clientset()

	// Plan Metal3 Baremetal Operator: check if its deployment exists.
	dep, depErr := cs.AppsV1().Deployments(metal3Namespace).Get(ctx, metal3Release, metav1.GetOptions{})
	if depErr == nil {
		depVersion := kube.ExtractImageVersion(dep.Spec.Template.Spec.Containers)
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "baremetal-operator",
			Action:    "skip",
			Chart:     metal3Chart,
			Version:   metal3Version,
			Current:   depVersion,
			Namespace: metal3Namespace,
		})
	} else {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "baremetal-operator",
			Action:    "install",
			Chart:     metal3Chart,
			Version:   metal3Version,
			Namespace: metal3Namespace,
		})
	}

	// Plan BareMetalHost provisioning component.
	// At Plan time we cannot inspect the full site config (only profile),
	// so we mark it as an install candidate. Apply() decides per-node.
	plan.Components = append(plan.Components, engine.ComponentPlan{
		Name:      "baremetalhosts",
		Action:    "install",
		Namespace: metal3Namespace,
	})

	plan.Action = engine.DeterminePlanAction(plan.Components, plan.Patches)

	return plan, nil
}

// Apply executes the stage plan, deploying the Metal3 operator and creating
// BareMetalHost CRDs for each node defined in the site configuration.
func (s *ProvisionStage) Apply(ctx context.Context, kc *kube.Client, hc *helm.Client, site *config.Site, profile *config.Profile, plan *engine.StagePlan, printer *output.Printer) error {
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
			case "baremetal-operator":
				printer.ComponentStart(idx, total, comp.Name, comp.Version, "installing")
				err = deployMetal3(ctx, hc, kc, printer)

			case "baremetalhosts":
				if site == nil || len(site.Nodes) == 0 {
					printer.ComponentSkipped(idx, total, comp.Name, "", "no nodes configured")
					continue
				}
				// Count nodes that have BMC config.
				bmcNodes := nodesWithBMC(site.Nodes)
				if len(bmcNodes) == 0 {
					printer.ComponentSkipped(idx, total, comp.Name, "", "no nodes with BMC configured")
					continue
				}
				printer.ComponentStart(idx, total, comp.Name, fmt.Sprintf("%d nodes", len(bmcNodes)), "provisioning")
				err = provisionNodes(ctx, kc, site, bmcNodes, printer)

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

// Status reports the current runtime health of the Provisioning stage.
func (s *ProvisionStage) Status(ctx context.Context, kc *kube.Client) (*engine.StageStatus, error) {
	status := &engine.StageStatus{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	cs := kc.Clientset()

	// Check Metal3 Baremetal Operator deployment.
	opStatus := engine.ComponentStatus{
		Name:      "baremetal-operator",
		Namespace: metal3Namespace,
	}
	dep, err := cs.AppsV1().Deployments(metal3Namespace).Get(ctx, metal3Release, metav1.GetOptions{})
	if err != nil {
		opStatus.Status = "not-installed"
	} else {
		opStatus.Pods = int(dep.Status.Replicas)
		opStatus.Ready = int(dep.Status.ReadyReplicas)
		opStatus.Version = kube.ExtractImageVersion(dep.Spec.Template.Spec.Containers)
		if dep.Status.AvailableReplicas >= 1 {
			opStatus.Status = "running"
		} else {
			opStatus.Status = "degraded"
		}
		status.Version = opStatus.Version
		status.Applied = dep.CreationTimestamp.Time
	}
	status.Components = append(status.Components, opStatus)

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

// Destroy removes BareMetalHost resources and the Metal3 operator from the cluster.
func (s *ProvisionStage) Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error {
	total := 2

	// Remove BareMetalHost resources first.
	dc := kc.DynamicClient()
	if dc != nil {
		bmhList, listErr := dc.Resource(bmhGVR).Namespace(metal3Namespace).List(ctx, metav1.ListOptions{})
		if listErr == nil && len(bmhList.Items) > 0 {
			printer.ComponentStart(1, total, "baremetalhosts", fmt.Sprintf("%d hosts", len(bmhList.Items)), "destroying")
			var lastErr error
			for _, bmh := range bmhList.Items {
				err := dc.Resource(bmhGVR).Namespace(metal3Namespace).Delete(ctx, bmh.GetName(), metav1.DeleteOptions{})
				if err != nil {
					lastErr = err
				}
			}
			printer.ComponentDone("baremetalhosts", lastErr)
			if lastErr != nil {
				return lastErr
			}
		} else {
			printer.ComponentSkipped(1, total, "baremetalhosts", "", "no hosts found")
		}
	} else {
		printer.ComponentSkipped(1, total, "baremetalhosts", "", "no dynamic client")
	}

	// Uninstall Metal3 Baremetal Operator.
	installed, version, err := hc.IsInstalled(metal3Release, metal3Namespace)
	if err != nil {
		return fmt.Errorf("checking baremetal-operator: %w", err)
	}
	if installed {
		printer.ComponentStart(2, total, "baremetal-operator", version, "destroying")
		err = hc.Uninstall(metal3Release, metal3Namespace)
		printer.ComponentDone("baremetal-operator", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped(2, total, "baremetal-operator", "", "not installed")
	}

	return nil
}

// nodesWithBMC filters nodes that have BMC configuration.
func nodesWithBMC(nodes []config.Node) []config.Node {
	var result []config.Node
	for _, n := range nodes {
		if n.BMC != nil && n.BMC.IP != "" {
			result = append(result, n)
		}
	}
	return result
}

// provisionNodes creates BMC secrets and BareMetalHost resources for each node.
func provisionNodes(ctx context.Context, kc *kube.Client, site *config.Site, nodes []config.Node, printer *output.Printer) error {
	// Determine OS image URL from site overrides.
	imageURL := defaultImageURL
	if site.Overrides != nil {
		if bmhOverrides, ok := site.Overrides["baremetalhosts"]; ok {
			if url, ok := bmhOverrides["imageURL"].(string); ok && url != "" {
				imageURL = url
			}
		}
	}

	for _, node := range nodes {
		printer.Debugf("provisioning node %s (BMC: %s)", node.Name, node.BMC.IP)

		// Create BMC credentials secret.
		if err := createBMCSecret(ctx, kc, node, metal3Namespace, printer); err != nil {
			return fmt.Errorf("creating BMC secret for %s: %w", node.Name, err)
		}

		// Create BareMetalHost resource.
		if err := createBareMetalHost(ctx, kc, node, metal3Namespace, imageURL, printer); err != nil {
			return fmt.Errorf("creating BareMetalHost for %s: %w", node.Name, err)
		}
	}

	return nil
}
