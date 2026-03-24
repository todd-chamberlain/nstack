package detect

import (
	"context"

	"k8s.io/client-go/kubernetes"

	"github.com/todd-chamberlain/nstack/pkg/engine"
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
func detectOperators(ctx context.Context, clientset kubernetes.Interface) ([]engine.DetectedOperator, error) {
	results := make([]engine.DetectedOperator, 0, len(operators))
	for _, op := range operators {
		results = append(results, engine.DetectDeployment(ctx, clientset, op.namespace, op.deployment, op.name))
	}
	return results, nil
}
