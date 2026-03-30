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
	Cluster    *ClusterConfig                    `yaml:"cluster,omitempty"`
	Overlay    *OverlayConfig                    `yaml:"overlay,omitempty"`
	Fabric     *FabricConfig                     `yaml:"fabric,omitempty"`
	Federation *FederationConfig                 `yaml:"federation,omitempty"`
	Nodes      []Node                            `yaml:"nodes,omitempty"`
	Overrides  map[string]map[string]interface{} `yaml:"overrides,omitempty"`
	Hooks      map[string]*HookConfig            `yaml:"hooks,omitempty"`
	Versions   map[string]string                 `yaml:"versions,omitempty"`
}

// FederationConfig defines multi-site Slurm federation settings.
type FederationConfig struct {
	// Name of the federation (shared across all federated sites)
	Name string `yaml:"name"`
	// Features are cluster-level features for job routing (e.g., "site-us-east,has-imagenet,gpu")
	Features []string `yaml:"features,omitempty"`
	// Accounting configures the shared slurmdbd for federation
	Accounting *AccountingConfig `yaml:"accounting,omitempty"`
	// Telemetry configures cross-site monitoring
	Telemetry *TelemetryConfig `yaml:"telemetry,omitempty"`
}

// AccountingConfig defines Slurm accounting database settings.
type AccountingConfig struct {
	// Host is the slurmdbd hostname (can be a Tailscale MagicDNS name)
	Host string `yaml:"host"`
	// Port is the slurmdbd port (default 6819)
	Port int `yaml:"port,omitempty"`
	// Deploy controls whether NStack deploys slurmdbd on this site
	Deploy bool `yaml:"deploy"`
	// Database connection for slurmdbd (only used when Deploy=true)
	Database *DatabaseConfig `yaml:"database,omitempty"`
}

// DatabaseConfig defines the MariaDB/MySQL connection for slurmdbd.
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port,omitempty"`
	Name     string `yaml:"name"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

// TelemetryConfig defines cross-site monitoring settings.
type TelemetryConfig struct {
	// Type: "thanos" or "prometheus-federation" or "none"
	Type string `yaml:"type"`
	// RemoteWriteURL is the Thanos Receive endpoint for this site's Prometheus
	RemoteWriteURL string `yaml:"remoteWriteUrl,omitempty"`
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
	// CgroupV2 indicates whether the target uses cgroup v2. This is used by
	// the profile YAML selection logic — when true and distribution is k3s,
	// the k3s.yaml overlay supplies customCgroupConfig with CgroupPlugin=disabled.
	// No additional Go-level branching is needed.
	CgroupV2         bool   `yaml:"cgroupV2"`
	ContainerdSocket string `yaml:"containerdSocket"`
	StorageClass     string `yaml:"storageClass"`
	RuntimeClass     string `yaml:"runtimeClass"`
}

// ProfilePatches lists the minimal runtime patches needed for specific distributions.
// Most K3s adaptations are handled via Helm values and the patched operator fork.
type ProfilePatches struct {
	ContainerdSocketBind bool `yaml:"containerdSocketBind"`
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

// StorageConfig holds resolved storage configuration with defaults applied.
type StorageConfig struct {
	Type         string // "hostPath" or "pvc"
	BasePath     string // only for hostPath
	StorageClass string // only for pvc
}

// ClusterConfig holds the Slurm cluster identity used throughout deployment.
type ClusterConfig struct {
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
}

// ResolveCluster returns a ClusterConfig with defaults applied from the site.
// The defaults are: name="slurm1", namespace="slurm".
func ResolveCluster(site *Site) ClusterConfig {
	cc := ClusterConfig{Name: "slurm1", Namespace: "slurm"}
	if site != nil && site.Cluster != nil {
		if site.Cluster.Name != "" {
			cc.Name = site.Cluster.Name
		}
		if site.Cluster.Namespace != "" {
			cc.Namespace = site.Cluster.Namespace
		}
	}
	return cc
}

// ResolveStorage returns a StorageConfig with defaults applied from the profile.
// The defaults are: type="hostPath", basePath="/var/lib/nstack/slurm", storageClass="".
// The basePath default matches the k3s-single profile (internal/assets/profiles/k3s-single.yaml).
func ResolveStorage(profile *Profile) StorageConfig {
	sc := StorageConfig{
		Type:     "hostPath",
		BasePath: "/var/lib/nstack/slurm",
	}
	if profile == nil {
		return sc
	}
	if profile.Storage.Type != "" {
		sc.Type = profile.Storage.Type
	}
	if profile.Storage.BasePath != "" {
		sc.BasePath = profile.Storage.BasePath
	}
	if profile.Kubernetes.StorageClass != "" {
		sc.StorageClass = profile.Kubernetes.StorageClass
	}
	return sc
}
