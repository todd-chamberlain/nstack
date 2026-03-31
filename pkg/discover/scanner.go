package discover

import (
	"bytes"
	"context"
	"net"
	"sort"
	"sync"
	"time"
)

// maxScanIPs is the maximum number of IPs allowed in a single CIDR scan
// to prevent accidental scans of very large subnets.
const maxScanIPs = 4096

// defaultWorkers is the default number of concurrent scan workers.
const defaultWorkers = 32

// defaultTimeout is the default per-host timeout in seconds.
const defaultTimeout = 10

// Scan discovers hosts on a network range by probing IPMI/Redfish, SSH, and K8s API
// in parallel for each IP. It classifies each host and returns a sorted list.
func Scan(ctx context.Context, opts ScanOptions) ([]DiscoveredHost, error) {
	if opts.Workers <= 0 {
		opts.Workers = defaultWorkers
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}

	ips, err := expandCIDR(opts.Network)
	if err != nil {
		return nil, err
	}

	if len(ips) > maxScanIPs {
		return nil, &CIDRTooLargeError{CIDR: opts.Network, Count: len(ips), Max: maxScanIPs}
	}

	timeout := time.Duration(opts.Timeout) * time.Second

	var (
		mu      sync.Mutex
		hosts   []DiscoveredHost
		wg      sync.WaitGroup
		sem     = make(chan struct{}, opts.Workers)
	)

	for _, ip := range ips {
		select {
		case <-ctx.Done():
			wg.Wait()
			return hosts, ctx.Err()
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			defer func() { <-sem }()

			host := scanHost(ctx, addr, opts, timeout)
			if host == nil {
				return // No probes succeeded, skip this IP
			}

			mu.Lock()
			hosts = append(hosts, *host)
			mu.Unlock()
		}(ip)
	}

	wg.Wait()

	// Sort by IP numerically for stable output
	sort.Slice(hosts, func(i, j int) bool {
		a := net.ParseIP(hosts[i].IP)
		b := net.ParseIP(hosts[j].IP)
		if a == nil || b == nil {
			return hosts[i].IP < hosts[j].IP
		}
		return bytes.Compare(a.To16(), b.To16()) < 0
	})

	return hosts, nil
}

// scanHost probes a single IP via BMC, SSH, and K8s API, merges results, and classifies.
func scanHost(ctx context.Context, ip string, opts ScanOptions, timeout time.Duration) *DiscoveredHost {
	type bmcResult struct {
		result *BMCProbeResult
		err    error
	}
	type sshResult struct {
		result *SSHProbeResult
		err    error
	}
	type k8sResult struct {
		result *K8sProbeResult
		err    error
	}

	bmcCh := make(chan bmcResult, 1)
	sshCh := make(chan sshResult, 1)
	k8sCh := make(chan k8sResult, 1)

	// Probe BMC (IPMI/Redfish)
	go func() {
		r, err := probeBMC(ctx, ip, opts, timeout)
		bmcCh <- bmcResult{r, err}
	}()

	// Probe SSH
	go func() {
		r, err := probeSSH(ctx, ip, opts)
		sshCh <- sshResult{r, err}
	}()

	// Probe K8s API
	go func() {
		r, err := probeK8sAPI(ctx, ip, timeout)
		k8sCh <- k8sResult{r, err}
	}()

	bmc := <-bmcCh
	ssh := <-sshCh
	k8s := <-k8sCh

	// If all probes failed, skip this host
	if bmc.err != nil && ssh.err != nil && k8s.err != nil {
		return nil
	}

	host := &DiscoveredHost{IP: ip}

	// Merge BMC results
	if bmc.err == nil && bmc.result != nil {
		host.HasBMC = true
		host.BMCType = bmc.result.Protocol
		if bmc.result.Hostname != "" && host.Hostname == "" {
			host.Hostname = bmc.result.Hostname
		}
		if bmc.result.CPUs > 0 {
			host.CPUCores = bmc.result.CPUs
		}
		if bmc.result.MemoryGB > 0 {
			host.MemoryGB = bmc.result.MemoryGB
		}
		host.GPUs = append(host.GPUs, bmc.result.GPUs...)
		host.NICs = append(host.NICs, bmc.result.NICs...)
	}

	// Merge SSH results (SSH data is generally more accurate)
	if ssh.err == nil && ssh.result != nil {
		host.HasSSH = true
		if ssh.result.Hostname != "" {
			host.Hostname = ssh.result.Hostname
		}
		host.IsPhysical = ssh.result.IsPhysical
		host.VirtType = ssh.result.VirtType
		if ssh.result.OS != "" {
			host.OS = ssh.result.OS
		}
		if ssh.result.CPUCores > 0 {
			host.CPUCores = ssh.result.CPUCores
		}
		if ssh.result.MemoryGB > 0 {
			host.MemoryGB = ssh.result.MemoryGB
		}
		if len(ssh.result.GPUs) > 0 {
			host.GPUs = ssh.result.GPUs // SSH GPU info is more accurate
		}
		if len(ssh.result.NICs) > 0 {
			host.NICs = ssh.result.NICs // SSH NIC info is more accurate
		}
		if ssh.result.HasK8s {
			host.HasK8s = true
			host.K8sDistro = ssh.result.K8sDistro
			host.K8sVersion = ssh.result.K8sVersion
		}
	}

	// Merge K8s API results
	if k8s.err == nil && k8s.result != nil {
		host.HasK8s = true
		if k8s.result.Version != "" {
			host.K8sVersion = k8s.result.Version
		}
		if k8s.result.Distro != "" {
			host.K8sDistro = k8s.result.Distro
		}
	}

	classifyHost(host)

	return host
}

// expandCIDR returns all host IPs in a CIDR range.
func expandCIDR(cidr string) ([]string, error) {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	var ips []string
	for ip := ip.Mask(ipNet.Mask); ipNet.Contains(ip); incrementIP(ip) {
		ips = append(ips, ip.String())
	}

	// For /31 point-to-point links (RFC 3021), both IPs are usable hosts.
	// For all other prefixes, strip network and broadcast addresses.
	if len(ips) > 2 {
		ips = ips[1 : len(ips)-1]
	}

	return ips, nil
}

func incrementIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

// CIDRTooLargeError is returned when the CIDR range contains too many addresses.
type CIDRTooLargeError struct {
	CIDR  string
	Count int
	Max   int
}

func (e *CIDRTooLargeError) Error() string {
	return "CIDR " + e.CIDR + " contains too many addresses; use a narrower subnet"
}
