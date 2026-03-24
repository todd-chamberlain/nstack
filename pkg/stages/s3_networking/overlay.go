package s3_networking

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

// OverlayType constants
const (
	OverlayNone      = "none"
	OverlayWireGuard = "wireguard"
	OverlayTailscale = "tailscale"
)

// configureOverlay sets up the control plane overlay network.
// For K3s: documents that --flannel-backend=wireguard-native should be set (K3s config, not runtime).
// For other distros: could deploy a WireGuard mesh operator.
// For tailscale: deploys the Tailscale K8s Operator.
func configureOverlay(ctx context.Context, kc *kube.Client, hc *helm.Client, site *config.Site, profile *config.Profile, printer *output.Printer) error {
	overlayType := OverlayNone
	if site != nil && site.Overlay != nil && site.Overlay.Type != "" {
		overlayType = site.Overlay.Type
	} else if profile != nil && profile.Networking.Overlay != "" {
		overlayType = profile.Networking.Overlay
	}

	switch overlayType {
	case OverlayNone:
		printer.Debugf("no overlay configured, skipping")
		return nil

	case OverlayWireGuard:
		return configureWireGuard(ctx, kc, profile, printer)

	case OverlayTailscale:
		return configureTailscale(ctx, hc, site, printer)

	default:
		return fmt.Errorf("unsupported overlay type: %s", overlayType)
	}
}

// configureWireGuard handles WireGuard overlay setup.
// For K3s: WireGuard is built into Flannel. The user needs to set
// --flannel-backend=wireguard-native in the K3s server config.
// NStack verifies the backend is configured correctly.
func configureWireGuard(ctx context.Context, kc *kube.Client, profile *config.Profile, printer *output.Printer) error {
	if profile != nil && profile.Kubernetes.Distribution == "k3s" {
		// K3s has built-in WireGuard via Flannel.
		// Verify by checking the flannel ConfigMap for wireguard backend.
		cs := kc.Clientset()
		cm, err := cs.CoreV1().ConfigMaps("kube-system").Get(ctx, "kube-flannel-cfg", metav1.GetOptions{})
		if err != nil {
			printer.Infof("        → WireGuard: K3s flannel config not found, ensure --flannel-backend=wireguard-native is set")
			return nil // Non-fatal: user may need to reconfigure K3s
		}

		netConf, ok := cm.Data["net-conf.json"]
		if ok && strings.Contains(netConf, "wireguard") {
			printer.Infof("        → WireGuard: K3s Flannel WireGuard backend detected")
		} else {
			printer.Infof("        → WireGuard: K3s detected but Flannel backend is not wireguard-native")
			printer.Infof("          To enable: add '--flannel-backend=wireguard-native' to K3s server config")
		}
		return nil
	}

	// For non-K3s: WireGuard mesh setup would go here.
	// v0.2 scope: just document the requirement.
	printer.Infof("        → WireGuard: manual WireGuard mesh configuration required for non-K3s clusters")
	return nil
}

// configureTailscale deploys the Tailscale Kubernetes Operator for overlay networking.
func configureTailscale(ctx context.Context, hc *helm.Client, site *config.Site, printer *output.Printer) error {
	// Tailscale K8s Operator deployment
	// Repo: https://pkgs.tailscale.com/helmcharts
	// Chart: tailscale/tailscale-operator
	printer.Infof("        → Tailscale: operator deployment (requires auth key in site config)")

	err := hc.AddRepo("tailscale", "https://pkgs.tailscale.com/helmcharts")
	if err != nil {
		return fmt.Errorf("adding tailscale repo: %w", err)
	}

	values := map[string]interface{}{
		"oauth": map[string]interface{}{
			"clientId":     "", // User must provide via site overrides
			"clientSecret": "", // User must provide via site overrides
		},
	}

	// Merge with site overrides
	if site != nil && site.Overrides != nil {
		if tsOverrides, ok := site.Overrides["tailscale"]; ok {
			values = helm.MergeValues(values, tsOverrides)
		}
	}

	// Validate that OAuth credentials are provided after merging overrides.
	oauth, ok := values["oauth"].(map[string]interface{})
	if !ok || oauth["clientId"] == nil || oauth["clientId"] == "" || oauth["clientSecret"] == nil || oauth["clientSecret"] == "" {
		return fmt.Errorf("tailscale overlay requires oauth.clientId and oauth.clientSecret in site overrides['tailscale']")
	}

	return hc.UpgradeOrInstall(
		ctx,
		"tailscale-operator",
		"tailscale/tailscale-operator",
		"tailscale-system",
		values,
		helm.WithCreateNamespace(),
		helm.WithWait(),
		helm.WithTimeout(5*time.Minute),
	)
}
