package s5_slurm

import (
	"context"
	"fmt"
	"os"
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

// SlurmStage implements the Stage interface for deploying the soperator
// controller, Slurm cluster, NodeSets, and K3s-specific patches.
type SlurmStage struct{}

// New returns a new SlurmStage instance.
func New() *SlurmStage { return &SlurmStage{} }

func (s *SlurmStage) Number() int         { return 5 }
func (s *SlurmStage) Name() string        { return "Slurm" }
func (s *SlurmStage) Dependencies() []int { return []int{4} }

// Detect checks for existing soperator and slurm cluster deployments.
func (s *SlurmStage) Detect(ctx context.Context, kc *kube.Client) (*engine.DetectResult, error) {
	result := &engine.DetectResult{}
	cs := kc.Clientset()

	// Check soperator-manager deployment.
	sopDep, err := cs.AppsV1().Deployments(soperatorNamespace).Get(ctx, "soperator-manager", metav1.GetOptions{})
	if err == nil {
		opStatus := "degraded"
		if sopDep.Status.AvailableReplicas >= 1 {
			opStatus = "running"
		}
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "soperator",
			Version:   extractVersion(sopDep.Spec.Template.Spec.Containers),
			Namespace: soperatorNamespace,
			Status:    opStatus,
		})
	} else {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "soperator",
			Namespace: soperatorNamespace,
			Status:    "not-installed",
		})
	}

	// Check for slurm-cluster Helm release by looking for controller pod.
	pods, err := cs.CoreV1().Pods(slurmNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=slurm-cluster",
	})
	if err == nil && len(pods.Items) > 0 {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "slurm-cluster",
			Namespace: slurmNamespace,
			Status:    "running",
		})
	} else {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "slurm-cluster",
			Namespace: slurmNamespace,
			Status:    "not-installed",
		})
	}

	return result, nil
}

// Validate verifies the cluster is reachable and the GPU stage is deployed.
func (s *SlurmStage) Validate(ctx context.Context, kc *kube.Client, profile *config.Profile) error {
	_, err := kc.Clientset().CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return fmt.Errorf("cluster not reachable: %w", err)
	}
	return nil
}

// Plan builds a StagePlan describing what actions to take for the Slurm stage.
func (s *SlurmStage) Plan(ctx context.Context, kc *kube.Client, profile *config.Profile, current *state.StageState) (*engine.StagePlan, error) {
	plan := &engine.StagePlan{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	cs := kc.Clientset()
	hc := helm.NewClient("", slurmNamespace)

	// Plan storage: check if PVCs exist.
	_, pvcErr := cs.CoreV1().PersistentVolumeClaims(slurmNamespace).Get(ctx, "jail-pvc", metav1.GetOptions{})
	if pvcErr == nil {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "storage",
			Action:    "skip",
			Namespace: slurmNamespace,
		})
	} else {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "storage",
			Action:    "install",
			Namespace: slurmNamespace,
		})
	}

	// Plan soperator CRDs + operator.
	hc.SetNamespace(soperatorNamespace)
	sopInstalled, sopVersion, _ := hc.IsInstalled(soperatorRelease)
	if sopInstalled {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "soperator",
			Action:    "skip",
			Version:   soperatorVersion,
			Current:   sopVersion,
			Namespace: soperatorNamespace,
		})
	} else {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "soperator",
			Action:    "install",
			Version:   soperatorVersion,
			Namespace: soperatorNamespace,
		})
	}

	// Plan slurm-cluster.
	hc.SetNamespace(slurmNamespace)
	clusterInstalled, clusterVersion, _ := hc.IsInstalled(slurmClusterRelease)
	if clusterInstalled {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "slurm-cluster",
			Action:    "skip",
			Version:   soperatorVersion,
			Current:   clusterVersion,
			Namespace: slurmNamespace,
		})
	} else {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "slurm-cluster",
			Action:    "install",
			Version:   soperatorVersion,
			Namespace: slurmNamespace,
		})
	}

	// Plan nodesets.
	nsInstalled, nsVersion, _ := hc.IsInstalled(nodesetsRelease)
	if nsInstalled {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "nodesets",
			Action:    "skip",
			Version:   soperatorVersion,
			Current:   nsVersion,
			Namespace: slurmNamespace,
		})
	} else {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "nodesets",
			Action:    "install",
			Version:   soperatorVersion,
			Namespace: slurmNamespace,
		})
	}

	// Plan patches.
	if profile != nil {
		if profile.Patches.CgroupEntrypoint {
			plan.Patches = append(plan.Patches, engine.PatchPlan{
				Name:        "cgroup-entrypoint",
				Description: "Create worker-entrypoint-fix ConfigMap for cgroup v2",
				Condition:   "patches.cgroupEntrypoint=true",
			})
		}
		if profile.Patches.BusyboxRetag {
			plan.Patches = append(plan.Patches, engine.PatchPlan{
				Name:        "busybox-retag",
				Description: "Retag busybox:latest to Nebius registry path",
				Condition:   "patches.busyboxRetag=true",
			})
		}
		if profile.Patches.OperatorScaleDown {
			plan.Patches = append(plan.Patches, engine.PatchPlan{
				Name:        "operator-scale-down",
				Description: "Scale soperator-manager to 0 replicas",
				Condition:   "patches.operatorScaleDown=true",
			})
		}
		if profile.Patches.WorkerInitSkip {
			plan.Patches = append(plan.Patches, engine.PatchPlan{
				Name:        "worker-init-skip",
				Description: "Skip worker-gpu init container for topology",
				Condition:   "patches.workerInitSkip=true",
			})
		}
		if profile.Patches.ProcMountDefault {
			plan.Patches = append(plan.Patches, engine.PatchPlan{
				Name:        "proc-mount-default",
				Description: "Set NodeSet procMount to Default for K3s",
				Condition:   "patches.procMountDefault=true",
			})
		}
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
	if len(plan.Patches) > 0 {
		allSkip = false
		hasInstall = true
	}
	if allSkip {
		plan.Action = "skip"
	} else if hasInstall {
		plan.Action = "install"
	}

	return plan, nil
}

