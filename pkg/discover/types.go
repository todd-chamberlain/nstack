package discover

// DiscoveredHost represents a host found during network discovery.
type DiscoveredHost struct {
	IP              string          `json:"ip"`
	Hostname        string          `json:"hostname,omitempty"`
	IsPhysical      bool            `json:"isPhysical"`
	VirtType        string          `json:"virtType,omitempty"`    // "none", "kvm", "vmware", "docker", etc.
	OS              string          `json:"os,omitempty"`          // "Ubuntu 24.04", etc.
	HasBMC          bool            `json:"hasBmc"`
	BMCType         string          `json:"bmcType,omitempty"`     // "redfish", "ipmi"
	HasSSH          bool            `json:"hasSsh"`
	HasK8s          bool            `json:"hasK8s"`
	K8sDistro       string          `json:"k8sDistro,omitempty"`   // "k3s", "kubeadm", "eks", etc.
	K8sVersion      string          `json:"k8sVersion,omitempty"`
	GPUs            []DiscoveredGPU `json:"gpus,omitempty"`
	CPUCores        int             `json:"cpuCores,omitempty"`
	MemoryGB        int             `json:"memoryGb,omitempty"`
	NICs            []DiscoveredNIC `json:"nics,omitempty"`
	EntryPoint      string          `json:"entryPoint"`       // "bare-metal", "needs-k8s", "k8s-ready"
	RecommendedStages string        `json:"recommendedStages"` // "0-6", "2-6", "4-6"
}

// DiscoveredGPU describes a GPU model found on a host.
type DiscoveredGPU struct {
	Model string `json:"model"`
	Count int    `json:"count"`
	VRAM  string `json:"vram,omitempty"`
}

// DiscoveredNIC describes a network interface found on a host.
type DiscoveredNIC struct {
	Name  string `json:"name"`
	Speed string `json:"speed,omitempty"`
	Type  string `json:"type"` // "ethernet", "infiniband"
}

// ScanOptions configures how network discovery is performed.
type ScanOptions struct {
	Network    string // CIDR range
	SSHUser    string // SSH username
	SSHKeyPath string // Path to SSH private key
	SSHPass    string // SSH password (fallback)
	BMCUser    string // IPMI/Redfish username
	BMCPass    string // IPMI/Redfish password
	Timeout    int    // Per-host timeout in seconds
	Workers    int    // Concurrent scan workers
}
