package s1_provision

import (
	"context"
	"fmt"
	"time"

	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

const (
	metal3Namespace = "metal3-system"
	metal3Version   = "0.8.0"
	defaultImageURL = "https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img"
)

// deployMetal3 installs the Metal3 Baremetal Operator via its Helm chart.
// The baremetal-operator Helm chart is published at:
// https://metal3-io.github.io/baremetal-operator
func deployMetal3(ctx context.Context, hc *helm.Client, kc *kube.Client, printer *output.Printer) error {
	printer.Debugf("deploying Metal3 Baremetal Operator v%s", metal3Version)

	// Add the metal3 Helm chart repository.
	if err := hc.AddRepo("metal3", metal3ChartRepo); err != nil {
		return fmt.Errorf("adding metal3 repo: %w", err)
	}

	values := map[string]interface{}{
		"global": map[string]interface{}{
			"ironicIP": "",
		},
		"ironic": map[string]interface{}{
			"enabled":         true,
			"enableDHCP":      true,
			"enableTLS":       false,
			"enableBasicAuth": true,
		},
	}

	return hc.UpgradeOrInstall(
		metal3Release,
		metal3Chart,
		metal3Namespace,
		values,
		helm.WithVersion(metal3Version),
		helm.WithCreateNamespace(),
		helm.WithWait(),
		helm.WithTimeout(10*time.Minute),
	)
}
