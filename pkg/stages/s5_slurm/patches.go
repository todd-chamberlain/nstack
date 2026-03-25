package s5_slurm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

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
echo "Add SPANK plugin path to linker cache"
echo "/usr/lib/x86_64-linux-gnu/slurm" > /etc/ld.so.conf.d/slurm-spank.conf
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
//
// Patches that were previously runtime hacks are now handled via Helm values:
//   - busyboxRetag: replaced by images.* in slurm-cluster/k3s.yaml
//   - spankDisable: replaced by plugstack-override ConfigMap mount in values
//   - prologToBinTrue: slurmConfig.prolog/epilog in slurm-cluster/k3s.yaml
//   - entrypoint volume mount: customVolumeMounts in nodesets/k3s.yaml
func applyK3sPatches(ctx context.Context, kc *kube.Client, profile *config.Profile, printer *output.Printer) error {
	if profile == nil {
		return nil
	}

	// Create ConfigMaps needed by Helm value volume mounts.
	if profile.Patches.CgroupEntrypoint {
		if err := patchK3sConfigMaps(ctx, kc, printer); err != nil {
			return fmt.Errorf("K3s ConfigMaps: %w", err)
		}
		printer.PatchApplied("k3s-configmaps")
	}

	// Ensure .populated marker exists in the jail PVC.
	// The populate-jail job skips population when the jail already has data
	// but doesn't create the marker, which blocks sconfigcontroller's init.
	patchJailPopulatedMarker(ctx, kc, printer)
	printer.PatchApplied("jail-populated-marker")

	// Webhook-dependent patches need the operator running.
	// Temporarily scale up if it was previously scaled to 0.
	needsWebhook := profile.Patches.ProcMountDefault || profile.Patches.WorkerInitSkip
	if needsWebhook && profile.Patches.OperatorScaleDown {
		dep, err := kc.Clientset().AppsV1().Deployments(soperatorNamespace).Get(ctx, "soperator-manager", metav1.GetOptions{})
		if err == nil && dep.Spec.Replicas != nil && *dep.Spec.Replicas == 0 {
			printer.Debugf("temporarily scaling soperator-manager to 1 for webhook-dependent patches")
			_ = kc.ScaleDeployment(ctx, soperatorNamespace, "soperator-manager", 1)
			_ = kc.WaitForDeployment(ctx, soperatorNamespace, "soperator-manager", 60*time.Second)
		}
	}

	// Phase 2: Webhook-dependent patches (operator must be running).
	if profile.Patches.ProcMountDefault {
		if err := patchProcMount(ctx, kc, printer); err != nil {
			return fmt.Errorf("proc mount patch: %w", err)
		}
		printer.PatchApplied("proc-mount-default")
	}

	// Phase 3: Scale operator DOWN immediately after webhook patches.
	// This MUST happen before StatefulSet patches — the operator's reconciliation
	// loop would overwrite our StatefulSet changes (replicas, init container skip, procMount).
	if profile.Patches.OperatorScaleDown {
		time.Sleep(5 * time.Second) // Let operator finish current reconciliation.
		if err := patchOperatorScaleDown(ctx, kc, printer); err != nil {
			return fmt.Errorf("operator scale-down patch: %w", err)
		}
		printer.PatchApplied("operator-scale-down")
		time.Sleep(3 * time.Second) // Wait for operator pod to terminate.
	}

	// Phase 4: StatefulSet patches (operator MUST be down).
	if profile.Patches.WorkerInitSkip {
		printer.Debugf("waiting for worker-gpu StatefulSet...")
		gvr := schema.GroupVersionResource{Group: "apps.kruise.io", Version: "v1beta1", Resource: "statefulsets"}
		for i := 0; i < 30; i++ {
			_, err := kc.DynamicClient().Resource(gvr).Namespace(slurmNamespace).Get(ctx, "worker-gpu", metav1.GetOptions{})
			if err == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if err := patchWorkerInitSkip(ctx, kc, printer); err != nil {
			return fmt.Errorf("worker init skip patch: %w", err)
		}
		printer.PatchApplied("worker-init-skip")
	}

	// Phase 5: Host-level patches.
	if profile.Patches.ContainerdSocketBind {
		if err := patchContainerdSocketBind(ctx, kc, printer); err != nil {
			printer.Debugf("containerd socket bind (non-fatal): %v", err)
		} else {
			printer.PatchApplied("containerd-socket-bind")
		}
	}

	// SPANK library path: handled via spank-ldconfig ConfigMap mounted on all pods.
	// No runtime patch needed.

	return nil
}

// patchK3sConfigMaps creates or updates the ConfigMaps that are referenced by
// Helm value volume mounts (customMounts and customVolumeMounts):
//   - worker-entrypoint-fix: patched supervisord_entrypoint.sh for cgroup v2
//   - plugstack-override: empty plugstack.conf to disable SPANK chroot plugin
func patchK3sConfigMaps(ctx context.Context, kc *kube.Client, printer *output.Printer) error {
	cs := kc.Clientset()

	configMaps := []*corev1.ConfigMap{
		{
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
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "plugstack-override",
				Namespace: slurmNamespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "nstack",
					"app.kubernetes.io/component":  "slurm-patch",
				},
			},
			Data: map[string]string{
				"plugstack.conf": "# SPANK disabled for K3s by NStack\n",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "spank-ldconfig",
				Namespace: slurmNamespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "nstack",
					"app.kubernetes.io/component":  "slurm-patch",
				},
			},
			Data: map[string]string{
				"slurm-spank.conf": "/usr/lib/x86_64-linux-gnu/slurm\n",
			},
		},
	}

	for _, cm := range configMaps {
		_, err := cs.CoreV1().ConfigMaps(slurmNamespace).Create(ctx, cm, metav1.CreateOptions{})
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				_, err = cs.CoreV1().ConfigMaps(slurmNamespace).Update(ctx, cm, metav1.UpdateOptions{})
				if err != nil {
					return fmt.Errorf("updating %s ConfigMap: %w", cm.Name, err)
				}
				printer.Debugf("updated existing %s ConfigMap", cm.Name)
				continue
			}
			return fmt.Errorf("creating %s ConfigMap: %w", cm.Name, err)
		}
		printer.Debugf("created %s ConfigMap", cm.Name)
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
// sets replicas to 1, and sets procMount to Default on the main container.
// Uses a JSON patch targeting containers by index (not name) because the
// operator names the init container "worker-init", not "init-k8s-topology".
func patchWorkerInitSkip(ctx context.Context, kc *kube.Client, printer *output.Printer) error {
	// JSON Patch (RFC 6902) targeting by index:
	// - initContainers[1] is the worker-init container (index 0 is munge native sidecar)
	// - containers[0] is the slurmd container
	patchData := []byte(`[
  {"op":"replace","path":"/spec/replicas","value":1},
  {"op":"replace","path":"/spec/template/spec/initContainers/1/command","value":["bash","-c","echo Skipping worker-init; exit 0"]},
  {"op":"replace","path":"/spec/template/spec/containers/0/securityContext/procMount","value":"Default"}
]`)

	// kruise StatefulSets use the apps.kruise.io/v1beta1 API group.
	gvr := schema.GroupVersionResource{
		Group:    "apps.kruise.io",
		Version:  "v1beta1",
		Resource: "statefulsets",
	}

	printer.Debugf("patching kruise StatefulSet worker-gpu (JSON patch)")
	return kc.PatchResource(ctx, gvr, slurmNamespace, "worker-gpu",
		types.JSONPatchType, patchData)
}

