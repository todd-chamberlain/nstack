package s5_slurm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
type SlurmStage struct {
	cluster config.ClusterConfig
}

// New returns a new SlurmStage instance with default cluster identity.
func New() *SlurmStage {
	return &SlurmStage{cluster: config.ClusterConfig{Name: "slurm1", Namespace: "slurm"}}
}

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
	pods, err := cs.CoreV1().Pods(s.cluster.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=slurm-cluster",
	})
	if err == nil && len(pods.Items) > 0 {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "slurm-cluster",
			Namespace: s.cluster.Namespace,
			Status:    "running",
		})
	} else {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:      "slurm-cluster",
			Namespace: s.cluster.Namespace,
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
	_, pvcErr := cs.CoreV1().PersistentVolumeClaims(s.cluster.Namespace).Get(ctx, "jail-pvc", metav1.GetOptions{})
	if pvcErr == nil {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "storage",
			Action:    "skip",
			Namespace: s.cluster.Namespace,
		})
	} else {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:      "storage",
			Action:    "install",
			Namespace: s.cluster.Namespace,
		})
	}

	// Plan soperator, slurm-cluster, and nodesets.
	plan.Components = append(plan.Components,
		engine.PlanHelmComponent(hc, "soperator", "", soperatorVersion, soperatorNamespace, soperatorRelease),
		engine.PlanHelmComponent(hc, "slurm-cluster", "", soperatorVersion, s.cluster.Namespace, slurmClusterRelease),
		engine.PlanHelmComponent(hc, "nodesets", "", soperatorVersion, s.cluster.Namespace, nodesetsRelease),
	)

	// Plan patches — only the minimal runtime patches still needed.
	if profile != nil {
		plan.Patches = append(plan.Patches, engine.PatchPlan{
			Name:        "jail-populated-marker",
			Description: "Ensure .populated marker exists in jail PVC",
			Condition:   "always",
		})
		if profile.Patches.ContainerdSocketBind {
			plan.Patches = append(plan.Patches, engine.PatchPlan{
				Name:        "containerd-socket-bind",
				Description: "Bind-mount K3s containerd socket for kruise-daemon",
				Condition:   "patches.containerdSocketBind=true",
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
	// Resolve cluster identity from site config (updates struct for subsequent methods).
	s.cluster = config.ResolveCluster(site)

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
		gitTag := config.ResolveVersion(site, "soperator", soperatorGitTag)
		repoDir, err = cloneSoperatorRepo(ctx, gitTag, printer)
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
				err = createStorage(ctx, kc, profile, s.cluster, printer)
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
				// Wait for soperator webhook to be ready (needs cert-manager cert).
				printer.Debugf("waiting for soperator webhook...")
				for retry := 0; retry < 30; retry++ {
					select {
					case <-ctx.Done():
						return fmt.Errorf("context cancelled waiting for soperator webhook: %w", ctx.Err())
					default:
					}
					eps, epErr := kc.Clientset().CoreV1().Endpoints(soperatorNamespace).Get(ctx, "soperator-webhook-service", metav1.GetOptions{})
					if epErr == nil && len(eps.Subsets) > 0 && len(eps.Subsets[0].Addresses) > 0 {
						printer.Debugf("webhook ready")
						break
					}
					time.Sleep(5 * time.Second)
				}
				err = installSlurmCluster(ctx, hc, site, profile, repoDir, s.cluster, printer)
			case "nodesets":
				err = installNodeSets(ctx, hc, site, profile, repoDir, s.cluster, printer)
			default:
				err = fmt.Errorf("unknown component: %s", comp.Name)
			}

			printer.ComponentDone(comp.Name, err)
			if err != nil {
				return err
			}
		}
	}

	// Wait for the operator to fully reconcile the SlurmCluster.
	// The operator creates ConfigMaps (SSH keys, munge keys, slurm configs),
	// StatefulSets, and other resources. We must wait for reconciliation to
	// complete before scaling the operator down, otherwise pods will fail
	// with missing volume mounts.
	//
	// Signal that reconciliation is complete: the SSH root keys ConfigMap
	// exists (it's one of the last resources the operator creates).
	if len(plan.Patches) > 0 {
		printer.Infof("  Waiting for Slurm cluster reconciliation...")
		cs := kc.Clientset()
		sshKeyCM := s.cluster.Name + "-ssh-root-keys"

		for i := 0; i < 120; i++ {
			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled waiting for cluster reconciliation: %w", ctx.Err())
			default:
			}

			// Check if key ConfigMaps exist (signals operator finished).
			_, cmErr := cs.CoreV1().ConfigMaps(s.cluster.Namespace).Get(ctx, sshKeyCM, metav1.GetOptions{})
			if cmErr == nil {
				printer.Debugf("cluster reconciled: SSH keys ConfigMap %s exists", sshKeyCM)

				// Clear stale node_state from the controller spool PVC.
				// Previous deploys may have saved state with different CPU topology
				// which causes INVALID_REG on the next deploy.
				execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				clearCmd := exec.CommandContext(execCtx, "kubectl", "exec", "-n", s.cluster.Namespace,
					"controller-0", "-c", "slurmctld", "--",
					"bash", "-c", "rm -f /var/spool/slurmctld/node_state /var/spool/slurmctld/node_state.old /var/spool/slurmctld/job_state /var/spool/slurmctld/job_state.old 2>/dev/null; pkill -HUP slurmctld 2>/dev/null || true")
				if out, err := clearCmd.CombinedOutput(); err != nil {
					printer.Debugf("spool cleanup (non-fatal): %s: %v", string(out), err)
				} else {
					printer.Debugf("cleared stale slurmctld state")
				}

				break
			}
			if i%12 == 0 {
				printer.Debugf("waiting for operator to finish reconciliation...")
			}
			time.Sleep(5 * time.Second)
		}
	}

	// Apply K3s patches after operator has reconciled.
	if len(plan.Patches) > 0 {
		printer.Infof("  Applying K3s patches...")
		if err := applyK3sPatches(ctx, kc, profile, s.cluster, printer); err != nil {
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
		// Scale-down to 0 may be intentional (e.g., manual operator pause).
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
		checkPodGroupStatus(ctx, cs, s.cluster.Namespace, "app.kubernetes.io/component=controller", "slurm-cluster"))

	// Check worker pods.
	status.Components = append(status.Components,
		checkPodGroupStatus(ctx, cs, s.cluster.Namespace, "app.kubernetes.io/component=worker", "worker-gpu"))

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
	total := 4

	// Uninstall Helm releases in reverse dependency order.
	releases := []struct {
		name, release, namespace string
	}{
		{"nodesets", nodesetsRelease, s.cluster.Namespace},
		{"slurm-cluster", slurmClusterRelease, s.cluster.Namespace},
		{"soperator", soperatorRelease, soperatorNamespace},
	}

	for i, r := range releases {
		if err := engine.DestroyHelmRelease(hc, r.name, r.release, r.namespace, i+1, total, printer); err != nil {
			return err
		}
	}

	// Remove storage (PVCs + PVs).
	printer.ComponentStart(4, total, "storage", "", "destroying")
	err := destroyStorage(ctx, kc, s.cluster, printer)
	printer.ComponentDone("storage", err)
	return err
}
