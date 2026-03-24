package s5_slurm

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

// workerEntrypointScript is the patched supervisord entrypoint for K3s cgroup v2
// compatibility. This is embedded as a Go string constant from the original
// worker-entrypoint-fix ConfigMap.
const workerEntrypointScript = `#!/bin/bash
echo "Starting slurmd entrypoint script (patched for K3s cgroup v2)"
if [ -n "${CGROUP_V2}" ]; then
    CGROUP_PATH=$(cat /proc/self/cgroup | sed 's/^0:://')
    if [ -n "${CGROUP_PATH}" ]; then
        echo "cgroup v2 detected, creating cgroup for ${CGROUP_PATH}"
        mkdir -p /sys/fs/cgroup/"${CGROUP_PATH}"/../system.slice 2>/dev/null || echo "Warning: could not create system.slice cgroup (expected on K3s), continuing..."
        echo "1" > /sys/fs/cgroup/${CGROUP_PATH}/../system.slice/memory.oom.group 2>/dev/null || true
    else
        echo "cgroup v2 detected, but cgroup path is empty - continuing anyway"
    fi
fi
echo "Link users from jail"
ln -sf /mnt/jail/etc/passwd /etc/passwd
ln -sf /mnt/jail/etc/group /etc/group
ln -sf /mnt/jail/etc/shadow /etc/shadow
ln -sf /mnt/jail/etc/gshadow /etc/gshadow
chown -h 0:42 /etc/{shadow,gshadow} 2>/dev/null || true
echo "Link SSH message of the day scripts from jail"
ln -sf /mnt/jail/etc/update-motd.d /etc/update-motd.d
echo "Link home from jail because slurmd uses it"
ln -sf /mnt/jail/home /home
echo "Link soperator home directories from jail to use SSH keys from there"
mkdir -p /mnt/jail/opt/soperator-home
ln -sf /mnt/jail/opt/soperator-home /opt/soperator-home
echo "Symlink slurm configs from jail(sconfigcontroller)"
rm -rf /etc/slurm && ln -sf /mnt/jail/etc/slurm /etc/slurm
echo "Make ulimits as big as possible"
set_ulimit() {
    local limit_option=$1
    local limit_value=$2
    ulimit "$limit_option" "$limit_value" 2>/dev/null || true
}
set_ulimit -HSR unlimited
set_ulimit -HSc unlimited
set_ulimit -HSd unlimited
set_ulimit -HSe unlimited
set_ulimit -HSf unlimited
set_ulimit -HSi unlimited
set_ulimit -HSl unlimited
set_ulimit -HSm unlimited
set_ulimit -HSn 1048576
set_ulimit -HSq unlimited
set_ulimit -HSr unlimited
set_ulimit -HSs unlimited
set_ulimit -HSt unlimited
set_ulimit -HSu unlimited
set_ulimit -HSv unlimited
set_ulimit -HSx unlimited
echo "Apply sysctl limits from /etc/sysctl.conf"
sysctl -p 2>/dev/null || true
echo "Update linker cache"
ldconfig
echo "Complement jail rootfs"
/opt/bin/slurm/complement_jail.sh -j /mnt/jail -u /mnt/jail.upper -w
echo "Create privilege separation directory /var/run/sshd"
mkdir -p /var/run/sshd
echo "Waiting until munge is started"
while [ ! -S "/run/munge/munge.socket.2" ]; do sleep 2; done
echo "Start supervisord daemon"
exec /usr/bin/supervisord
`

// applyK3sPatches applies distribution-specific patches based on the profile.
// Each patch is conditional on its corresponding flag in profile.Patches.
func applyK3sPatches(ctx context.Context, kc *kube.Client, profile *config.Profile, printer *output.Printer) error {
	if profile == nil {
		return nil
	}

	if profile.Patches.CgroupEntrypoint {
		if err := patchCgroupEntrypoint(ctx, kc, printer); err != nil {
			return fmt.Errorf("cgroup entrypoint patch: %w", err)
		}
		printer.PatchApplied("cgroup-entrypoint")
	}

	if profile.Patches.BusyboxRetag {
		if err := patchBusyboxRetag(ctx, kc, profile, printer); err != nil {
			// Non-fatal: warn and continue.
			printer.Debugf("busybox retag patch (non-fatal): %v", err)
		} else {
			printer.PatchApplied("busybox-retag")
		}
	}

	// Apply webhook-dependent patches BEFORE scaling down the operator.
	if profile.Patches.ProcMountDefault {
		if err := patchProcMount(ctx, kc, printer); err != nil {
			return fmt.Errorf("proc mount patch: %w", err)
		}
		printer.PatchApplied("proc-mount-default")
	}

	if profile.Patches.WorkerInitSkip {
		if err := patchWorkerInitSkip(ctx, kc, printer); err != nil {
			return fmt.Errorf("worker init skip patch: %w", err)
		}
		printer.PatchApplied("worker-init-skip")
	}

	// Scale down operator LAST — after all patches that need the webhook.
	if profile.Patches.OperatorScaleDown {
		if err := patchOperatorScaleDown(ctx, kc, printer); err != nil {
			return fmt.Errorf("operator scale-down patch: %w", err)
		}
		printer.PatchApplied("operator-scale-down")
	}

	return nil
}

