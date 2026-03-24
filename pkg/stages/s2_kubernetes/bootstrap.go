package s2_kubernetes

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

// bootstrapCluster sets up Kubernetes based on the profile's distribution.
func bootstrapCluster(ctx context.Context, kc *kube.Client, site *config.Site, profile *config.Profile, printer *output.Printer) error {
	if profile == nil {
		return fmt.Errorf("profile is required for Kubernetes bootstrap")
	}

	switch profile.Kubernetes.Distribution {
	case "k3s":
		return bootstrapK3s(ctx, site, profile, printer)
	case "kubeadm":
		return bootstrapKubeadm(ctx, site, profile, printer)
	case "managed", "eks", "gke", "aks", "nebius":
		return validateManagedCluster(ctx, kc, printer)
	default:
		// If kubeconfig is provided, just validate the cluster.
		if site != nil && site.Kubeconfig != "" {
			return validateExistingCluster(ctx, kc, printer)
		}
		return fmt.Errorf("unsupported distribution: %s", profile.Kubernetes.Distribution)
	}
}

// bootstrapK3s sets up a K3s cluster.
// For remote nodes, this would SSH to each node and run the K3s installer.
// For local (single-node), it checks if K3s is already running.
func bootstrapK3s(ctx context.Context, site *config.Site, profile *config.Profile, printer *output.Printer) error {
	printer.Infof("        K3s: checking cluster status")

	// Check if K3s is already running locally.
	if site != nil && site.Kubeconfig != "" {
		kc, err := kube.NewClient(site.Kubeconfig)
		if err == nil {
			nodes, listErr := kc.Clientset().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if listErr == nil && len(nodes.Items) > 0 {
				printer.Infof("        K3s cluster already running with %d node(s)", len(nodes.Items))
				return nil
			}
		}
	}

	// K3s not running — provide installation instructions.
	// For v0.3, NStack documents the setup rather than running SSH commands.
	// Future versions will use SSH to bootstrap remote nodes.
	printer.Infof("        K3s: cluster not detected")
	printer.Infof("          Install K3s server:")
	printer.Infof("            curl -sfL https://get.k3s.io | sh -s - server \\")

	if profile.Networking.Overlay == "wireguard" {
		printer.Infof("              --flannel-backend=wireguard-native \\")
	}

	if profile.Kubernetes.ContainerdSocket != "" {
		printer.Infof("              --data-dir=%s", profile.Kubernetes.ContainerdSocket)
	}

	// For multi-node, provide agent join instructions.
	if profile.Kubernetes.MultiNode {
		printer.Infof("          Join worker nodes:")
		printer.Infof("            curl -sfL https://get.k3s.io | K3S_URL=https://<server>:6443 K3S_TOKEN=<token> sh -")
	}

	return nil
}

// bootstrapKubeadm sets up a kubeadm cluster.
func bootstrapKubeadm(ctx context.Context, site *config.Site, profile *config.Profile, printer *output.Printer) error {
	printer.Infof("        kubeadm: checking cluster status")

	if site != nil && site.Kubeconfig != "" {
		kc, err := kube.NewClient(site.Kubeconfig)
		if err == nil {
			nodes, listErr := kc.Clientset().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if listErr == nil && len(nodes.Items) > 0 {
				printer.Infof("        kubeadm cluster already running with %d node(s)", len(nodes.Items))
				return nil
			}
		}
	}

	printer.Infof("        kubeadm: cluster not detected")
	printer.Infof("          Initialize control plane:")
	printer.Infof("            sudo kubeadm init --pod-network-cidr=10.244.0.0/16")
	printer.Infof("          Join worker nodes:")
	printer.Infof("            sudo kubeadm join <control-plane>:6443 --token <token> --discovery-token-ca-cert-hash sha256:<hash>")

	return nil
}

// validateManagedCluster verifies a managed K8s cluster (EKS, GKE, AKS, Nebius) is accessible.
func validateManagedCluster(ctx context.Context, kc *kube.Client, printer *output.Printer) error {
	if kc == nil {
		return fmt.Errorf("kubeconfig required for managed Kubernetes cluster")
	}

	version, err := kc.Clientset().Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("cannot reach managed K8s API: %w", err)
	}

	printer.Infof("        Managed K8s cluster accessible (version %s)", version.GitVersion)
	return nil
}

// validateExistingCluster verifies an existing cluster is accessible via kubeconfig.
func validateExistingCluster(ctx context.Context, kc *kube.Client, printer *output.Printer) error {
	if kc == nil {
		return fmt.Errorf("kubeconfig required to validate existing cluster")
	}

	version, err := kc.Clientset().Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("cannot reach K8s API: %w", err)
	}

	nodes, err := kc.Clientset().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("cannot list nodes: %w", err)
	}

	ready := 0
	for _, node := range nodes.Items {
		for _, cond := range node.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				ready++
			}
		}
	}

	printer.Infof("        Existing cluster accessible (version %s, %d/%d nodes ready)",
		version.GitVersion, ready, len(nodes.Items))
	return nil
}
