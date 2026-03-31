package discover

import (
	"strings"
	"testing"
)

func TestClassifyHost_BareMetal(t *testing.T) {
	host := &DiscoveredHost{
		IP:     "10.0.0.13",
		HasBMC: true,
	}
	classifyHost(host)

	if host.EntryPoint != "bare-metal" {
		t.Errorf("EntryPoint = %q, want bare-metal", host.EntryPoint)
	}
	if host.RecommendedStages != "0-6" {
		t.Errorf("RecommendedStages = %q, want 0-6", host.RecommendedStages)
	}
}

func TestClassifyHost_NeedsK8s(t *testing.T) {
	host := &DiscoveredHost{
		IP:     "10.0.0.11",
		HasBMC: true,
		HasSSH: true,
		OS:     "Ubuntu 24.04",
	}
	classifyHost(host)

	if host.EntryPoint != "needs-k8s" {
		t.Errorf("EntryPoint = %q, want needs-k8s", host.EntryPoint)
	}
	if host.RecommendedStages != "2-6" {
		t.Errorf("RecommendedStages = %q, want 2-6", host.RecommendedStages)
	}
}

func TestClassifyHost_K8sReady(t *testing.T) {
	host := &DiscoveredHost{
		IP:     "10.0.0.64",
		HasSSH: true,
		HasK8s: true,
	}
	classifyHost(host)

	if host.EntryPoint != "k8s-ready" {
		t.Errorf("EntryPoint = %q, want k8s-ready", host.EntryPoint)
	}
	if host.RecommendedStages != "4-6" {
		t.Errorf("RecommendedStages = %q, want 4-6", host.RecommendedStages)
	}
}

func TestClassifyHost_BMCAndK8s(t *testing.T) {
	host := &DiscoveredHost{
		IP:     "10.0.0.20",
		HasBMC: true,
		HasSSH: true,
		HasK8s: true,
	}
	classifyHost(host)

	if host.EntryPoint != "k8s-ready" {
		t.Errorf("EntryPoint = %q, want k8s-ready (K8s takes precedence)", host.EntryPoint)
	}
	if host.RecommendedStages != "4-6" {
		t.Errorf("RecommendedStages = %q, want 4-6", host.RecommendedStages)
	}
}

func TestClassifyHost_SSHOnly(t *testing.T) {
	host := &DiscoveredHost{
		IP:     "10.0.0.30",
		HasSSH: true,
		OS:     "Ubuntu 24.04",
	}
	classifyHost(host)

	if host.EntryPoint != "needs-k8s" {
		t.Errorf("EntryPoint = %q, want needs-k8s", host.EntryPoint)
	}
}

func TestGroupHosts_Mixed(t *testing.T) {
	hosts := []DiscoveredHost{
		{
			IP:         "10.0.0.11",
			IsPhysical: true,
			HasSSH:     true,
			OS:         "Ubuntu 24.04",
			GPUs:       []DiscoveredGPU{{Model: "H100-SXM5-80GB", Count: 8}},
			EntryPoint: "needs-k8s",
		},
		{
			IP:         "10.0.0.12",
			IsPhysical: true,
			HasSSH:     true,
			OS:         "Ubuntu 24.04",
			GPUs:       []DiscoveredGPU{{Model: "H100-SXM5-80GB", Count: 8}},
			EntryPoint: "needs-k8s",
		},
		{
			IP:         "10.0.0.64",
			IsPhysical: true,
			HasSSH:     true,
			HasK8s:     true,
			K8sDistro:  "k3s",
			OS:         "Ubuntu 24.04",
			GPUs:       []DiscoveredGPU{{Model: "NVIDIA T400 4GB", Count: 2}},
			EntryPoint: "k8s-ready",
		},
	}

	recs := GroupHosts(hosts)
	if len(recs) < 2 {
		t.Fatalf("expected at least 2 site recommendations, got %d", len(recs))
	}

	// Check that H100 hosts are grouped together
	foundH100Group := false
	foundT400Group := false
	for _, rec := range recs {
		if len(rec.Hosts) == 2 && rec.EntryPoint == "needs-k8s" {
			foundH100Group = true
			if rec.Profile != "kubeadm-ha" {
				t.Errorf("H100 cluster profile = %q, want kubeadm-ha", rec.Profile)
			}
		}
		if len(rec.Hosts) == 1 && rec.EntryPoint == "k8s-ready" {
			foundT400Group = true
			if rec.Profile != "k3s-single" {
				t.Errorf("Single GPU host profile = %q, want k3s-single", rec.Profile)
			}
		}
	}
	if !foundH100Group {
		t.Error("expected a 2-host H100 group")
	}
	if !foundT400Group {
		t.Error("expected a 1-host T400 group")
	}
}

