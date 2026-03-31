package discover

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// siteConfig is the YAML structure for generated config.
type siteConfig struct {
	Version string                    `yaml:"version"`
	Sites   map[string]*siteEntry     `yaml:"sites"`
}

type siteEntry struct {
	Profile    string     `yaml:"profile"`
	Kubeconfig string     `yaml:"kubeconfig"`
	Nodes      []nodeEntry `yaml:"nodes,omitempty"`
}

type nodeEntry struct {
	Name string      `yaml:"name"`
	IP   string      `yaml:"ip"`
	Role string      `yaml:"role,omitempty"`
	GPUs []gpuEntry  `yaml:"gpus,omitempty"`
}

type gpuEntry struct {
	Model string `yaml:"model"`
	Count int    `yaml:"count"`
}

// GenerateConfig takes classified hosts and generates a YAML config string.
func GenerateConfig(hosts []DiscoveredHost, opts ScanOptions) (string, error) {
	if len(hosts) == 0 {
		return "", fmt.Errorf("no hosts to generate config for")
	}

	recs := GroupHosts(hosts)
	cfg := &siteConfig{
		Version: "v1",
		Sites:   make(map[string]*siteEntry),
	}

	for _, rec := range recs {
		site := &siteEntry{
			Profile:    rec.Profile,
			Kubeconfig: kubeconfigPlaceholder(rec),
		}

		for i, h := range rec.Hosts {
			name := h.Hostname
			if name == "" {
				name = fmt.Sprintf("node-%02d", i+1)
			}

			node := nodeEntry{
				Name: name,
				IP:   h.IP,
				Role: nodeRole(i, len(rec.Hosts)),
			}

			for _, g := range h.GPUs {
				node.GPUs = append(node.GPUs, gpuEntry{
					Model: g.Model,
					Count: g.Count,
				})
			}

			site.Nodes = append(site.Nodes, node)
		}

		cfg.Sites[rec.Name] = site
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return "", fmt.Errorf("marshaling config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("closing encoder: %w", err)
	}

	// Append comments with recommended commands
	result := buf.String()
	result += "\n"
	for _, rec := range recs {
		ipList := make([]string, 0, len(rec.Hosts))
		for _, h := range rec.Hosts {
			ipList = append(ipList, h.IP)
		}
		result += fmt.Sprintf("# %s (%s): %s\n", rec.Name, strings.Join(ipList, ", "), rec.Summary)
		result += fmt.Sprintf("#   nstack deploy --site %s --from %s\n", rec.Name, rec.FromStage)
	}

	return result, nil
}

// kubeconfigPlaceholder returns a kubeconfig path hint based on entry point.
func kubeconfigPlaceholder(rec SiteRecommendation) string {
	switch rec.EntryPoint {
	case "k8s-ready":
		return fmt.Sprintf("~/.kube/%s.yaml", rec.Name)
	default:
		return fmt.Sprintf("~/.kube/%s.yaml  # TODO: set after K8s bootstrap", rec.Name)
	}
}

// nodeRole assigns a role based on position: first node is server, rest are workers.
func nodeRole(index, _ int) string {
	if index == 0 {
		return "server"
	}
	return "worker"
}
