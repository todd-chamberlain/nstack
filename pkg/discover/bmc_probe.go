package discover

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// BMCProbeResult holds information gathered from a BMC probe.
type BMCProbeResult struct {
	Protocol string // "redfish" or "ipmi"
	Hostname string
	CPUs     int
	MemoryGB int
	GPUs     []DiscoveredGPU
	NICs     []DiscoveredNIC
}

// BMC probe constants.
const (
	maxRedfishBody = 1 << 20      // Maximum Redfish response body size (1 MB).
	redfishPort    = "443"        // Standard Redfish HTTPS port.
	ipmiPort       = "623"        // Standard IPMI UDP port.
	tcpDialTimeout = 2 * time.Second // Timeout for quick TCP port checks.
)

// probeBMC tries Redfish first, then falls back to IPMI.
func probeBMC(ctx context.Context, ip string, opts ScanOptions, timeout time.Duration) (*BMCProbeResult, error) {
	// Try Redfish (HTTPS port 443)
	result, err := probeRedfishDiscover(ctx, ip, opts, timeout)
	if err == nil {
		return result, nil
	}

	// Fallback to IPMI (UDP port 623)
	result, err = probeIPMIDiscover(ctx, ip, timeout)
	if err == nil {
		return result, nil
	}

	return nil, fmt.Errorf("no BMC found at %s", ip)
}

// probeRedfishDiscover checks for a Redfish BMC and optionally gathers system info.
func probeRedfishDiscover(ctx context.Context, ip string, opts ScanOptions, timeout time.Duration) (*BMCProbeResult, error) {
	// Quick TCP check on Redfish port
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, redfishPort), tcpDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("port %s not open on %s", redfishPort, ip)
	}
	conn.Close()

	httpClient := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // BMCs use self-signed certs
			DisableKeepAlives: true,
		},
	}

	baseURL := fmt.Sprintf("https://%s", ip)

	// Check Redfish service root
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/redfish/v1", nil)
	if err != nil {
		return nil, err
	}
	if opts.BMCUser != "" {
		req.SetBasicAuth(opts.BMCUser, opts.BMCPass)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("redfish probe failed on %s: %w", ip, err)
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("redfish returned %d on %s", resp.StatusCode, ip)
	}

	result := &BMCProbeResult{Protocol: "redfish"}

	// If credentials are provided, get system info
	if opts.BMCUser != "" {
		fetchRedfishSystemInfo(ctx, httpClient, baseURL, opts.BMCUser, opts.BMCPass, result)
	}

	return result, nil
}

// redfishSystem is a subset of the Redfish Systems/1 response.
type redfishSystem struct {
	HostName         string                  `json:"HostName"`
	ProcessorSummary redfishProcessorSummary `json:"ProcessorSummary"`
	MemorySummary    redfishMemorySummary    `json:"MemorySummary"`
}

type redfishProcessorSummary struct {
	Count int `json:"Count"`
}

type redfishMemorySummary struct {
	TotalSystemMemoryGiB int `json:"TotalSystemMemoryGiB"`
}

type redfishCollection struct {
	Members []redfishMember `json:"Members"`
}

type redfishMember struct {
	ID string `json:"@odata.id"`
}

type redfishPCIeDevice struct {
	Manufacturer string `json:"Manufacturer"`
	Model        string `json:"Model"`
	DeviceClass  string `json:"DeviceClass"`
}

