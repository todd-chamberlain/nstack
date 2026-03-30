package s5_slurm

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

// setupFederation configures Slurm federation via sacctmgr commands
// executed inside the controller pod.
func setupFederation(ctx context.Context, kc *kube.Client, site *config.Site, cluster config.ClusterConfig, printer *output.Printer) error {
	if site == nil || site.Federation == nil || site.Federation.Name == "" {
		return nil
	}

	fed := site.Federation
	ns := cluster.Namespace

	// Find a running controller pod.
	controllerPod, err := findPodByLabel(ctx, kc.Clientset(), ns, "app.kubernetes.io/component=controller")
	if err != nil {
		return fmt.Errorf("finding controller pod for federation setup: %w", err)
	}

	sacctmgrExec := func(args string) error {
		execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(execCtx, "kubectl", "exec", "-n", ns,
			controllerPod, "-c", "slurmctld", "--",
			"bash", "-c", fmt.Sprintf("sacctmgr -i %s 2>&1 || true", args))
		out, err := cmd.CombinedOutput()
		printer.Debugf("sacctmgr %s: %s", args, strings.TrimSpace(string(out)))
		if err != nil {
			return fmt.Errorf("sacctmgr %s: %s: %w", args, string(out), err)
		}
		return nil
	}

	printer.Infof("  Configuring Slurm federation '%s'...", fed.Name)

	// Create federation (idempotent; may already exist).
	if err := sacctmgrExec(fmt.Sprintf("add federation %s", fed.Name)); err != nil {
		printer.Debugf("federation create (may already exist): %v", err)
	}

	// Add this cluster to the federation.
	if err := sacctmgrExec(fmt.Sprintf("modify cluster %s set federation=%s", cluster.Name, fed.Name)); err != nil {
		return fmt.Errorf("adding cluster to federation: %w", err)
	}

	// Set cluster features for data locality.
	if len(fed.Features) > 0 {
		features := strings.Join(fed.Features, ",")
		if err := sacctmgrExec(fmt.Sprintf("modify cluster %s set features=%s", cluster.Name, features)); err != nil {
			return fmt.Errorf("setting cluster features: %w", err)
		}
		printer.Debugf("set cluster features: %s", features)
	}

	printer.PatchApplied(fmt.Sprintf("federation-%s", fed.Name))
	return nil
}

// exposeSlurmdbdOverTailscale annotates the slurmdbd service for Tailscale exposure.
// This is only applied when the site has a Tailscale overlay and accounting.deploy=true.
func exposeSlurmdbdOverTailscale(ctx context.Context, kc *kube.Client, site *config.Site, cluster config.ClusterConfig, printer *output.Printer) error {
	if site.Overlay == nil || site.Overlay.Type != "tailscale" {
		return nil
	}
	if site.Federation == nil || site.Federation.Accounting == nil || !site.Federation.Accounting.Deploy {
		return nil
	}

	// Patch the slurmdbd service to add Tailscale expose annotations.
	svcGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
	hostname := cluster.Name + "-slurmdbd"

	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				"tailscale.com/expose":   "true",
				"tailscale.com/hostname": hostname,
			},
		},
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling slurmdbd service patch: %w", err)
	}

	// Try to patch the slurmdbd service. The service name follows the soperator
	// naming convention: <cluster-name>-slurmdbd or similar. Try common names.
	svcNames := []string{
		cluster.Name + "-slurmdbd",
		"slurmdbd",
	}

	for _, svcName := range svcNames {
		_, getErr := kc.Clientset().CoreV1().Services(cluster.Namespace).Get(ctx, svcName, metav1.GetOptions{})
		if getErr != nil {
			continue
		}

		_, patchErr := kc.DynamicClient().Resource(svcGVR).Namespace(cluster.Namespace).Patch(
			ctx, svcName, types.MergePatchType, patchData, metav1.PatchOptions{},
		)
		if patchErr != nil {
			return fmt.Errorf("patching slurmdbd service %s for tailscale: %w", svcName, patchErr)
		}
		printer.Debugf("exposed slurmdbd over Tailscale as %s", hostname)
		return nil
	}

	printer.Debugf("slurmdbd service not found for Tailscale exposure (may not be deployed yet)")
	return nil
}
