package s2_kubernetes

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/engine"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
	"github.com/todd-chamberlain/nstack/pkg/state"
)

// KubernetesStage implements the Stage interface for bootstrapping or
// validating a Kubernetes cluster. It supports K3s, kubeadm, and managed
// cloud distributions (EKS, GKE, AKS, Nebius).
//
// In v0.3 this is a "guided bootstrap" — it detects cluster state and
// provides installation instructions rather than running SSH commands.
type KubernetesStage struct{}

// New returns a new KubernetesStage instance.
func New() *KubernetesStage { return &KubernetesStage{} }

func (s *KubernetesStage) Number() int         { return 2 }
func (s *KubernetesStage) Name() string        { return "Kubernetes" }
func (s *KubernetesStage) Dependencies() []int { return []int{1} }

// Detect checks if a Kubernetes API server is reachable via the kube client.
// If kc is nil (no kubeconfig), detection reports no cluster found.
func (s *KubernetesStage) Detect(ctx context.Context, kc *kube.Client) (*engine.DetectResult, error) {
	result := &engine.DetectResult{}

	if kc == nil {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:   "kubernetes",
			Status: "not-installed",
		})
		return result, nil
	}

	version, err := kc.Clientset().Discovery().ServerVersion()
	if err != nil {
		result.Operators = append(result.Operators, engine.DetectedOperator{
			Name:   "kubernetes",
			Status: "not-installed",
		})
		return result, nil
	}

	result.Operators = append(result.Operators, engine.DetectedOperator{
		Name:    "kubernetes",
		Version: version.GitVersion,
		Status:  "running",
	})
	return result, nil
}

// Validate checks that either the cluster is reachable via kubeconfig, or
// that nodes are defined in the site config for bootstrap.
func (s *KubernetesStage) Validate(ctx context.Context, kc *kube.Client, profile *config.Profile) error {
	// If we have a working kube client, verify it can reach the API.
	if kc != nil {
		_, err := kc.Clientset().CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
		if err == nil {
			return nil
		}
		// API unreachable — fall through to check if bootstrap is possible.
	}

	// No cluster reachable — check if profile provides enough info to bootstrap.
	if profile == nil {
		return fmt.Errorf("no cluster reachable and no profile provided for bootstrap")
	}

	dist := profile.Kubernetes.Distribution
	if dist == "" {
		return fmt.Errorf("no cluster reachable and no Kubernetes distribution configured in profile")
	}

	return nil
}

// Plan determines whether to skip (cluster exists) or install (bootstrap needed).
func (s *KubernetesStage) Plan(ctx context.Context, kc *kube.Client, profile *config.Profile, current *state.StageState) (*engine.StagePlan, error) {
	plan := &engine.StagePlan{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	// Determine distribution from profile.
	dist := "unknown"
	if profile != nil && profile.Kubernetes.Distribution != "" {
		dist = profile.Kubernetes.Distribution
	}

	// Check if cluster is already reachable.
	clusterReachable := false
	clusterVersion := ""
	if kc != nil {
		version, err := kc.Clientset().Discovery().ServerVersion()
		if err == nil {
			clusterReachable = true
			clusterVersion = version.GitVersion
		}
	}

	if clusterReachable {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:    "kubernetes",
			Action:  "skip",
			Current: clusterVersion,
		})
	} else {
		plan.Components = append(plan.Components, engine.ComponentPlan{
			Name:   "kubernetes",
			Action: "install",
		})
	}

	// Add distribution-specific component.
	plan.Components = append(plan.Components, engine.ComponentPlan{
		Name: dist + "-bootstrap",
		Action: func() string {
			if clusterReachable {
				return "skip"
			}
			return "install"
		}(),
	})

	plan.Action = engine.DeterminePlanAction(plan.Components, plan.Patches)

	return plan, nil
}