// fetchRedfishSystemInfo queries Redfish for system details (CPU, memory, GPUs, NICs).
func fetchRedfishSystemInfo(ctx context.Context, httpClient *http.Client, baseURL, user, pass string, result *BMCProbeResult) {
	// Get system info
	sysReq, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/redfish/v1/Systems/1", nil)
	if err != nil {
		return
	}
	sysReq.SetBasicAuth(user, pass)

	sysResp, err := httpClient.Do(sysReq)
	if err == nil && sysResp.StatusCode == 200 {
		body, _ := io.ReadAll(io.LimitReader(sysResp.Body, maxRedfishBody))
		sysResp.Body.Close()
		var system redfishSystem
		if json.Unmarshal(body, &system) == nil {
			result.Hostname = system.HostName
			result.CPUs = system.ProcessorSummary.Count
			result.MemoryGB = system.MemorySummary.TotalSystemMemoryGiB
		}
	} else if sysResp != nil {
		sysResp.Body.Close()
	}

	// Query PCIe devices for GPUs/NICs
	pciReq, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/redfish/v1/Systems/1/PCIeDevices", nil)
	if err != nil {
		return
	}
	pciReq.SetBasicAuth(user, pass)

	pciResp, err := httpClient.Do(pciReq)
	if err == nil && pciResp.StatusCode == 200 {
		body, _ := io.ReadAll(io.LimitReader(pciResp.Body, maxRedfishBody))
		pciResp.Body.Close()
		var collection redfishCollection
		if json.Unmarshal(body, &collection) == nil {
			for _, member := range collection.Members {
				if !strings.HasPrefix(member.ID, "/") || strings.Contains(member.ID, "://") {
					continue
				}
				devReq, devErr := http.NewRequestWithContext(ctx, "GET", baseURL+member.ID, nil)
				if devErr != nil {
					continue
				}
				devReq.SetBasicAuth(user, pass)
				devResp, devErr := httpClient.Do(devReq)
				if devErr != nil || devResp.StatusCode != 200 {
					if devResp != nil {
						devResp.Body.Close()
					}
					continue
				}
				devBody, _ := io.ReadAll(io.LimitReader(devResp.Body, maxRedfishBody))
				devResp.Body.Close()

				var device redfishPCIeDevice
				if json.Unmarshal(devBody, &device) == nil {
					classifyBMCPCIDevice(result, device)
				}
			}
		}
	} else if pciResp != nil {
		pciResp.Body.Close()
	}
}

// classifyBMCPCIDevice categorizes a PCIe device as GPU or NIC.
func classifyBMCPCIDevice(result *BMCProbeResult, device redfishPCIeDevice) {
	switch device.DeviceClass {
	case "DisplayController", "ProcessingAccelerator":
		if device.Manufacturer == "NVIDIA" || device.Manufacturer == "NVIDIA Corporation" {
			for i, g := range result.GPUs {
				if g.Model == device.Model {
					result.GPUs[i].Count++
					return
				}
			}
			result.GPUs = append(result.GPUs, DiscoveredGPU{
				Model: device.Model,
				Count: 1,
			})
		}
	case "NetworkController":
		nicType := "ethernet"
		if device.Manufacturer == "Mellanox" || device.Manufacturer == "NVIDIA" {
			if strings.Contains(device.Model, "InfiniBand") || strings.Contains(device.Model, "ConnectX") {
				nicType = "infiniband"
			}
		}
		result.NICs = append(result.NICs, DiscoveredNIC{
			Name: device.Model,
			Type: nicType,
		})
	}
}

// probeIPMIDiscover attempts IPMI detection via ASF Presence Ping on UDP port 623.
func probeIPMIDiscover(ctx context.Context, ip string, timeout time.Duration) (*BMCProbeResult, error) {
	conn, err := net.DialTimeout("udp", net.JoinHostPort(ip, ipmiPort), tcpDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("IPMI port %s not reachable on %s", ipmiPort, ip)
	}
	defer conn.Close()

	// ASF Presence Ping (RMCP)
	asfPing := []byte{
		0x06, 0x00, 0xff, 0x06, // RMCP header
		0x00, 0x00, 0x11, 0xbe, // ASF header
		0x80, 0x00, 0x00, 0x00, // IANA Enterprise Number (ASF)
		0x00, 0x00, 0x00, 0x00, // Message Type = Presence Ping
	}

	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}

	if _, err := conn.Write(asfPing); err != nil {
		return nil, fmt.Errorf("IPMI write failed on %s: %w", ip, err)
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return nil, fmt.Errorf("no IPMI response from %s", ip)
	}

	return &BMCProbeResult{Protocol: "ipmi"}, nil
}
