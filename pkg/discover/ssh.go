package discover

import (
	"context"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// SSHProbeResult holds information gathered from SSH assessment commands.
type SSHProbeResult struct {
	Hostname   string
	IsPhysical bool
	VirtType   string
	OS         string
	CPUCores   int
	MemoryGB   int
	GPUs       []DiscoveredGPU
	NICs       []DiscoveredNIC
	HasK8s     bool
	K8sDistro  string
	K8sVersion string
}

// SSH probe constants.
const (
	cmdTimeout     = 5 * time.Second // Per-command timeout for SSH assessment commands.
	sshPort        = "22"
	defaultSSHUser = "root"
)

// versionRe matches Kubernetes-style version strings (v1.x.y...).
var versionRe = regexp.MustCompile(`v\d+\.\d+\.\d+[^\s]*`)

// probeSSH connects to a host via SSH and runs assessment commands.
func probeSSH(ctx context.Context, ip string, opts ScanOptions) (*SSHProbeResult, error) {
	timeout := time.Duration(opts.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	authMethods := buildAuthMethods(opts)
	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH authentication methods available")
	}

	user := opts.SSHUser
	if user == "" {
		user = defaultSSHUser
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // Discovery scans unknown hosts
		Timeout:         timeout,
	}

	addr := net.JoinHostPort(ip, sshPort)
	client, err := sshDialContext(ctx, "tcp", addr, config, timeout)
	if err != nil {
		return nil, fmt.Errorf("SSH connect to %s: %w", ip, err)
	}
	defer client.Close()

	result := &SSHProbeResult{}

	// Run each assessment command, ignoring individual failures
	result.Hostname = strings.TrimSpace(runSSHCommand(client, "hostname"))

	virtType := strings.TrimSpace(runSSHCommand(client, "systemd-detect-virt"))
	if virtType != "" {
		result.VirtType = virtType
		result.IsPhysical = (virtType == "none")
	}

	result.OS = parseOSRelease(runSSHCommand(client, "cat /etc/os-release"))

	if cores := strings.TrimSpace(runSSHCommand(client, "nproc")); cores != "" {
		if n, err := strconv.Atoi(cores); err == nil {
			result.CPUCores = n
		}
	}

	if mem := strings.TrimSpace(runSSHCommand(client, "free -g | awk '/Mem:/{print $2}'")); mem != "" {
		if n, err := strconv.Atoi(mem); err == nil {
			result.MemoryGB = n
		}
	}

	gpuOutput := runSSHCommand(client, "nvidia-smi -L 2>/dev/null")
	if gpuOutput != "" {
		result.GPUs = parseNvidiaSMI(gpuOutput)
	}

	nicOutput := runSSHCommand(client, "ip -br link show | grep -v lo")
	if nicOutput != "" {
		result.NICs = parseNICs(nicOutput)
	}

	// Check for InfiniBand
	ibOutput := runSSHCommand(client, "ls /sys/class/infiniband/ 2>/dev/null")
	if strings.TrimSpace(ibOutput) != "" {
		ibDevices := strings.Fields(strings.TrimSpace(ibOutput))
		for _, dev := range ibDevices {
			found := false
			for i := range result.NICs {
				if result.NICs[i].Name == dev {
					result.NICs[i].Type = "infiniband"
					found = true
					break
				}
			}
			if !found {
				result.NICs = append(result.NICs, DiscoveredNIC{
					Name: dev,
					Type: "infiniband",
				})
			}
		}
	}

	// K8s detection
	kubectlVersion := strings.TrimSpace(runSSHCommand(client, "kubectl version --client --short 2>/dev/null || kubectl version --client 2>/dev/null"))
	nodesOutput := strings.TrimSpace(runSSHCommand(client, "kubectl get nodes 2>/dev/null | head -5"))

	if nodesOutput != "" && !strings.Contains(nodesOutput, "refused") && !strings.Contains(nodesOutput, "error") {
		result.HasK8s = true
		result.K8sVersion, result.K8sDistro = parseK8sInfo(kubectlVersion, nodesOutput)
	}

	// Also check for K8s by looking for common K8s process
	if !result.HasK8s {
		k3sCheck := strings.TrimSpace(runSSHCommand(client, "pgrep -x k3s >/dev/null 2>&1 && echo running"))
		if k3sCheck == "running" {
			result.HasK8s = true
			result.K8sDistro = "k3s"
			if kubectlVersion != "" {
				result.K8sVersion = extractVersion(kubectlVersion)
			}
		}
	}

	return result, nil
}

// buildAuthMethods constructs SSH authentication methods from ScanOptions.
func buildAuthMethods(opts ScanOptions) []ssh.AuthMethod {
	var methods []ssh.AuthMethod

	// Try SSH key first
	if opts.SSHKeyPath != "" {
		if key, err := os.ReadFile(opts.SSHKeyPath); err == nil {
			if signer, err := ssh.ParsePrivateKey(key); err == nil {
				methods = append(methods, ssh.PublicKeys(signer))
			}
		}
	}

	// Try SSH agent
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			agentClient := agent.NewClient(conn)
			methods = append(methods, ssh.PublicKeysCallback(agentClient.Signers))
		}
	}

	// Try password
	if opts.SSHPass != "" {
		methods = append(methods, ssh.Password(opts.SSHPass))
	}

	return methods
}