// Apply executes the stage plan, installing soperator, slurm-cluster, nodesets,
// and applying K3s-specific patches as needed.
func (s *SlurmStage) Apply(ctx context.Context, kc *kube.Client, hc *helm.Client, site *config.Site, profile *config.Profile, plan *engine.StagePlan, printer *output.Printer) error {
	// Clone soperator repo upfront (needed for CRDs, operator chart, cluster chart, nodesets).
	var repoDir string
	needsRepo := false
	for _, comp := range plan.Components {
		if comp.Action == "install" && comp.Name != "storage" {
			needsRepo = true
			break
		}
	}

	if needsRepo {
		var err error
		repoDir, err = cloneSoperatorRepo(ctx, printer)
		if err != nil {
			return fmt.Errorf("cloning soperator repo: %w", err)
		}
		defer os.RemoveAll(repoDir)
		printer.Debugf("soperator repo cloned to %s", repoDir)
	}

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
			case "storage":
				err = createStorage(ctx, kc, profile, printer)
			case "soperator":
				// Install CRDs first, then the operator.
				err = installSoperatorCRDs(ctx, kc, repoDir, printer)
				if err == nil {
					var overrides map[string]interface{}
					if site != nil && site.Overrides != nil {
						overrides = site.Overrides["soperator"]
					}
					err = installSoperator(ctx, hc, kc, profile, repoDir, overrides, printer)
				}
			case "slurm-cluster":
				err = installSlurmCluster(ctx, hc, site, profile, repoDir, printer)
			case "nodesets":
				err = installNodeSets(ctx, hc, site, profile, repoDir, printer)
			default:
				err = fmt.Errorf("unknown component: %s", comp.Name)
			}

			printer.ComponentDone(comp.Name, err)
			if err != nil {
				return err
			}
		}
	}

	// Apply K3s patches after all components are installed.
	if len(plan.Patches) > 0 {
		printer.Infof("  Applying K3s patches...")
		if err := applyK3sPatches(ctx, kc, profile, printer); err != nil {
			return fmt.Errorf("applying K3s patches: %w", err)
		}
	}

	return nil
}

