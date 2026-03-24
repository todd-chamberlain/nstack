package engine

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/todd-chamberlain/nstack/pkg/kube"
)

// DetermineOverallStatus derives a single status string from a set of
// component statuses. It is used by every stage's Status() method.
func DetermineOverallStatus(components []ComponentStatus) string {
	allRunning := true
	anyNotInstalled := false
	anyDegraded := false
	for _, c := range components {
		switch c.Status {
		case "not-installed":
			anyNotInstalled = true
			allRunning = false
		case "degraded", "failed":
			anyDegraded = true
			allRunning = false
		case "running", "scaled-down":
			// ok
		default:
			allRunning = false
		}
	}
	switch {
	case allRunning && !anyNotInstalled:
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