// patchContainerdSocketBind creates the bind-mount from the K3s containerd socket
// to the standard path expected by kruise-daemon. This must persist across reboots
// so we also create a systemd mount unit.
func patchContainerdSocketBind(ctx context.Context, kc *kube.Client, printer *output.Printer) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("containerd socket bind requires root privileges (run with sudo)")
	}

	k3sSocket := "/run/k3s/containerd/containerd.sock"
	stdSocket := "/run/containerd/containerd.sock"

	// Create the bind-mount now.
	cmds := [][]string{
		{"mkdir", "-p", "/run/containerd"},
		{"touch", stdSocket},
		{"mount", "--bind", k3sSocket, stdSocket},
	}

	for _, args := range cmds {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			// mount --bind may fail if already mounted — that's OK
			if args[0] == "mount" {
				printer.Debugf("mount --bind (may already be mounted): %s", string(out))
				continue
			}
			return fmt.Errorf("running %v: %s: %w", args, string(out), err)
		}
	}

	// Create a systemd mount unit so this persists across reboots.
	mountUnit := `[Unit]
Description=Bind mount K3s containerd socket for kruise-daemon
After=k3s.service
Requires=k3s.service

[Mount]
What=/run/k3s/containerd/containerd.sock
Where=/run/containerd/containerd.sock
Type=none
Options=bind

[Install]
WantedBy=multi-user.target
`
	unitPath := "/etc/systemd/system/run-containerd-containerd.sock.mount"
	writeCmd := exec.CommandContext(ctx, "bash", "-c",
		fmt.Sprintf("echo '%s' > %s && systemctl daemon-reload && systemctl enable run-containerd-containerd.sock.mount",
			mountUnit, unitPath))
	if out, err := writeCmd.CombinedOutput(); err != nil {
		printer.Debugf("systemd mount unit (non-fatal): %s: %v", string(out), err)
	}

	// Restart kruise-daemon to pick up the socket.
	cs := kc.Clientset()
	pods, err := cs.CoreV1().Pods("kruise-system").List(ctx, metav1.ListOptions{
		LabelSelector: "control-plane=kruise-daemon",
	})
	if err == nil {
		for _, pod := range pods.Items {
			_ = cs.CoreV1().Pods("kruise-system").Delete(ctx, pod.Name, metav1.DeleteOptions{})
			printer.Debugf("restarted kruise-daemon pod %s", pod.Name)
		}
	}

	return nil
}

