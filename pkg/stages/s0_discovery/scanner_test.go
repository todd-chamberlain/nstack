package s0_discovery

import (
	"context"
	"net"
	"testing"
)

func TestExpandCIDR_Valid(t *testing.T) {
	ips, err := expandCIDR("10.0.0.0/30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// /30 has 4 IPs total: .0 (network), .1, .2, .3 (broadcast)
	// After stripping network + broadcast, we expect .1 and .2
	expected := []string{"10.0.0.1", "10.0.0.2"}
	if len(ips) != len(expected) {
		t.Fatalf("expected %d IPs, got %d: %v", len(expected), len(ips), ips)
	}
	for i, ip := range ips {
		if ip != expected[i] {
			t.Errorf("ips[%d] = %s, want %s", i, ip, expected[i])
		}
	}
}

func TestExpandCIDR_Single(t *testing.T) {
	ips, err := expandCIDR("10.0.0.5/32")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// /32 is a single host; only 1 IP, no network/broadcast to strip
	if len(ips) != 1 {
		t.Fatalf("expected 1 IP, got %d: %v", len(ips), ips)
	}
	if ips[0] != "10.0.0.5" {
		t.Errorf("ips[0] = %s, want 10.0.0.5", ips[0])
	}
}

func TestExpandCIDR_Invalid(t *testing.T) {
	_, err := expandCIDR("not-a-cidr")
	if err == nil {
		t.Fatal("expected error for invalid CIDR, got nil")
	}
}

func TestExpandCIDR_Slash31(t *testing.T) {
	ips, err := expandCIDR("10.0.0.0/31")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// /31 has 2 IPs: point-to-point link, no broadcast stripping
	if len(ips) != 2 {
		t.Fatalf("expected 2 IPs, got %d: %v", len(ips), ips)
	}
}

func TestClassifyPCIDevice_GPU(t *testing.T) {
	node := &DiscoveredNode{}
	dev := redfishPCIeDevice{
		Name:         "NVIDIA A100",
		Manufacturer: "NVIDIA Corporation",
		Model:        "A100-SXM4-80GB",
		DeviceClass:  "DisplayController",
	}
	classifyPCIDevice(node, dev)

	if len(node.GPUs) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(node.GPUs))
	}
	if node.GPUs[0].Model != "A100-SXM4-80GB" {
		t.Errorf("GPU model = %s, want A100-SXM4-80GB", node.GPUs[0].Model)
	}
	if node.GPUs[0].Count != 1 {
		t.Errorf("GPU count = %d, want 1", node.GPUs[0].Count)
	}
}

func TestClassifyPCIDevice_GPU_Duplicate(t *testing.T) {
	node := &DiscoveredNode{}
	dev := redfishPCIeDevice{
		Name:         "NVIDIA H100",
		Manufacturer: "NVIDIA",
		Model:        "H100-SXM5-80GB",
		DeviceClass:  "ProcessingAccelerator",
	}
	classifyPCIDevice(node, dev)
	classifyPCIDevice(node, dev)

	if len(node.GPUs) != 1 {
		t.Fatalf("expected 1 GPU entry (deduplicated), got %d", len(node.GPUs))
	}
	if node.GPUs[0].Count != 2 {
		t.Errorf("GPU count = %d, want 2", node.GPUs[0].Count)
	}
}

func TestClassifyPCIDevice_NIC(t *testing.T) {
	node := &DiscoveredNode{}
	dev := redfishPCIeDevice{
		Name:         "Intel X710",
		Manufacturer: "Intel",
		Model:        "X710-DA2",
		DeviceClass:  "NetworkController",
	}
	classifyPCIDevice(node, dev)

	if len(node.NICs) != 1 {
		t.Fatalf("expected 1 NIC, got %d", len(node.NICs))
	}
	if node.NICs[0].Type != "ethernet" {
		t.Errorf("NIC type = %s, want ethernet", node.NICs[0].Type)
	}
}

func TestClassifyPCIDevice_IB(t *testing.T) {
	node := &DiscoveredNode{}
	dev := redfishPCIeDevice{
		Name:         "ConnectX-7",
		Manufacturer: "Mellanox",
		Model:        "ConnectX-7 InfiniBand NDR",
		DeviceClass:  "NetworkController",
	}
	classifyPCIDevice(node, dev)

	if len(node.NICs) != 1 {
		t.Fatalf("expected 1 NIC, got %d", len(node.NICs))
	}
	if node.NICs[0].Type != "infiniband" {
		t.Errorf("NIC type = %s, want infiniband", node.NICs[0].Type)
	}
}

func TestClassifyPCIDevice_ConnectX_NVIDIA(t *testing.T) {
	node := &DiscoveredNode{}
	dev := redfishPCIeDevice{
		Name:         "ConnectX-7",
		Manufacturer: "NVIDIA",
		Model:        "ConnectX-7 400GbE",
		DeviceClass:  "NetworkController",
	}
	classifyPCIDevice(node, dev)

	if len(node.NICs) != 1 {
		t.Fatalf("expected 1 NIC, got %d", len(node.NICs))
	}
	// ConnectX without "InfiniBand" in model should still be infiniband type
	// because ConnectX cards are dual-protocol
	if node.NICs[0].Type != "infiniband" {
		t.Errorf("NIC type = %s, want infiniband", node.NICs[0].Type)
	}
}

func TestScanNetwork_EmptyCIDR(t *testing.T) {
	_, err := ScanNetwork(context.Background(), "not-a-cidr", BMCCredentials{}, 4)
	if err == nil {
		t.Fatal("expected error for invalid CIDR, got nil")
	}
}

func TestScanNetwork_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result, err := ScanNetwork(ctx, "10.0.0.0/30", BMCCredentials{}, 4)
	// Should return quickly with context error or empty result
	if err != nil && err != context.Canceled {
		// Some IPs may have been enqueued before cancellation
		t.Logf("got non-cancel error (acceptable): %v", err)
	}
	if result != nil {
		t.Logf("got %d nodes from cancelled scan", len(result.Nodes))
	}
}

func TestIncrementIP(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"10.0.0.1", "10.0.0.2"},
		{"10.0.0.255", "10.0.1.0"},
		{"10.0.255.255", "10.1.0.0"},
		{"0.0.0.0", "0.0.0.1"},
	}

	for _, tt := range tests {
		ip := net.ParseIP(tt.input).To4()
		if ip == nil {
			t.Fatalf("failed to parse IP %s", tt.input)
		}
		incrementIP(ip)
		got := ip.String()
		if got != tt.expected {
			t.Errorf("incrementIP(%s) = %s, want %s", tt.input, got, tt.expected)
		}
	}
}