// Apply bootstraps K8s based on the profile distribution, or validates
// an existing cluster.
func (s *KubernetesStage) Apply(ctx context.Context, kc *kube.Client, hc *helm.Client, site *config.Site, profile *config.Profile, plan *engine.StagePlan, printer *output.Printer) error {
	return bootstrapCluster(ctx, kc, site, profile, printer)
}

// Status reports the cluster health: API server reachability and node readiness.
func (s *KubernetesStage) Status(ctx context.Context, kc *kube.Client) (*engine.StageStatus, error) {
	status := &engine.StageStatus{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	if kc == nil {
		status.Status = "not-installed"
		status.Components = append(status.Components, engine.ComponentStatus{
			Name:   "kubernetes",
			Status: "not-installed",
		})
		return status, nil
	}

	// Check API server.
	version, err := kc.Clientset().Discovery().ServerVersion()
	if err != nil {
		status.Status = "not-installed"
		status.Error = fmt.Sprintf("API server unreachable: %v", err)
		status.Components = append(status.Components, engine.ComponentStatus{
			Name:   "kubernetes",
			Status: "not-installed",
		})
		return status, nil
	}

	status.Version = version.GitVersion

	// Check nodes.
	nodes, err := kc.Clientset().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		status.Status = "degraded"
		status.Error = fmt.Sprintf("cannot list nodes: %v", err)
		status.Components = append(status.Components, engine.ComponentStatus{
			Name:    "kubernetes",
			Status:  "degraded",
			Version: version.GitVersion,
		})
		return status, nil
	}

	totalNodes := len(nodes.Items)
	readyNodes := 0
	for _, node := range nodes.Items {
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				readyNodes++
			}
		}
	}

	nodeStatus := engine.ComponentStatus{
		Name:    "kubernetes",
		Status:  "running",
		Version: version.GitVersion,
		Pods:    totalNodes,
		Ready:   readyNodes,
	}

	if totalNodes == 0 {
		nodeStatus.Status = "degraded"
	} else if readyNodes < totalNodes {
		nodeStatus.Status = "degraded"
	}

	status.Components = append(status.Components, nodeStatus)

	// Set creation timestamp from the oldest node as a proxy for "applied" time.
	if totalNodes > 0 {
		oldest := nodes.Items[0].CreationTimestamp.Time
		for _, node := range nodes.Items[1:] {
			if node.CreationTimestamp.Time.Before(oldest) {
				oldest = node.CreationTimestamp.Time
			}
		}
		status.Applied = oldest
	} else {
		status.Applied = time.Now()
	}

	// Overall status.
	switch {
	case totalNodes == 0:
		status.Status = "degraded"
	case readyNodes == totalNodes:
		status.Status = "deployed"
	default:
		status.Status = "degraded"
	}

	return status, nil
}

// Destroy drains and cordons nodes. It does not delete the cluster itself,
// as that is too dangerous for a default operation.
func (s *KubernetesStage) Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error {
	if kc == nil {
		printer.Infof("        No cluster connection — nothing to destroy")
		return nil
	}

	nodes, err := kc.Clientset().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("cannot list nodes: %w", err)
	}

	total := len(nodes.Items)
	if total == 0 {
		printer.Infof("        No nodes found — nothing to destroy")
		return nil
	}

	for i, node := range nodes.Items {
		idx := i + 1
		printer.ComponentStart(idx, total, node.Name, "", "cordoning")

		// Cordon the node (mark unschedulable).
		node.Spec.Unschedulable = true
		_, err := kc.Clientset().CoreV1().Nodes().Update(ctx, &node, metav1.UpdateOptions{})
		printer.ComponentDone(node.Name, err)
		if err != nil {
			return fmt.Errorf("failed to cordon node %s: %w", node.Name, err)
		}
	}

	printer.Infof("        Nodes cordoned. To fully remove nodes, run:")
	printer.Infof("          kubectl drain <node> --ignore-daemonsets --delete-emptydir-data")
	printer.Infof("          kubectl delete node <node>")

	return nil
}
