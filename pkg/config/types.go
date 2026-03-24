package config

// Config is the top-level configuration for nstack.
type Config struct {
	Version string           `yaml:"version"`
	Sites   map[string]*Site `yaml:"sites"`
}

// Site defines a deployment target with its profile and infrastructure details.
type Site struct {
	Name       string                            `yaml:"-"` // Set from map key
	Profile    string                            `yaml:"profile"`
	Kubeconfig string                            `yaml:"kubeconfig"`
	Overlay    *OverlayConfig                    `yaml:"overlay,omitempty"`
	Fabric     *FabricConfig                     `yaml:"fabric,omitempty"`
	Nodes      []Node                            `yaml:"nodes,omitempty"`
	Overrides  map[string]map[string]interface{} `yaml:"overrides,omitempty"`
	Hooks      map[string]*HookConfig            `yaml:"hooks,omitempty"`
}

// OverlayConfig defines network overlay settings for cross-node communication.
type OverlayConfig struct {
	Type     string `yaml:"type"`     // "wireguard", "tailscale", "none"
	Endpoint string `yaml:"endpoint"`
}

// FabricConfig defines high-performance interconnect settings.
type FabricConfig struct {
	Type     string `yaml:"type"`     // "infiniband", "roce", "ethernet", "none"
	Rate     string `yaml:"rate"`     // "ndr", "hdr", "edr", "25g", "100g"
	Topology string `yaml:"topology"` // "fat-tree", "dragonfly", "torus"
}

// Node represents a physical or virtual machine in the cluster.
type Node struct {
	Name string     `yaml:"name"`
	IP   string     `yaml:"ip"`
	Role string     `yaml:"role"` // "server", "worker", "agent"
	BMC  *BMCConfig `yaml:"bmc,omitempty"`
	GPUs []GPU      `yaml:"gpus,omitempty"`
	NICs []NIC      `yaml:"nics,omitempty"`
	DPUs []DPU      `yaml:"dpus,omitempty"`
}

// BMCConfig holds baseboard management controller connection details.
type BMCConfig struct {
	IP          string `yaml:"ip"`
	Protocol    string `yaml:"protocol"`
	Credentials string `yaml:"credentials"`
}

// GPU describes a GPU device attached to a node.
type GPU struct {
	Model string `yaml:"model"`
	Count int    `yaml:"count"`
	VRAM  string `yaml:"vram"`
	UUID  string `yaml:"uuid,omitempty"`
}

// NIC describes a network interface on a node.
type NIC struct {
	Type  string `yaml:"type"`
	Model string `yaml:"model,omitempty"`
	Count int    `yaml:"count"`
	Speed string `yaml:"speed"`
}

// DPU describes a data processing unit on a node.
type DPU struct {
	Model string `yaml:"model"`
	Count int    `yaml:"count"`
	Mode  string `yaml:"mode"`
}

// HookConfig defines pre/post hooks for a deployment stage.
type HookConfig struct {
	PreApply  []HookEntry `yaml:"pre-apply,omitempty"`
	PostApply []HookEntry `yaml:"post-apply,omitempty"`
}

// HookEntry is a single hook action.
type HookEntry struct {
	Script  string `yaml:"script"`
	Timeout string `yaml:"timeout,omitempty"`
}

// Profile defines environment-specific defaults and behavior adjustments.
type Profile struct {
	Name        string             `yaml:"name"`
	Description string             `yaml:"description"`
	Kubernetes  ProfileKubernetes  `yaml:"kubernetes"`
	Patches     ProfilePatches     `yaml:"patches"`
	Storage     ProfileStorage     `yaml:"storage"`
	Networking  ProfileNetworking  `yaml:"networking"`
	Images      ProfileImages      `yaml:"images"`
}

// ProfileKubernetes describes the Kubernetes distribution and runtime settings.
type ProfileKubernetes struct {
	Distribution     string `yaml:"distribution"`     // k3s, kubeadm, managed, eks, gke, aks
	MultiNode        bool   `yaml:"multiNode"`
	CgroupV2         bool   `yaml:"cgroupV2"`
	ContainerdSocket string `yaml:"containerdSocket"`
	StorageClass     string `yaml:"storageClass"`
	RuntimeClass     string `yaml:"runtimeClass"`
}

// ProfilePatches lists workaround patches to apply for specific distributions.
type ProfilePatches struct {
	BusyboxRetag      bool `yaml:"busyboxRetag"`
	CgroupEntrypoint  bool `yaml:"cgroupEntrypoint"`
	OperatorScaleDown bool `yaml:"operatorScaleDown"`
	WorkerInitSkip    bool `yaml:"workerInitSkip"`
	PrologToBinTrue   bool `yaml:"prologToBinTrue"`
	ProcMountDefault  bool `yaml:"procMountDefault"`
}

// ProfileStorage defines the default storage strategy.
type ProfileStorage struct {
	Type     string `yaml:"type"`     // "hostPath" or "pvc"
	BasePath string `yaml:"basePath"` // only for hostPath
}

// ProfileNetworking defines default networking configuration.
type ProfileNetworking struct {
	Overlay string `yaml:"overlay"` // "wireguard", "tailscale", "none"
	Fabric  string `yaml:"fabric"`  // "infiniband", "roce", "none"
}

// ProfileImages defines container image registry defaults.
type ProfileImages struct {
	Registry string `yaml:"registry"`
}