// patchProcMount patches the NodeSet "worker-gpu" custom resource to set
// security.procMount to "Default", required for K3s which doesn't support
// the "Unmasked" procMount type.
func patchProcMount(ctx context.Context, kc *kube.Client, printer *output.Printer) error {
	patchData := []byte(`{"spec":{"security":{"procMount":"Default"}}}`)

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

// patchJailPopulatedMarker creates the .populated marker in the jail PVC.
// The populate-jail job exits early when the jail already has data but skips
// creating the marker, which blocks sconfigcontroller's init container.
func patchJailPopulatedMarker(ctx context.Context, kc *kube.Client, printer *output.Printer) {
	targets := []struct{ pod, container string }{
		{"controller-0", "slurmctld"},
		{"login-0", "sshd"},
	}
	cs := kc.Clientset()
	for _, t := range targets {
		if _, err := cs.CoreV1().Pods(slurmNamespace).Get(ctx, t.pod, metav1.GetOptions{}); err != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, "kubectl", "exec", "-n", slurmNamespace,
			t.pod, "-c", t.container, "--", "touch", "/mnt/jail/.populated")
		if out, err := cmd.CombinedOutput(); err != nil {
			printer.Debugf("jail marker on %s (non-fatal): %s: %v", t.pod, string(out), err)
		} else {
			printer.Debugf("created .populated marker via %s", t.pod)
			return
		}
	}
}

// patchSpankLibPath adds the SPANK plugin library path to the dynamic linker
// cache on controller and login pods. The worker handles this in its custom
// entrypoint script. This is needed because the operator hardcodes bare library
// names in plugstack.conf (e.g., "chroot.so") and dlopen can't find them without
// the path in ld.so.conf.d.
func patchSpankLibPath(ctx context.Context, kc *kube.Client, printer *output.Printer) {
	cs := kc.Clientset()
	targets := []struct {
		name, container string
	}{
		{"controller-0", "slurmctld"},
		{"login-0", "sshd"},
	}

	ldconfigCmd := `echo "/usr/lib/x86_64-linux-gnu/slurm" > /etc/ld.so.conf.d/slurm-spank.conf && ldconfig`

	for _, t := range targets {
		if _, err := cs.CoreV1().Pods(slurmNamespace).Get(ctx, t.name, metav1.GetOptions{}); err != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, "kubectl", "exec", "-n", slurmNamespace,
			t.name, "-c", t.container, "--", "bash", "-c", ldconfigCmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			printer.Debugf("ldconfig on %s (non-fatal): %s: %v", t.name, string(out), err)
		} else {
			printer.Debugf("SPANK lib path added on %s/%s", t.name, t.container)
		}
	}
}
