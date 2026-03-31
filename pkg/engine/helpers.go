package engine

import (
	"context"
	"fmt"
	"log"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

// DetermineOverallStatus derives a single status string from a set of
// component statuses. It is used by every stage's Status() method.
func DetermineOverallStatus(components []ComponentStatus) string {
	allRunning := true
	anyDegraded := false
	for _, c := range components {
		switch c.Status {
		case "running", "scaled-down":
			// ok
		case "degraded", "failed":
			anyDegraded = true
			allRunning = false
		default:
			allRunning = false
		}
	}
	switch {
	case allRunning:
		return "deployed"
	case anyDegraded:
		return "degraded"
	default:
		return "not-installed"
	}
}

// DetectDeployment probes a single Deployment and returns a DetectedOperator
// describing its presence and health. Used by stage Detect() methods.
func DetectDeployment(ctx context.Context, cs kubernetes.Interface, namespace, deploymentName, operatorName string) DetectedOperator {
	dep, err := cs.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return DetectedOperator{Name: operatorName, Namespace: namespace, Status: "not-installed"}
	}
	status := "degraded"
	if dep.Status.AvailableReplicas >= 1 {
		status = "running"
	}
	return DetectedOperator{
		Name:      operatorName,
		Version:   kube.ExtractImageVersion(dep.Spec.Template.Spec.Containers),
		Namespace: namespace,
		Status:    status,
	}
}

// CheckDeploymentStatus queries a Deployment and returns its ComponentStatus.
// Used by stage Status() methods for Deployment-based components.
func CheckDeploymentStatus(ctx context.Context, cs kubernetes.Interface, namespace, deploymentName, componentName string) ComponentStatus {
	status := ComponentStatus{Name: componentName, Namespace: namespace, Status: "not-installed"}
	dep, err := cs.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return status
	}
	status.Version = kube.ExtractImageVersion(dep.Spec.Template.Spec.Containers)
	status.Pods = int(dep.Status.Replicas)
	status.Ready = int(dep.Status.ReadyReplicas)
	if dep.Status.AvailableReplicas >= 1 {
		status.Status = "running"
	} else {
		status.Status = "degraded"
	}
	return status
}

// CheckStatefulSetStatus queries a StatefulSet and returns its ComponentStatus.
// Used by stage Status() methods for StatefulSet-based components (e.g.
// Prometheus, Alertmanager).
func CheckStatefulSetStatus(ctx context.Context, cs kubernetes.Interface, namespace, stsName, componentName string) ComponentStatus {
	status := ComponentStatus{Name: componentName, Namespace: namespace, Status: "not-installed"}
	sts, err := cs.AppsV1().StatefulSets(namespace).Get(ctx, stsName, metav1.GetOptions{})
	if err != nil {
		return status
	}
	status.Version = kube.ExtractImageVersion(sts.Spec.Template.Spec.Containers)
	status.Pods = int(sts.Status.Replicas)
	status.Ready = int(sts.Status.ReadyReplicas)
	if sts.Status.ReadyReplicas >= 1 {
		status.Status = "running"
	} else {
		status.Status = "degraded"
	}
	return status
}

// ValidateClusterReachable performs a lightweight check to confirm the
// Kubernetes API server is responding. Used by stage Validate() methods.
func ValidateClusterReachable(ctx context.Context, cs kubernetes.Interface) error {
	_, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return fmt.Errorf("cluster not reachable: %w", err)
	}
	return nil
}

// PlanDeploymentComponent checks whether a Deployment exists and returns a
// ComponentPlan with action "skip" (if deployed) or "install" (if not).
// This eliminates the repeated Get-then-branch pattern across stage Plan() methods.
func PlanDeploymentComponent(ctx context.Context, cs kubernetes.Interface, name, chart, version, namespace, deploymentName string) ComponentPlan {
	dep, err := cs.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return ComponentPlan{
			Name:      name,
			Action:    "install",
			Chart:     chart,
			Version:   version,
			Namespace: namespace,
		}
	}
	return ComponentPlan{
		Name:      name,
		Action:    "skip",
		Chart:     chart,
		Version:   version,
		Current:   kube.ExtractImageVersion(dep.Spec.Template.Spec.Containers),
		Namespace: namespace,
	}
}

// PlanHelmComponent checks whether a Helm release exists and returns a
// ComponentPlan with action "skip" (if installed) or "install" (if not).
// Use this for components managed via Helm where the deployment name differs
// from the release name (e.g., monitoring stack, soperator).
func PlanHelmComponent(hc *helm.Client, name, chart, targetVersion, namespace, releaseName string) ComponentPlan {
	installed, currentVersion, err := hc.IsInstalled(releaseName, namespace)
	if err != nil {
		log.Printf("warning: checking helm release %q in %s: %v", releaseName, namespace, err)
	}
	if installed {
		return ComponentPlan{
			Name:      name,
			Action:    "skip",
			Chart:     chart,
			Version:   targetVersion,
			Current:   currentVersion,
			Namespace: namespace,
		}
	}
	return ComponentPlan{
		Name:      name,
		Action:    "install",
		Chart:     chart,
		Version:   targetVersion,
		Namespace: namespace,
	}
}

// DestroyHelmRelease checks whether a Helm release is installed and uninstalls
// it if so, printing progress via the printer. Returns an error if uninstall
// fails. The idx and total parameters control the progress numbering.
func DestroyHelmRelease(hc *helm.Client, name, releaseName, namespace string, idx, total int, printer *output.Printer) error {
	installed, version, err := hc.IsInstalled(releaseName, namespace)
	if err != nil {
		return fmt.Errorf("checking %s: %w", name, err)
	}
	if !installed {
		printer.ComponentSkipped(idx, total, name, "", "not installed")
		return nil
	}
	printer.ComponentStart(idx, total, name, version, "destroying")
	err = hc.Uninstall(releaseName, namespace)
	printer.ComponentDone(name, err)
	return err
}