func TestGroupHosts_SingleBareMetal(t *testing.T) {
	hosts := []DiscoveredHost{
		{
			IP:         "10.0.0.13",
			HasBMC:     true,
			BMCType:    "ipmi",
			IsPhysical: false, // Unknown from BMC alone
			EntryPoint: "bare-metal",
		},
	}

	recs := GroupHosts(hosts)
	if len(recs) != 1 {
		t.Fatalf("expected 1 site recommendation, got %d", len(recs))
	}

	rec := recs[0]
	if rec.FromStage != "stage0" {
		t.Errorf("FromStage = %q, want stage0", rec.FromStage)
	}
	if !strings.Contains(rec.Summary, "BMC only") {
		t.Errorf("Summary = %q, want it to mention 'BMC only'", rec.Summary)
	}
}

func TestGroupHosts_VMWithK8s(t *testing.T) {
	hosts := []DiscoveredHost{
		{
			IP:         "10.0.0.20",
			HasSSH:     true,
			HasK8s:     true,
			K8sDistro:  "k3s",
			K8sVersion: "v1.34.1+k3s1",
			IsPhysical: false,
			VirtType:   "kvm",
			OS:         "Ubuntu 24.04",
			EntryPoint: "k8s-ready",
		},
	}

	recs := GroupHosts(hosts)
	if len(recs) != 1 {
		t.Fatalf("expected 1 site recommendation, got %d", len(recs))
	}

	rec := recs[0]
	if rec.FromStage != "stage4" {
		t.Errorf("FromStage = %q, want stage4", rec.FromStage)
	}
	if !strings.Contains(rec.Summary, "k3s running") {
		t.Errorf("Summary = %q, want it to mention 'k3s running'", rec.Summary)
	}
}

func TestGenerateConfig_MixedHosts(t *testing.T) {
	hosts := []DiscoveredHost{
		{
			IP:         "10.0.0.11",
			Hostname:   "gpu-node-01",
			IsPhysical: true,
			HasSSH:     true,
			OS:         "Ubuntu 24.04",
			GPUs:       []DiscoveredGPU{{Model: "H100-SXM5-80GB", Count: 8}},
			EntryPoint: "needs-k8s",
		},
		{
			IP:         "10.0.0.64",
			Hostname:   "homelab",
			IsPhysical: true,
			HasSSH:     true,
			HasK8s:     true,
			K8sDistro:  "k3s",
			OS:         "Ubuntu 24.04",
			GPUs:       []DiscoveredGPU{{Model: "NVIDIA T400 4GB", Count: 2}},
			EntryPoint: "k8s-ready",
		},
	}

	cfg, err := GenerateConfig(hosts, ScanOptions{})
	if err != nil {
		t.Fatalf("GenerateConfig error: %v", err)
	}

	if !strings.Contains(cfg, "version: v1") {
		t.Error("config missing version: v1")
	}
	if !strings.Contains(cfg, "gpu-node-01") {
		t.Error("config missing gpu-node-01 hostname")
	}
	if !strings.Contains(cfg, "10.0.0.11") {
		t.Error("config missing IP 10.0.0.11")
	}
	if !strings.Contains(cfg, "10.0.0.64") {
		t.Error("config missing IP 10.0.0.64")
	}
	if !strings.Contains(cfg, "nstack deploy") {
		t.Error("config missing deploy command hint")
	}
}

func TestGenerateConfig_Empty(t *testing.T) {
	_, err := GenerateConfig(nil, ScanOptions{})
	if err == nil {
		t.Error("expected error for empty hosts, got nil")
	}
}

