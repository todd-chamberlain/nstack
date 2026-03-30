package s5_slurm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

// applyK3sPatches applies the minimal set of runtime patches needed for K3s.
//
// Most K3s adaptations are handled via Helm values (customSlurmConfig,
// customCgroupConfig, plugStackConfig, images) and the patched operator fork
// (no State=CLOUD, absolute SPANK paths, optional chroot.so).
//
// The only runtime patches remaining are:
//   - jailPopulatedMarker: populate-jail skips creating .populated when jail exists
//   - containerdSocketBind: K3s containerd socket at non-standard path for kruise
func applyK3sPatches(ctx context.Context, kc *kube.Client, profile *config.Profile, cluster config.ClusterConfig, printer *output.Printer) error {
	if profile == nil {
		return nil
	}

	// Ensure .populated marker exists in the jail PVC.
	// The populate-jail job exits early when the jail already has data
	// but doesn't create the marker, which blocks sconfigcontroller's init.
	if patchJailPopulatedMarker(ctx, kc, cluster.Namespace, printer) {
		printer.PatchApplied("jail-populated-marker")
	}

	// Bind-mount K3s containerd socket for kruise-daemon.
	if profile.Patches.ContainerdSocketBind {
		if err := patchContainerdSocketBind(ctx, kc, profile.Kubernetes.ContainerdSocket, printer); err != nil {
			printer.Debugf("containerd socket bind (non-fatal): %v", err)
		} else {
			printer.PatchApplied("containerd-socket-bind")
		}
	}

	return nil
}

// patchJailPopulatedMarker creates the .populated marker in the jail PVC.
func patchJailPopulatedMarker(ctx context.Context, kc *kube.Client, namespace string, printer *output.Printer) bool {
	targets := []struct{ pod, container string }{
		{"controller-0", "slurmctld"},
		{"login-0", "sshd"},
	}
	cs := kc.Clientset()
	for _, t := range targets {
		if _, err := cs.CoreV1().Pods(namespace).Get(ctx, t.pod, metav1.GetOptions{}); err != nil {
			continue
		}
		execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		cmd := exec.CommandContext(execCtx, "kubectl", "exec", "-n", namespace,
			t.pod, "-c", t.container, "--", "touch", "/mnt/jail/.populated")
		if out, err := cmd.CombinedOutput(); err != nil {
			printer.Debugf("jail marker on %s (non-fatal): %s: %v", t.pod, string(out), err)
			cancel()
		} else {
			printer.Debugf("created .populated marker via %s", t.pod)
			cancel()
			return true
		}
	}
	return false
}

// patchContainerdSocketBind creates the bind-mount from the K3s containerd socket
// to the standard path expected by kruise-daemon, with a systemd mount unit for
// boot persistence. If containerdSocket is empty, it falls back to the default
// K3s socket path.
func patchContainerdSocketBind(ctx context.Context, kc *kube.Client, containerdSocket string, printer *output.Printer) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("containerd socket bind requires root privileges (run with sudo)")
	}

	k3sSocket := containerdSocket
	if k3sSocket == "" {
		k3sSocket = "/run/k3s/containerd/containerd.sock"
	}
	stdSocket := "/run/containerd/containerd.sock"

	cmds := [][]string{
		{"mkdir", "-p", "/run/containerd"},
		{"touch", stdSocket},
		{"mount", "--bind", k3sSocket, stdSocket},
	}

	for _, args := range cmds {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			if args[0] == "mount" {
				printer.Debugf("mount --bind (may already be mounted): %s", string(out))
				continue
			}
			return fmt.Errorf("running %v: %s: %w", args, string(out), err)
		}
	}

	mountUnit := fmt.Sprintf(`[Unit]
Description=Bind mount K3s containerd socket for kruise-daemon
After=k3s.service
Requires=k3s.service

[Mount]
What=%s
Where=%s
Type=none
Options=bind

[Install]
WantedBy=multi-user.target
`, k3sSocket, stdSocket)
	unitPath := "/etc/systemd/system/run-containerd-containerd.sock.mount"
	if err := os.WriteFile(unitPath, []byte(mountUnit), 0644); err != nil {
		printer.Debugf("writing systemd mount unit (non-fatal): %v", err)
	} else {
		reloadCmd := exec.CommandContext(ctx, "systemctl", "daemon-reload")
		if out, err := reloadCmd.CombinedOutput(); err != nil {
			printer.Debugf("systemctl daemon-reload (non-fatal): %s: %v", string(out), err)
		}
		enableCmd := exec.CommandContext(ctx, "systemctl", "enable", "run-containerd-containerd.sock.mount")
		if out, err := enableCmd.CombinedOutput(); err != nil {
			printer.Debugf("systemctl enable (non-fatal): %s: %v", string(out), err)
		}
	}

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
