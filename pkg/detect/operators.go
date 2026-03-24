package detect

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"

	"github.com/todd-chamberlain/nstack/pkg/kube"
)

// knownOperator describes an operator we look for in the cluster.
type knownOperator struct {
	name       string
	deployment string
	namespace  string
}

// operators is the list of known operators to probe.
var operators = []knownOperator{
	{name: "gpu-operator", deployment: "gpu-operator", namespace: "gpu-operator"},
	{name: "network-operator", deployment: "nvidia-network-operator", namespace: "network-operator"},
	{name: "soperator", deployment: "soperator-manager", namespace: "soperator-system"},
	{name: "cert-manager", deployment: "cert-manager", namespace: "cert-manager"},
}

// detectOperators checks for the presence and status of known operators.
func detectOperators(ctx context.Context, clientset kubernetes.Interface) ([]DetectedOperator, error) {
	var results []DetectedOperator

	for _, op := range operators {
		dep, err := clientset.AppsV1().Deployments(op.namespace).Get(ctx, op.deployment, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				results = append(results, DetectedOperator{
					Name:      op.name,
					Namespace: op.namespace,
					Status:    "not-installed",
				})
				continue
			}
			return nil, err
		}

		version := kube.ExtractImageVersion(dep.Spec.Template.Spec.Containers)
		status := "degraded"
		if dep.Status.AvailableReplicas >= 1 {
			status = "running"
		}

		results = append(results, DetectedOperator{
			Name:      op.name,
			Version:   version,
			Namespace: op.namespace,
			Status:    status,
		})
	}

	return results, nil
}
