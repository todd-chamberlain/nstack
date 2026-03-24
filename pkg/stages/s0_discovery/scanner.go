package s0_discovery

import (
	"context"
	"fmt"
	"net"
	"sync"
)

// ScanResult holds discovered BMC endpoints and their hardware inventory.
type ScanResult struct {
	Nodes []DiscoveredNode
}

// DiscoveredNode represents a bare metal host found via BMC scanning.
type DiscoveredNode struct {
	BMCAddress string          `json:"bmcAddress"`
	Hostname   string          `json:"hostname"`
	Protocol   string          `json:"protocol"` // "redfish" or "ipmi"
	GPUs       []DiscoveredGPU `json:"gpus,omitempty"`
	NICs       []DiscoveredNIC `json:"nics,omitempty"`
	CPUs       int             `json:"cpus"`
	MemoryGB   int             `json:"memoryGB"`
	PowerState string          `json:"powerState"` // "on", "off", "unknown"
}

// DiscoveredGPU describes a GPU model and how many were found on a host.
type DiscoveredGPU struct {
	Model string `json:"model"`
	Count int    `json:"count"`
}

// DiscoveredNIC describes a network interface found on a host.
type DiscoveredNIC struct {
	Model string `json:"model"`
	Speed string `json:"speed"`
	Type  string `json:"type"` // "ethernet", "infiniband"
}

// BMCCredentials holds authentication info for BMC access.
type BMCCredentials struct {
	Username string
	Password string
}

// ScanNetwork scans a CIDR range for BMC endpoints using Redfish and IPMI.
// It probes common BMC ports (443 for Redfish, 623 for IPMI) on each IP.
func ScanNetwork(ctx context.Context, cidr string, credentials BMCCredentials, concurrency int) (*ScanResult, error) {
	ips, err := expandCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("parsing CIDR %s: %w", cidr, err)
	}

	if concurrency <= 0 {
		concurrency = 32
	}

	result := &ScanResult{}
	var mu sync.Mutex
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, ip := range ips {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			defer func() { <-sem }()

			node, probeErr := probeHost(ctx, addr, credentials)
			if probeErr != nil {
				return // Host not reachable or not a BMC, skip silently
			}

			mu.Lock()
			result.Nodes = append(result.Nodes, *node)
			mu.Unlock()
		}(ip)
	}

	wg.Wait()
	return result, nil
}

// probeHost attempts to connect to a host's BMC via Redfish (port 443) then IPMI (port 623).
func probeHost(ctx context.Context, addr string, creds BMCCredentials) (*DiscoveredNode, error) {
	// Try Redfish first (HTTPS on port 443)
	node, err := probeRedfish(ctx, addr, creds)
	if err == nil {
		return node, nil
	}

	// Fallback to IPMI (UDP port 623)
	node, err = probeIPMI(ctx, addr, creds)
	if err == nil {
		return node, nil
	}

	return nil, fmt.Errorf("no BMC found at %s", addr)
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

	// Remove network and broadcast addresses for /24 and larger
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
