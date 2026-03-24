package s4_gpu

import (
	"context"
	"fmt"
	"time"

	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

const (
	certManagerRepo      = "https://charts.jetstack.io"
	certManagerRepoName  = "jetstack"
	certManagerChart     = "jetstack/cert-manager"
	certManagerNamespace = "cert-manager"
	certManagerRelease   = "cert-manager"
	certManagerVersion   = "v1.17.2"
)

// installCertManager deploys cert-manager via its Helm chart.
func installCertManager(ctx context.Context, hc *helm.Client, printer *output.Printer) error {
	printer.Debugf("installing %s", certManagerRelease)

	if err := hc.AddRepo(certManagerRepoName, certManagerRepo); err != nil {
		return fmt.Errorf("adding cert-manager repo: %w", err)
	}

	values := map[string]interface{}{
		"installCRDs": true,
	}

	if err := hc.UpgradeOrInstall(
		certManagerRelease,
		certManagerChart,
		certManagerNamespace,
		values,
		helm.WithVersion(certManagerVersion),
		helm.WithCreateNamespace(),
		helm.WithWait(),
		helm.WithTimeout(10*time.Minute),
	); err != nil {
		return fmt.Errorf("installing cert-manager: %w", err)
	}

	return nil
}
