package s5_slurm

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

// validateSlurmName ensures a name is safe for use in sacctmgr commands.
func validateSlurmName(name string) error {
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9_-]+$`, name)
	if !matched {
		return fmt.Errorf("invalid name %q: must match [a-zA-Z0-9_-]+", name)
	}
	return nil
}

// setupFederation configures Slurm federation via sacctmgr commands
// executed inside the controller pod.
func setupFederation(ctx context.Context, kc *kube.Client, site *config.Site, cluster config.ClusterConfig, printer *output.Printer) error {
	if site == nil || site.Federation == nil || site.Federation.Name == "" {
		return nil
	}

	fed := site.Federation
	ns := cluster.Namespace

	// Validate all names before use to prevent shell injection.
	if err := validateSlurmName(fed.Name); err != nil {
		return fmt.Errorf("federation name: %w", err)
	}
	if err := validateSlurmName(cluster.Name); err != nil {
		return fmt.Errorf("cluster name: %w", err)
	}
	for _, f := range fed.Features {
		if err := validateSlurmName(f); err != nil {
			return fmt.Errorf("feature %q: %w", f, err)
		}
	}

	// Find a running controller pod.
	controllerPod, err := findPodByLabel(ctx, kc.Clientset(), ns, "app.kubernetes.io/component=controller")
	if err != nil {
		return fmt.Errorf("finding controller pod for federation setup: %w", err)
	}

	sacctmgrExec := func(args string) (string, error) {
		execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(execCtx, "kubectl", "exec", "-n", ns,
			controllerPod, "-c", "slurmctld", "--",
			"bash", "-c", fmt.Sprintf("sacctmgr -i %s 2>&1", args))
		out, err := cmd.CombinedOutput()
		outStr := strings.TrimSpace(string(out))
		printer.Debugf("sacctmgr %s: %s", args, outStr)
		if err != nil {
			return outStr, fmt.Errorf("sacctmgr %s: %s: %w", args, outStr, err)
		}
		return outStr, nil
	}

	printer.Infof("  Configuring Slurm federation '%s'...", fed.Name)

	// Create federation (idempotent; may already exist).
	out, err := sacctmgrExec(fmt.Sprintf("add federation %s", fed.Name))
	if err != nil {
		if strings.Contains(strings.ToLower(out), "already exists") {
			printer.Debugf("federation '%s' already exists", fed.Name)
		} else {
			printer.Debugf("federation create failed: %v", err)
		}
	}

	// Add this cluster to the federation.
	if _, err := sacctmgrExec(fmt.Sprintf("modify cluster %s set federation=%s", cluster.Name, fed.Name)); err != nil {
		return fmt.Errorf("adding cluster to federation: %w", err)
	}

	// Set cluster features for data locality.
	if len(fed.Features) > 0 {
		features := strings.Join(fed.Features, ",")
		if _, err := sacctmgrExec(fmt.Sprintf("modify cluster %s set features=%s", cluster.Name, features)); err != nil {
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

	hostname := cluster.Name + "-slurmdbd"

	patchData, err := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				"tailscale.com/expose":   "true",
				"tailscale.com/hostname": hostname,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("marshaling slurmdbd service patch: %w", err)
	}

	// Try to patch the slurmdbd service. The service name follows the soperator
	// naming convention: <cluster-name>-slurmdbd or similar. Try common names.
	svcClient := kc.Clientset().CoreV1().Services(cluster.Namespace)
	svcNames := []string{
		cluster.Name + "-slurmdbd",
		"slurmdbd",
	}

	for _, svcName := range svcNames {
		if _, err := svcClient.Get(ctx, svcName, metav1.GetOptions{}); err != nil {
			continue
		}
		if _, err := svcClient.Patch(ctx, svcName, types.MergePatchType, patchData, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("patching slurmdbd service %s for tailscale: %w", svcName, err)
		}
		printer.Debugf("exposed slurmdbd over Tailscale as %s", hostname)
		return nil
	}

	printer.Debugf("slurmdbd service not found for Tailscale exposure (may not be deployed yet)")
	return nil
}