// patchCgroupEntrypoint creates or updates a ConfigMap containing the patched
// supervisord_entrypoint.sh script for cgroup v2 compatibility on K3s.
func patchCgroupEntrypoint(ctx context.Context, kc *kube.Client, printer *output.Printer) error {
	cs := kc.Clientset()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "worker-entrypoint-fix",
			Namespace: slurmNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "nstack",
				"app.kubernetes.io/component":  "slurm-patch",
			},
		},
		Data: map[string]string{
			"supervisord_entrypoint.sh": workerEntrypointScript,
		},
	}

	_, err := cs.CoreV1().ConfigMaps(slurmNamespace).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Update the existing ConfigMap.
			_, err = cs.CoreV1().ConfigMaps(slurmNamespace).Update(ctx, cm, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("updating worker-entrypoint-fix ConfigMap: %w", err)
			}
			printer.Debugf("updated existing worker-entrypoint-fix ConfigMap")
			return nil
		}
		return fmt.Errorf("creating worker-entrypoint-fix ConfigMap: %w", err)
	}

	printer.Debugf("created worker-entrypoint-fix ConfigMap")
	return nil
}

// patchBusyboxRetag pulls busybox:latest and retags it to the Nebius registry path
// expected by soperator init containers. Uses nerdctl with the k3s containerd socket.
func patchBusyboxRetag(ctx context.Context, kc *kube.Client, profile *config.Profile, printer *output.Printer) error {
	socket := "unix:///run/k3s/containerd/containerd.sock"
	if profile != nil && profile.Kubernetes.ContainerdSocket != "" {
		socket = profile.Kubernetes.ContainerdSocket
	}

	// Check if nerdctl is available.
	nerdctl, err := exec.LookPath("nerdctl")
	if err != nil {
		return fmt.Errorf("nerdctl not found in PATH: %w", err)
	}

	printer.Debugf("pulling busybox:latest via nerdctl")
	pullCmd := exec.CommandContext(ctx, nerdctl,
		"--address", socket,
		"--namespace", "k8s.io",
		"pull", "busybox:latest",
	)
	if out, err := pullCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pulling busybox:latest: %s: %w", string(out), err)
	}

	printer.Debugf("tagging busybox to nebius registry path")
	tagCmd := exec.CommandContext(ctx, nerdctl,
		"--address", socket,
		"--namespace", "k8s.io",
		"tag", "busybox:latest",
		"cr.eu-north1.nebius.cloud/soperator/busybox:latest",
	)
	if out, err := tagCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tagging busybox: %s: %w", string(out), err)
	}

	return nil
}

// patchOperatorScaleDown scales the soperator-manager deployment to 0 replicas.
// This prevents the operator from reconciling resources while manual patches are
// being applied to the Slurm cluster.
func patchOperatorScaleDown(ctx context.Context, kc *kube.Client, printer *output.Printer) error {
	printer.Debugf("scaling soperator-manager to 0 replicas")
	return kc.ScaleDeployment(ctx, soperatorNamespace, "soperator-manager", 0)
}

// patchWorkerInitSkip patches the worker-gpu StatefulSet (kruise) to skip
// the problematic init container by replacing its command with a no-op,
// and sets replicas to 1.
func patchWorkerInitSkip(ctx context.Context, kc *kube.Client, printer *output.Printer) error {
	// Use a strategic merge patch to modify the StatefulSet.
	// Target: kruise StatefulSet "worker-gpu" in slurm namespace.
	// We use a JSON merge patch to:
	// 1. Set replicas to 1
	// 2. Replace initContainers[1] command with a no-op

	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"replicas": 1,
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"initContainers": []map[string]interface{}{
						{
							"name":    "init-k8s-topology",
							"command": []string{"bash", "-c", "echo Skipping; exit 0"},
						},
					},
				},
			},
		},
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling worker-gpu patch: %w", err)
	}

	// kruise StatefulSets use the apps.kruise.io/v1beta1 API group.
	gvr := schema.GroupVersionResource{
		Group:    "apps.kruise.io",
		Version:  "v1beta1",
		Resource: "statefulsets",
	}

	printer.Debugf("patching kruise StatefulSet worker-gpu")
	return kc.PatchResource(ctx, gvr, slurmNamespace, "worker-gpu",
		types.MergePatchType, patchData)
}

// patchProcMount patches the NodeSet "worker-gpu" custom resource to set
// security.procMount to "Default", required for K3s which doesn't support
// the "Unmasked" procMount type.
func patchProcMount(ctx context.Context, kc *kube.Client, printer *output.Printer) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"security": map[string]interface{}{
				"procMount": "Default",
			},
		},
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling NodeSet procMount patch: %w", err)
	}

	// NodeSet is a soperator CRD.
	gvr := schema.GroupVersionResource{
		Group:    "slurm.nebius.ai",
		Version:  "v1alpha1",
		Resource: "nodesets",
	}

	printer.Debugf("patching NodeSet worker-gpu procMount to Default")
	return kc.PatchResource(ctx, gvr, slurmNamespace, "worker-gpu",
		types.MergePatchType, patchData)
}
