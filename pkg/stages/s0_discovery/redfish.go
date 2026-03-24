package s0_discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// maxRedfishBody is the maximum response body size we'll read from a Redfish BMC (1 MB).
const maxRedfishBody = 1 << 20

// probeRedfish attempts to connect to a Redfish BMC at the given address.
// It queries /redfish/v1 for system information including CPU, memory, and PCI devices (GPUs/NICs).
func probeRedfish(ctx context.Context, addr string, creds BMCCredentials, httpClient *http.Client) (*DiscoveredNode, error) {
	// First check if port 443 is open (fast TCP check)
	conn, err := net.DialTimeout("tcp", addr+":443", 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("port 443 not open on %s", addr)
	}
	conn.Close()

	baseURL := fmt.Sprintf("https://%s", addr)

	// Check Redfish service root
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/redfish/v1", nil)
	if err != nil {
		return nil, err
	}
	if creds.Username != "" {
		req.SetBasicAuth(creds.Username, creds.Password)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("redfish probe failed on %s: %w", addr, err)
	}
	// Drain the root response body (not used) so the connection can be reused.
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("redfish returned %d on %s", resp.StatusCode, addr)
	}

	// Get system information from /redfish/v1/Systems/1 (standard path)
	node := &DiscoveredNode{
		BMCAddress: addr,
		Protocol:   "redfish",
	}

	sysReq, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/redfish/v1/Systems/1", nil)
	if err != nil {
		return node, nil
	}
	if creds.Username != "" {
		sysReq.SetBasicAuth(creds.Username, creds.Password)
	}
	sysResp, err := httpClient.Do(sysReq)
	if err == nil && sysResp.StatusCode == 200 {
		body, _ := io.ReadAll(io.LimitReader(sysResp.Body, maxRedfishBody))
		sysResp.Body.Close()
		var system redfishSystem
		if json.Unmarshal(body, &system) == nil {
			node.Hostname = system.HostName
			node.CPUs = system.ProcessorSummary.Count
			node.MemoryGB = system.MemorySummary.TotalSystemMemoryGiB
			node.PowerState = system.PowerState
		}
	} else if sysResp != nil {
		sysResp.Body.Close()
	}

	// Query PCI devices for GPUs and NICs
	pciReq, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/redfish/v1/Systems/1/PCIeDevices", nil)
	if err != nil {
		return node, nil
	}
	if creds.Username != "" {
		pciReq.SetBasicAuth(creds.Username, creds.Password)
	}
	pciResp, err := httpClient.Do(pciReq)
	if err == nil && pciResp.StatusCode == 200 {
		body, _ := io.ReadAll(io.LimitReader(pciResp.Body, maxRedfishBody))
		pciResp.Body.Close()
		var pciCollection redfishCollection
		if json.Unmarshal(body, &pciCollection) == nil {
			for _, member := range pciCollection.Members {
				if !strings.HasPrefix(member.ID, "/") || strings.Contains(member.ID, "://") {
					continue // Skip potentially malicious or malformed paths
				}
				devReq, devReqErr := http.NewRequestWithContext(ctx, "GET", baseURL+member.ID, nil)
				if devReqErr != nil {
					continue
				}
				if creds.Username != "" {
					devReq.SetBasicAuth(creds.Username, creds.Password)
				}
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
					classifyPCIDevice(node, device)
				}
			}
		}
	} else if pciResp != nil {
		pciResp.Body.Close()
	}

	return node, nil
}

// Redfish data structures (subset)
type redfishSystem struct {
	HostName         string                  `json:"HostName"`
	PowerState       string                  `json:"PowerState"`
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
	Name         string `json:"Name"`
	Manufacturer string `json:"Manufacturer"`
	Model        string `json:"Model"`
	DeviceClass  string `json:"DeviceClass"` // "DisplayController", "NetworkController"
}

// classifyPCIDevice adds a GPU or NIC to the node based on the PCI device class.
func classifyPCIDevice(node *DiscoveredNode, device redfishPCIeDevice) {
	switch device.DeviceClass {
	case "DisplayController", "ProcessingAccelerator":
		// GPU — currently we track NVIDIA GPUs
		if device.Manufacturer == "NVIDIA" || device.Manufacturer == "NVIDIA Corporation" {
			// Check if we already have this model
			for i, g := range node.GPUs {
				if g.Model == device.Model {
					node.GPUs[i].Count++
					return
				}
			}
			node.GPUs = append(node.GPUs, DiscoveredGPU{
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
		node.NICs = append(node.NICs, DiscoveredNIC{
			Model: device.Model,
			Speed: "", // Not available from PCIe device info
			Type:  nicType,
		})
	}
}
