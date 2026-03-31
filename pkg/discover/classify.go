package discover

import (
	"fmt"
	"sort"
	"strings"
)

// classifyHost determines the entry point and recommended stages for a host.
func classifyHost(host *DiscoveredHost) {
	switch {
	case host.HasK8s:
		// K8s is running — standard NStack flow
		host.EntryPoint = "k8s-ready"
		host.RecommendedStages = "4-6"
	case host.HasSSH:
		// Has OS but no K8s — needs K8s bootstrap
		host.EntryPoint = "needs-k8s"
		host.RecommendedStages = "2-6"
	case host.HasBMC:
		// BMC only, no OS — needs OS provisioning
		host.EntryPoint = "bare-metal"
		host.RecommendedStages = "0-6"
	default:
		// Should not reach here (scanHost returns nil if all probes fail)
		host.EntryPoint = "unknown"
		host.RecommendedStages = "0-6"
	}
}

// SiteRecommendation groups similar hosts into a recommended site configuration.
type SiteRecommendation struct {
	Name       string
	Hosts      []DiscoveredHost
	EntryPoint string
	Profile    string
	FromStage  string
	Summary    string
}

// GroupHosts groups discovered hosts into site recommendations by similarity.
func GroupHosts(hosts []DiscoveredHost) []SiteRecommendation {
	type groupKey struct {
		EntryPoint string
		GPUModel   string
		OS         string
		IsPhysical bool
	}

	groups := make(map[groupKey][]DiscoveredHost)
	for _, h := range hosts {
		gpuModel := primaryGPUModel(h)
		key := groupKey{
			EntryPoint: h.EntryPoint,
			GPUModel:   gpuModel,
			OS:         h.OS,
			IsPhysical: h.IsPhysical,
		}
		groups[key] = append(groups[key], h)
	}

	var recs []SiteRecommendation
	for key, group := range groups {
		rec := SiteRecommendation{
			Hosts:      group,
			EntryPoint: key.EntryPoint,
		}

		// Generate site name
		rec.Name = generateSiteName(key.EntryPoint, key.GPUModel, key.IsPhysical, len(group))

		// Select profile
		rec.Profile = selectProfile(key.EntryPoint, key.IsPhysical, len(group))

		// Determine from-stage
		switch key.EntryPoint {
		case "bare-metal":
			rec.FromStage = "stage0"
		case "needs-k8s":
			rec.FromStage = "stage2"
		case "k8s-ready":
			rec.FromStage = "stage4"
		}

		// Build summary
		rec.Summary = buildSummary(group, key.GPUModel, key.EntryPoint)

		recs = append(recs, rec)
	}

	// Sort for stable output
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].EntryPoint != recs[j].EntryPoint {
			return recs[i].EntryPoint < recs[j].EntryPoint
		}
		return recs[i].Name < recs[j].Name
	})

	return recs
}

// primaryGPUModel returns the GPU model name for grouping, or "no-gpu".
func primaryGPUModel(h DiscoveredHost) string {
	if len(h.GPUs) == 0 {
		return "no-gpu"
	}
	return h.GPUs[0].Model
}

// generateSiteName creates a descriptive site name from host characteristics.
func generateSiteName(entryPoint, gpuModel string, isPhysical bool, count int) string {
	var parts []string

	// Prefix based on type
	if isPhysical {
		parts = append(parts, "dc")
	} else {
		parts = append(parts, "lab")
	}

	// GPU info
	if gpuModel != "no-gpu" {
		// Shorten GPU model name
		short := shortenGPUModel(gpuModel)
		parts = append(parts, short)
	}

	// Scale indicator
	if count > 1 {
		parts = append(parts, "cluster")
	} else {
		parts = append(parts, "single")
	}

	// Entry point suffix for bare metal
	if entryPoint == "bare-metal" {
		parts = append(parts, "bare")
	}

	return strings.Join(parts, "-")
}

// shortenGPUModel extracts a short identifier from a GPU model string.
func shortenGPUModel(model string) string {
	model = strings.ToLower(model)
	// Match common GPU names
	for _, name := range []string{"h100", "h200", "a100", "a6000", "a5000", "a4000", "a2000", "l40", "t400", "l4", "t4", "v100", "rtx"} {
		if strings.Contains(model, name) {
			return name
		}
	}
	// Fallback: take first word
	fields := strings.Fields(model)
	if len(fields) > 0 {
		return strings.ToLower(fields[0])
	}
	return "gpu"
}

// selectProfile chooses a deployment profile based on host characteristics.
func selectProfile(entryPoint string, isPhysical bool, count int) string {
	if count == 1 {
		return "k3s-single"
	}
	return "kubeadm-ha"
}

// buildSummary creates a human-readable summary for a site recommendation.
func buildSummary(hosts []DiscoveredHost, gpuModel, entryPoint string) string {
	var parts []string

	hostType := "VM"
	if len(hosts) > 0 && hosts[0].IsPhysical {
		hostType = "Physical"
	}
	parts = append(parts, fmt.Sprintf("%dx %s", len(hosts), hostType))

	if gpuModel != "no-gpu" && len(hosts) > 0 {
		totalGPUs := 0
		for _, h := range hosts {
			for _, g := range h.GPUs {
				totalGPUs += g.Count
			}
		}
		if totalGPUs > 0 {
			gpuPerHost := totalGPUs / len(hosts)
			if gpuPerHost > 0 && len(hosts) > 1 {
				parts = append(parts, fmt.Sprintf("%dx %s each", gpuPerHost, gpuModel))
			} else {
				parts = append(parts, fmt.Sprintf("%dx %s", totalGPUs, gpuModel))
			}
		}
	}

	switch entryPoint {
	case "bare-metal":
		parts = append(parts, "BMC only, no OS")
	case "needs-k8s":
		parts = append(parts, "needs K8s")
	case "k8s-ready":
		if len(hosts) > 0 && hosts[0].K8sDistro != "" {
			parts = append(parts, fmt.Sprintf("%s running", hosts[0].K8sDistro))
		} else {
			parts = append(parts, "K8s running")
		}
	}

	return strings.Join(parts, ", ")
}
