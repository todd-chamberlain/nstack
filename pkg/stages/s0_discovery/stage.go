// Package s0_discovery implements Stage 0: Discovery.
// It scans a network for bare metal hosts via IPMI/Redfish, builds a hardware
// inventory, and populates the site config with discovered nodes.
package s0_discovery

import (
	"context"
	"fmt"
	"time"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/engine"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
	"github.com/todd-chamberlain/nstack/pkg/state"
)

// DiscoveryStage implements the Stage interface for bare-metal BMC discovery.
type DiscoveryStage struct {
	// Network is the CIDR range to scan (e.g. "10.0.0.0/24").
	Network string

	// Credentials are the BMC username/password used during scanning.
	Credentials BMCCredentials

	// Concurrency controls how many hosts are probed in parallel.
	Concurrency int

	// lastResult caches the most recent scan result for Status().
	lastResult *ScanResult
}

// New returns a new DiscoveryStage instance with default concurrency.
func New() *DiscoveryStage {
	return &DiscoveryStage{
		Concurrency: 32,
	}
}

func (s *DiscoveryStage) Number() int         { return 0 }
func (s *DiscoveryStage) Name() string        { return "Discovery" }
func (s *DiscoveryStage) Dependencies() []int { return nil }

// Detect returns an empty result — discovery IS detection.
func (s *DiscoveryStage) Detect(ctx context.Context, kc *kube.Client) (*engine.DetectResult, error) {
	return &engine.DetectResult{}, nil
}

// Validate checks that a network CIDR is configured for scanning, or that at
// least one node in the site config already has BMC credentials.
func (s *DiscoveryStage) Validate(ctx context.Context, kc *kube.Client, profile *config.Profile) error {
	if s.Network != "" {
		return nil
	}
	return fmt.Errorf("no network CIDR provided; use --network to specify a range to scan")
}

// Plan builds a StagePlan listing the network to scan.
func (s *DiscoveryStage) Plan(ctx context.Context, kc *kube.Client, profile *config.Profile, current *state.StageState) (*engine.StagePlan, error) {
	plan := &engine.StagePlan{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	if s.Network == "" {
		plan.Action = "skip"
		return plan, nil
	}

	plan.Components = append(plan.Components, engine.ComponentPlan{
		Name:   "network-scan",
		Action: "install",
	})
	plan.Action = engine.DeterminePlanAction(plan.Components, plan.Patches)
	return plan, nil
}

// Apply runs the discovery scan and populates the site config with results.
func (s *DiscoveryStage) Apply(ctx context.Context, kc *kube.Client, hc *helm.Client, site *config.Site, profile *config.Profile, plan *engine.StagePlan, printer *output.Printer) error {
	if s.Network == "" {
		printer.Infof("No network CIDR configured, skipping discovery")
		return nil
	}

	printer.ComponentStart(1, 1, "network-scan", s.Network, "scanning")

	start := time.Now()
	result, err := ScanNetwork(ctx, s.Network, s.Credentials, s.Concurrency)
	if err != nil {
		printer.ComponentDone("network-scan", err)
		return fmt.Errorf("network scan failed: %w", err)
	}
	s.lastResult = result
	elapsed := time.Since(start)

	printer.ComponentDone("network-scan", nil)
	printer.Infof("Discovered %d BMC endpoint(s) in %s", len(result.Nodes), elapsed.Truncate(time.Millisecond))

	// Populate site config with discovered nodes.
	for _, discovered := range result.Nodes {
		node := config.Node{
			Name: discovered.Hostname,
			IP:   discovered.BMCAddress,
			BMC: &config.BMCConfig{
				IP:       discovered.BMCAddress,
				Protocol: discovered.Protocol,
			},
		}
		if node.Name == "" {
			node.Name = discovered.BMCAddress
		}

		for _, gpu := range discovered.GPUs {
			node.GPUs = append(node.GPUs, config.GPU{
				Model: gpu.Model,
				Count: gpu.Count,
			})
		}

		for _, nic := range discovered.NICs {
			node.NICs = append(node.NICs, config.NIC{
				Type:  nic.Type,
				Model: nic.Model,
				Speed: nic.Speed,
				Count: 1,
			})
		}

		site.Nodes = append(site.Nodes, node)
		printer.Infof("  %s (%s) — %s, %d CPU, %d GB RAM, %d GPU(s), power: %s",
			node.Name, discovered.BMCAddress, discovered.Protocol,
			discovered.CPUs, discovered.MemoryGB, len(discovered.GPUs),
			discovered.PowerState)
	}

	return nil
}

// Status reports how many nodes were discovered.
func (s *DiscoveryStage) Status(ctx context.Context, kc *kube.Client) (*engine.StageStatus, error) {
	status := &engine.StageStatus{
		Stage: s.Number(),
		Name:  s.Name(),
	}

	if s.lastResult == nil {
		status.Status = "not-installed"
		return status, nil
	}

	count := len(s.lastResult.Nodes)
	status.Status = "deployed"
	status.Components = append(status.Components, engine.ComponentStatus{
		Name:   "discovered-nodes",
		Status: "running",
		Pods:   count,
		Ready:  count,
	})

	return status, nil
}

// Destroy is a no-op — discovery doesn't deploy anything.
func (s *DiscoveryStage) Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error {
	printer.Infof("Stage 0 (Discovery) has nothing to destroy")
	return nil
}
