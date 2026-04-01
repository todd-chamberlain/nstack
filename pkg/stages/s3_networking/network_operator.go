package s3_networking

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

const (
	networkOperatorChart     = "nvidia/network-operator"
	networkOperatorNamespace = "nvidia-network-operator"
	networkOperatorRelease   = "network-operator"
	networkOperatorVersion   = "25.7.0"

	nicClusterPolicyName = "nic-cluster-policy"
)

// installNetworkOperator deploys the NVIDIA Network Operator via its Helm chart
// and then creates/updates the NicClusterPolicy CR to configure OFED, RDMA,
// Multus, and other network components.
//
// Starting with chart v24.10.0, the chart only deploys the operator controller.
// The NicClusterPolicy CR (which drives all component deployments) must be
// created separately.
func installNetworkOperator(ctx context.Context, hc *helm.Client, kc *kube.Client, site *config.Site, profile *config.Profile, printer *output.Printer) error {
	printer.Debugf("installing %s", networkOperatorRelease)

	if err := hc.AddRepo(helm.NVIDIARepoName, helm.NVIDIARepoURL); err != nil {
		return fmt.Errorf("adding network-operator repo: %w", err)
	}

	// Load chart values (operator-level only: nfd, sriovNetworkOperator).
	var overrides map[string]interface{}
	if site != nil && site.Overrides != nil {
		overrides = site.Overrides["network-operator"]
	}
	chartValues, err := helm.LoadChartValues("network-operator", "", overrides)
	if err != nil {
		return fmt.Errorf("loading network-operator values: %w", err)
	}

	if err := hc.UpgradeOrInstall(
		ctx,
		networkOperatorRelease,
		networkOperatorChart,
		networkOperatorNamespace,
		chartValues,
		helm.WithVersion(config.ResolveVersion(site, "network-operator", networkOperatorVersion)),
		helm.WithCreateNamespace(),
		helm.WithWait(),
		helm.WithTimeout(10*time.Minute),
	); err != nil {
		return fmt.Errorf("installing network-operator chart: %w", err)
	}

	// Create/update the NicClusterPolicy CR.
	fabric := fabricType(site, profile)
	if err := applyNicClusterPolicy(ctx, kc, fabric, site, printer); err != nil {
		return fmt.Errorf("applying NicClusterPolicy: %w", err)
	}

	return nil
}

// applyNicClusterPolicy creates or updates the NicClusterPolicy CR that
// configures OFED drivers, RDMA device plugin, Multus, NVIPAM, and
// optionally IB Kubernetes and IPoIB for InfiniBand fabrics.
func applyNicClusterPolicy(ctx context.Context, kc *kube.Client, fabric string, site *config.Site, printer *output.Printer) error {
	spec := buildNicClusterPolicySpec(fabric, site)

	policy := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mellanox.com/v1alpha1",
			"kind":       "NicClusterPolicy",
			"metadata": map[string]interface{}{
				"name": nicClusterPolicyName,
			},
			"spec": spec,
		},
	}

	gvr := schema.GroupVersionResource{
		Group:    "mellanox.com",
		Version:  "v1alpha1",
		Resource: "nicclusterpolicies",
	}

	data, err := json.Marshal(policy.Object)
	if err != nil {
		return fmt.Errorf("marshaling NicClusterPolicy: %w", err)
	}

	// NicClusterPolicy is cluster-scoped.
	_, err = kc.DynamicClient().Resource(gvr).Patch(
		ctx,
		nicClusterPolicyName,
		types.ApplyPatchType,
		data,
		metav1.PatchOptions{
			FieldManager: "nstack",
			Force:        boolPtr(true),
		},
	)
	if err != nil {
		return fmt.Errorf("applying NicClusterPolicy CR: %w", err)
	}

	printer.Debugf("applied NicClusterPolicy %s (fabric=%s)", nicClusterPolicyName, fabric)
	return nil
}