// sshDialContext dials an SSH connection with context cancellation support.
func sshDialContext(ctx context.Context, network, addr string, config *ssh.ClientConfig, timeout time.Duration) (*ssh.Client, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	// Wrap with context deadline
	type connResult struct {
		client *ssh.Client
		err    error
	}
	ch := make(chan connResult, 1)
	go func() {
		sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
		if err != nil {
			conn.Close()
			ch <- connResult{nil, err}
			return
		}
		ch <- connResult{ssh.NewClient(sshConn, chans, reqs), nil}
	}()

	select {
	case <-ctx.Done():
		conn.Close()
		return nil, ctx.Err()
	case result := <-ch:
		return result.client, result.err
	}
}

// runSSHCommand executes a single command over SSH with a per-command timeout.
// Returns empty string on any error.
func runSSHCommand(client *ssh.Client, command string) string {
	session, err := client.NewSession()
	if err != nil {
		return ""
	}
	defer session.Close()

	// Use a timer for per-command timeout
	done := make(chan []byte, 1)
	go func() {
		out, _ := session.CombinedOutput(command)
		done <- out
	}()

	select {
	case out := <-done:
		return string(out)
	case <-time.After(cmdTimeout):
		return ""
	}
}

// parseOSRelease extracts PRETTY_NAME from /etc/os-release content.
func parseOSRelease(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			val := strings.TrimPrefix(line, "PRETTY_NAME=")
			val = strings.Trim(val, "\"")
			return val
		}
	}
	return ""
}

// nvidiaSMIRe matches nvidia-smi -L output lines.
// Example: "GPU 0: NVIDIA H100 80GB HBM3 (UUID: GPU-...)"
var nvidiaSMIRe = regexp.MustCompile(`GPU \d+:\s+(.+?)\s*\(UUID:`)

// parseNvidiaSMI parses nvidia-smi -L output into GPU info.
func parseNvidiaSMI(output string) []DiscoveredGPU {
	gpuCounts := make(map[string]int)
	gpuVRAM := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		matches := nvidiaSMIRe.FindStringSubmatch(line)
		if len(matches) >= 2 {
			model := matches[1]
			gpuCounts[model]++
			// Try to extract VRAM from model name (e.g., "80GB")
			vram := extractVRAM(model)
			if vram != "" {
				gpuVRAM[model] = vram
			}
		}
	}

	var gpus []DiscoveredGPU
	for model, count := range gpuCounts {
		gpus = append(gpus, DiscoveredGPU{
			Model: model,
			Count: count,
			VRAM:  gpuVRAM[model],
		})
	}
	return gpus
}

// vramRe matches VRAM sizes like "80GB", "4GB".
var vramRe = regexp.MustCompile(`(\d+)\s*GB`)

// extractVRAM tries to find a VRAM size in a GPU model string.
func extractVRAM(model string) string {
	matches := vramRe.FindStringSubmatch(model)
	if len(matches) >= 2 {
		return matches[1] + "GB"
	}
	return ""
}

// virtualNICPrefixes lists interface name prefixes to skip during NIC enumeration.
var virtualNICPrefixes = []string{"veth", "br-", "docker", "cni", "flannel", "calico"}

// parseNICs parses `ip -br link show` output into NIC info.
func parseNICs(output string) []DiscoveredNIC {
	var nics []DiscoveredNIC
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if name == "lo" || isVirtualNIC(name) {
			continue
		}
		nicType := "ethernet"
		if strings.HasPrefix(name, "ib") {
			nicType = "infiniband"
		}
		nics = append(nics, DiscoveredNIC{
			Name: name,
			Type: nicType,
		})
	}
	return nics
}

// isVirtualNIC returns true if the interface name matches a known virtual NIC prefix.
func isVirtualNIC(name string) bool {
	for _, prefix := range virtualNICPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// parseK8sInfo extracts K8s version and distro from kubectl output.
func parseK8sInfo(clientVersion, nodesOutput string) (version, distro string) {
	version = extractVersion(clientVersion)
	distro = detectK8sDistro(version)

	// Also try to get version from nodes output (server version is more accurate)
	for _, line := range strings.Split(nodesOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 {
			for _, f := range fields {
				if strings.HasPrefix(f, "v1.") {
					version = f
					distro = detectK8sDistro(f)
					break
				}
			}
		}
	}

	return version, distro
}

// extractVersion finds a version string (v1.x.y...) in text.
func extractVersion(text string) string {
	if match := versionRe.FindString(text); match != "" {
		return match
	}
	return strings.TrimSpace(text)
}