// Status reports the current runtime health of the Slurm stage.
func (s *SlurmStage) Status(ctx context.Context, kc *kube.Client) (*engine.StageStatus, error) {
	status := &engine.StageStatus{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	cs := kc.Clientset()

	// Check soperator-manager deployment.
	sopStatus := engine.ComponentStatus{
		Name:      "soperator",
		Namespace: soperatorNamespace,
	}
	sopDep, err := cs.AppsV1().Deployments(soperatorNamespace).Get(ctx, "soperator-manager", metav1.GetOptions{})
	if err != nil {
		sopStatus.Status = "not-installed"
	} else {
		sopStatus.Pods = int(sopDep.Status.Replicas)
		sopStatus.Ready = int(sopDep.Status.ReadyReplicas)
		sopStatus.Version = extractVersion(sopDep.Spec.Template.Spec.Containers)
		if sopDep.Status.AvailableReplicas >= 1 {
			sopStatus.Status = "running"
		} else {
			// Scale-down to 0 is intentional when patches.operatorScaleDown is set.
			desired := int32(1)
			if sopDep.Spec.Replicas != nil {
				desired = *sopDep.Spec.Replicas
			}
			if desired == 0 {
				sopStatus.Status = "scaled-down"
			} else {
				sopStatus.Status = "degraded"
			}
		}
		status.Version = sopStatus.Version
		status.Applied = sopDep.CreationTimestamp.Time
	}
	status.Components = append(status.Components, sopStatus)

	// Check slurm controller pods.
	ctrlStatus := engine.ComponentStatus{
		Name:      "slurm-cluster",
		Namespace: slurmNamespace,
	}
	ctrlPods, err := cs.CoreV1().Pods(slurmNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/component=controller",
	})
	if err == nil && len(ctrlPods.Items) > 0 {
		ctrlStatus.Pods = len(ctrlPods.Items)
		ready := 0
		for _, pod := range ctrlPods.Items {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					ready++
					break
				}
			}
		}
		ctrlStatus.Ready = ready
		if ready > 0 {
			ctrlStatus.Status = "running"
		} else {
			ctrlStatus.Status = "degraded"
		}
	} else {
		ctrlStatus.Status = "not-installed"
	}
	status.Components = append(status.Components, ctrlStatus)

	// Check worker pods.
	workerStatus := engine.ComponentStatus{
		Name:      "worker-gpu",
		Namespace: slurmNamespace,
	}
	workerPods, err := cs.CoreV1().Pods(slurmNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/component=worker",
	})
	if err == nil && len(workerPods.Items) > 0 {
		workerStatus.Pods = len(workerPods.Items)
		ready := 0
		for _, pod := range workerPods.Items {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					ready++
					break
				}
			}
		}
		workerStatus.Ready = ready
		if ready > 0 {
			workerStatus.Status = "running"
		} else {
			workerStatus.Status = "degraded"
		}
	} else {
		workerStatus.Status = "not-installed"
	}
	status.Components = append(status.Components, workerStatus)

	// Determine overall status.
	allRunning := true
	anyNotInstalled := false
	for _, c := range status.Components {
		if c.Status != "running" && c.Status != "scaled-down" {
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

// Destroy removes all Slurm stage components from the cluster in reverse order.
func (s *SlurmStage) Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error {
	totalComponents := 4

	// 1. Uninstall nodesets.
	hc.SetNamespace(slurmNamespace)
	installed, version, err := hc.IsInstalled(nodesetsRelease)
	if err != nil {
		return fmt.Errorf("checking nodesets: %w", err)
	}
	if installed {
		printer.ComponentStart(1, totalComponents, "nodesets", version, "destroying")
		err = hc.Uninstall(nodesetsRelease)
		printer.ComponentDone("nodesets", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped("nodesets", "", "not installed")
	}

	// 2. Uninstall slurm-cluster.
	installed, version, err = hc.IsInstalled(slurmClusterRelease)
	if err != nil {
		return fmt.Errorf("checking slurm-cluster: %w", err)
	}
	if installed {
		printer.ComponentStart(2, totalComponents, "slurm-cluster", version, "destroying")
		err = hc.Uninstall(slurmClusterRelease)
		printer.ComponentDone("slurm-cluster", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped("slurm-cluster", "", "not installed")
	}

	// 3. Uninstall soperator.
	hc.SetNamespace(soperatorNamespace)
	installed, version, err = hc.IsInstalled(soperatorRelease)
	if err != nil {
		return fmt.Errorf("checking soperator: %w", err)
	}
	if installed {
		printer.ComponentStart(3, totalComponents, "soperator", version, "destroying")
		err = hc.Uninstall(soperatorRelease)
		printer.ComponentDone("soperator", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped("soperator", "", "not installed")
	}

	// 4. Remove storage (PVCs + PVs).
	printer.ComponentStart(4, totalComponents, "storage", "", "destroying")
	err = destroyStorage(ctx, kc, printer)
	printer.ComponentDone("storage", err)
	if err != nil {
		return err
	}

	return nil
}