// buildNicClusterPolicySpec constructs the spec for the NicClusterPolicy CR
// based on the fabric type and any site-level overrides.
func buildNicClusterPolicySpec(fabric string, site *config.Site) map[string]interface{} {
	spec := map[string]interface{}{
		// OFED (DOCA) driver
		"ofedDriver": map[string]interface{}{
			"image":      "doca-driver",
			"repository": "nvcr.io/nvidia/mellanox",
			"version":    "25.04-0.6.1.0",
			"upgradePolicy": map[string]interface{}{
				"autoUpgrade":       true,
				"maxParallelUpgrades": 1,
				"drain": map[string]interface{}{
					"enable":         true,
					"force":          true,
					"deleteEmptyDir": true,
					"timeoutSeconds": 300,
				},
			},
		},

		// RDMA shared device plugin
		"rdmaSharedDevicePlugin": map[string]interface{}{
			"image":      "k8s-rdma-shared-dev-plugin",
			"repository": "nvcr.io/nvidia/mellanox",
			"version":    "network-operator-v25.7.0",
			"config":     `{"configList":[{"resourceName":"rdma_shared_device_a","rdmaHcaMax":63,"selectors":{"vendors":["15b3"]}}]}`,
		},

		// NVIDIA IPAM
		"nvIpam": map[string]interface{}{
			"image":         "nvidia-k8s-ipam",
			"repository":    "nvcr.io/nvidia/mellanox",
			"version":       "network-operator-v25.7.0",
			"enableWebhook": false,
		},

		// Secondary network (Multus + CNI plugins)
		"secondaryNetwork": map[string]interface{}{
			"multus": map[string]interface{}{
				"image":      "multus-cni",
				"repository": "ghcr.io/k8snetworkplumbingwg",
				"version":    "v4.1.0-thick",
			},
			"cniPlugins": map[string]interface{}{
				"image":      "plugins",
				"repository": "nvcr.io/nvidia/mellanox",
				"version":    "network-operator-v25.7.0",
			},
		},
	}

	// InfiniBand-specific: add IB Kubernetes and IPoIB
	if fabric == "infiniband" {
		spec["ibKubernetes"] = map[string]interface{}{
			"image":                 "ib-kubernetes",
			"repository":           "nvcr.io/nvidia/mellanox",
			"version":              "network-operator-v25.7.0",
			"periodicUpdateSeconds": 5,
			"pKeyGUIDPoolRangeStart": "02:00:00:00:00:00:00:00",
			"pKeyGUIDPoolRangeEnd":   "02:FF:FF:FF:FF:FF:FF:FF",
			"ufmSecret":             "",
		}
		sn := spec["secondaryNetwork"].(map[string]interface{})
		sn["ipoib"] = map[string]interface{}{
			"image":      "ipoib-cni",
			"repository": "nvcr.io/nvidia/mellanox",
			"version":    "network-operator-v25.7.0",
		}
	}

	// Merge site-level overrides for the NicClusterPolicy spec.
	if site != nil && site.Overrides != nil {
		if policyOverrides, ok := site.Overrides["nic-cluster-policy"]; ok {
			spec = helm.MergeValues(spec, policyOverrides)
		}
	}

	return spec
}

func boolPtr(b bool) *bool { return &b }

// fabricType returns the RDMA fabric type from site or profile configuration.
// Site-level Fabric.Type takes precedence over profile-level Networking.Fabric.
func fabricType(site *config.Site, profile *config.Profile) string {
	if site != nil && site.Fabric != nil && site.Fabric.Type != "" {
		return site.Fabric.Type
	}
	if profile != nil {
		return profile.Networking.Fabric
	}
	return ""
}

// hasFabric returns true if a high-performance network fabric is configured.
func hasFabric(site *config.Site, profile *config.Profile) bool {
	ft := fabricType(site, profile)
	return ft != "" && ft != "none" && ft != "ethernet"
}
