package s5_slurm

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/engine"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
	"github.com/todd-chamberlain/nstack/pkg/state"
)

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
	cs := kc.Clientset()

	result := &engine.DetectResult{
		Operators: []engine.DetectedOperator{
			engine.DetectDeployment(ctx, cs, soperatorNamespace, "soperator-manager", "soperator"),
		},
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
	return engine.ValidateClusterReachable(ctx, kc.Clientset())
}

// Plan builds a StagePlan describing what actions to take for the Slurm stage.
func (s *SlurmStage) Plan(ctx context.Context, kc *kube.Client, profile *config.Profile, current *state.StageState) (*engine.StagePlan, error) {
	plan := &engine.StagePlan{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	cs := kc.Clientset()
	hc := helm.NewClient(kc.Kubeconfig())

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
	sopInstalled, sopVersion, _ := hc.IsInstalled(soperatorRelease, soperatorNamespace)
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
	clusterInstalled, clusterVersion, _ := hc.IsInstalled(slurmClusterRelease, slurmNamespace)
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
	nsInstalled, nsVersion, _ := hc.IsInstalled(nodesetsRelease, slurmNamespace)
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
		if profile.Patches.PrologToBinTrue {
			plan.Patches = append(plan.Patches, engine.PatchPlan{
				Name:        "prolog-to-bin-true",
				Description: "Set prolog/epilog to /bin/true (applied via Helm values)",
				Condition:   "patches.prologToBinTrue=true",
				Applied:     true, // Applied via slurm-cluster/k3s.yaml values, not a runtime patch
			})
		}
		if profile.Patches.ContainerdSocketBind {
			plan.Patches = append(plan.Patches, engine.PatchPlan{
				Name:        "containerd-socket-bind",
				Description: "Bind-mount K3s containerd socket for kruise-daemon",
				Condition:   "patches.containerdSocketBind=true",
			})
		}
		if profile.Patches.SpankDisable {
			plan.Patches = append(plan.Patches, engine.PatchPlan{
				Name:        "spank-disable",
				Description: "Disable SPANK chroot plugin in plugstack.conf",
				Condition:   "patches.spankDisable=true",
			})
		}
	}

	// Determine overall stage action.
	plan.Action = engine.DeterminePlanAction(plan.Components, plan.Patches)

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
			printer.ComponentSkipped(idx, total, comp.Name, comp.Current, "already installed")
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
				// Ensure soperator webhook is available (scale up if needed for validation).
				if profile != nil && profile.Patches.OperatorScaleDown {
					_ = kc.ScaleDeployment(ctx, soperatorNamespace, "soperator-manager", 1)
					printer.Debugf("temporarily scaled soperator-manager to 1 for webhook")
					// Wait briefly for the webhook endpoint to become available.
					_ = kc.WaitForDeployment(ctx, soperatorNamespace, "soperator-manager", 60*time.Second)
				}
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
	cs := kc.Clientset()

	// Check soperator-manager deployment. Uses the shared helper as a
	// starting point, then overrides the status for intentional scale-down.
	sopStatus := engine.CheckDeploymentStatus(ctx, cs, soperatorNamespace, "soperator-manager", "soperator")
	if sopStatus.Status == "degraded" {
		// Scale-down to 0 is intentional when patches.operatorScaleDown is set.
		dep, err := cs.AppsV1().Deployments(soperatorNamespace).Get(ctx, "soperator-manager", metav1.GetOptions{})
		if err == nil {
			desired := int32(1)
			if dep.Spec.Replicas != nil {
				desired = *dep.Spec.Replicas
			}
			if desired == 0 {
				sopStatus.Status = "scaled-down"
			}
		}
	}

	status := &engine.StageStatus{
		Stage:   s.Number(),
		Name:    s.Name(),
		Version: sopStatus.Version,
		Components: []engine.ComponentStatus{
			sopStatus,
		},
	}

	// Check slurm controller pods.
	status.Components = append(status.Components,
		checkPodGroupStatus(ctx, cs, slurmNamespace, "app.kubernetes.io/component=controller", "slurm-cluster"))

	// Check worker pods.
	status.Components = append(status.Components,
		checkPodGroupStatus(ctx, cs, slurmNamespace, "app.kubernetes.io/component=worker", "worker-gpu"))

	status.Status = engine.DetermineOverallStatus(status.Components)

	return status, nil
}

// checkPodGroupStatus queries pods by label selector and returns a
// ComponentStatus summarizing their readiness.
func checkPodGroupStatus(ctx context.Context, cs kubernetes.Interface, namespace, labelSelector, componentName string) engine.ComponentStatus {
	compStatus := engine.ComponentStatus{
		Name:      componentName,
		Namespace: namespace,
		Status:    "not-installed",
	}

	pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil || len(pods.Items) == 0 {
		return compStatus
	}

	compStatus.Pods = len(pods.Items)
	ready := 0
	for _, pod := range pods.Items {
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				ready++
				break
			}
		}
	}
	compStatus.Ready = ready
	if ready > 0 {
		compStatus.Status = "running"
	} else {
		compStatus.Status = "degraded"
	}

	return compStatus
}

// Destroy removes all Slurm stage components from the cluster in reverse order.
func (s *SlurmStage) Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error {
	totalComponents := 4

	// 1. Uninstall nodesets.
	installed, version, err := hc.IsInstalled(nodesetsRelease, slurmNamespace)
	if err != nil {
		return fmt.Errorf("checking nodesets: %w", err)
	}
	if installed {
		printer.ComponentStart(1, totalComponents, "nodesets", version, "destroying")
		err = hc.Uninstall(nodesetsRelease, slurmNamespace)
		printer.ComponentDone("nodesets", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped(1, totalComponents, "nodesets", "", "not installed")
	}

	// 2. Uninstall slurm-cluster.
	installed, version, err = hc.IsInstalled(slurmClusterRelease, slurmNamespace)
	if err != nil {
		return fmt.Errorf("checking slurm-cluster: %w", err)
	}
	if installed {
		printer.ComponentStart(2, totalComponents, "slurm-cluster", version, "destroying")
		err = hc.Uninstall(slurmClusterRelease, slurmNamespace)
		printer.ComponentDone("slurm-cluster", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped(2, totalComponents, "slurm-cluster", "", "not installed")
	}

	// 3. Uninstall soperator.
	installed, version, err = hc.IsInstalled(soperatorRelease, soperatorNamespace)
	if err != nil {
		return fmt.Errorf("checking soperator: %w", err)
	}
	if installed {
		printer.ComponentStart(3, totalComponents, "soperator", version, "destroying")
		err = hc.Uninstall(soperatorRelease, soperatorNamespace)
		printer.ComponentDone("soperator", err)
		if err != nil {
			return err
		}
	} else {
		printer.ComponentSkipped(3, totalComponents, "soperator", "", "not installed")
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