func TestExpandCIDR_Valid(t *testing.T) {
	ips, err := expandCIDR("10.0.0.0/30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// /30 has 4 IPs; after stripping network + broadcast: .1 and .2
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

func TestParseNvidiaSMI(t *testing.T) {
	output := `GPU 0: NVIDIA H100 80GB HBM3 (UUID: GPU-abc123)
GPU 1: NVIDIA H100 80GB HBM3 (UUID: GPU-def456)
GPU 2: NVIDIA A100-SXM4-40GB (UUID: GPU-ghi789)
`
	gpus := parseNvidiaSMI(output)
	if len(gpus) < 2 {
		t.Fatalf("expected at least 2 GPU models, got %d", len(gpus))
	}

	h100Count := 0
	a100Count := 0
	for _, g := range gpus {
		if strings.Contains(g.Model, "H100") {
			h100Count = g.Count
		}
		if strings.Contains(g.Model, "A100") {
			a100Count = g.Count
		}
	}
	if h100Count != 2 {
		t.Errorf("H100 count = %d, want 2", h100Count)
	}
	if a100Count != 1 {
		t.Errorf("A100 count = %d, want 1", a100Count)
	}
}

func TestParseNvidiaSMI_Empty(t *testing.T) {
	gpus := parseNvidiaSMI("")
	if len(gpus) != 0 {
		t.Errorf("expected 0 GPUs for empty input, got %d", len(gpus))
	}
}

func TestParseOSRelease(t *testing.T) {
	content := `NAME="Ubuntu"
VERSION="24.04 LTS (Noble Numbat)"
PRETTY_NAME="Ubuntu 24.04 LTS"
ID=ubuntu
`
	os := parseOSRelease(content)
	if os != "Ubuntu 24.04 LTS" {
		t.Errorf("parseOSRelease = %q, want 'Ubuntu 24.04 LTS'", os)
	}
}

func TestParseOSRelease_Empty(t *testing.T) {
	os := parseOSRelease("")
	if os != "" {
		t.Errorf("parseOSRelease = %q, want empty", os)
	}
}

func TestParseNICs(t *testing.T) {
	output := `eth0             UP             00:11:22:33:44:55
ib0              UP             aa:bb:cc:dd:ee:ff
veth123abc       UP             11:22:33:44:55:66
`
	nics := parseNICs(output)
	if len(nics) != 2 {
		t.Fatalf("expected 2 NICs (excluding veth), got %d", len(nics))
	}
	if nics[0].Name != "eth0" || nics[0].Type != "ethernet" {
		t.Errorf("NIC[0] = %+v, want eth0/ethernet", nics[0])
	}
	if nics[1].Name != "ib0" || nics[1].Type != "infiniband" {
		t.Errorf("NIC[1] = %+v, want ib0/infiniband", nics[1])
	}
}

func TestDetectK8sDistro(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		{"v1.34.1+k3s1", "k3s"},
		{"v1.29.0-eks-abcdef", "eks"},
		{"v1.28.3-gke.1", "gke"},
		{"v1.27.5-aks-1", "aks"},
		{"v1.30.0", "kubeadm"},
	}

	for _, tt := range tests {
		got := detectK8sDistro(tt.version)
		if got != tt.want {
			t.Errorf("detectK8sDistro(%q) = %q, want %q", tt.version, got, tt.want)
		}
	}
}

func TestShortenGPUModel(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"NVIDIA H100 80GB HBM3", "h100"},
		{"A100-SXM4-80GB", "a100"},
		{"NVIDIA T400 4GB", "t400"},
		{"NVIDIA RTX A2000", "a2000"},
		{"Some Unknown GPU", "some"},
	}
	for _, tt := range tests {
		got := shortenGPUModel(tt.model)
		if got != tt.want {
			t.Errorf("shortenGPUModel(%q) = %q, want %q", tt.model, got, tt.want)
		}
	}
}

func TestNodeRole(t *testing.T) {
	if nodeRole(0, 1) != "server" {
		t.Error("single node should be server")
	}
	if nodeRole(0, 3) != "server" {
		t.Error("first of 3 should be server")
	}
	if nodeRole(1, 3) != "worker" {
		t.Error("second of 3 should be worker")
	}
}
