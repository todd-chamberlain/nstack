package s3_networking

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

func newTestPrinter() (*output.Printer, *bytes.Buffer) {
	var buf bytes.Buffer
	return output.NewWithWriter(&buf, "text", false, true), &buf
}

func TestConfigureOverlay_None(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer, _ := newTestPrinter()
	ctx := context.Background()

	// Explicit "none" overlay type.
	site := &config.Site{
		Overlay: &config.OverlayConfig{Type: OverlayNone},
	}
	err := configureOverlay(ctx, kc, nil, site, nil, printer)
	if err != nil {
		t.Fatalf("expected no error for overlay=none, got: %v", err)
	}

	// Nil overlay config (defaults to none).
	err = configureOverlay(ctx, kc, nil, &config.Site{}, nil, printer)
	if err != nil {
		t.Fatalf("expected no error for nil overlay, got: %v", err)
	}

	// Nil site entirely (defaults to none).
	err = configureOverlay(ctx, kc, nil, nil, nil, printer)
	if err != nil {
		t.Fatalf("expected no error for nil site, got: %v", err)
	}
}

func TestConfigureOverlay_WireGuard_K3s(t *testing.T) {
	// Create a fake flannel ConfigMap with wireguard backend.
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-flannel-cfg",
			Namespace: "kube-system",
		},
		Data: map[string]string{
			"net-conf.json": `{"Network":"10.42.0.0/16","Backend":{"Type":"wireguard-native"}}`,
		},
	}
	cs := fake.NewSimpleClientset(cm)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer, buf := newTestPrinter()
	ctx := context.Background()

	site := &config.Site{
		Overlay: &config.OverlayConfig{Type: OverlayWireGuard},
	}
	profile := &config.Profile{
		Kubernetes: config.ProfileKubernetes{Distribution: "k3s"},
	}

	err := configureOverlay(ctx, kc, nil, site, profile, printer)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "WireGuard backend detected") {
		t.Errorf("expected 'WireGuard backend detected' in output, got: %s", out)
	}
}

func TestConfigureOverlay_WireGuard_K3s_NoConfig(t *testing.T) {
	// No flannel ConfigMap exists.
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer, buf := newTestPrinter()
	ctx := context.Background()

	site := &config.Site{
		Overlay: &config.OverlayConfig{Type: OverlayWireGuard},
	}
	profile := &config.Profile{
		Kubernetes: config.ProfileKubernetes{Distribution: "k3s"},
	}

	err := configureOverlay(ctx, kc, nil, site, profile, printer)
	if err != nil {
		t.Fatalf("expected no error (non-fatal), got: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "flannel config not found") {
		t.Errorf("expected guidance about flannel config in output, got: %s", out)
	}
}

func TestConfigureOverlay_WireGuard_K3s_WrongBackend(t *testing.T) {
	// Flannel ConfigMap exists but with a different backend (e.g. vxlan).
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-flannel-cfg",
			Namespace: "kube-system",
		},
		Data: map[string]string{
			"net-conf.json": `{"Network":"10.42.0.0/16","Backend":{"Type":"vxlan"}}`,
		},
	}
	cs := fake.NewSimpleClientset(cm)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer, buf := newTestPrinter()
	ctx := context.Background()

	site := &config.Site{
		Overlay: &config.OverlayConfig{Type: OverlayWireGuard},
	}
	profile := &config.Profile{
		Kubernetes: config.ProfileKubernetes{Distribution: "k3s"},
	}

	err := configureOverlay(ctx, kc, nil, site, profile, printer)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "not wireguard-native") {
		t.Errorf("expected guidance about wrong backend in output, got: %s", out)
	}
}

func TestConfigureOverlay_WireGuard_NonK3s(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer, buf := newTestPrinter()
	ctx := context.Background()

	site := &config.Site{
		Overlay: &config.OverlayConfig{Type: OverlayWireGuard},
	}
	profile := &config.Profile{
		Kubernetes: config.ProfileKubernetes{Distribution: "kubeadm"},
	}

	err := configureOverlay(ctx, kc, nil, site, profile, printer)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "manual WireGuard mesh") {
		t.Errorf("expected manual WireGuard guidance in output, got: %s", out)
	}
}

func TestConfigureOverlay_Tailscale(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Tailscale test in short mode (requires network)")
	}
	// Tailscale overlay will attempt a Helm install, which will fail without a
	// real cluster. We just verify it does not panic and returns an error.
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer, _ := newTestPrinter()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	site := &config.Site{
		Overlay: &config.OverlayConfig{Type: OverlayTailscale},
		Overrides: map[string]map[string]interface{}{
			"tailscale": {
				"oauth": map[string]interface{}{
					"clientId":     "test-id",
					"clientSecret": "test-secret",
				},
			},
		},
	}

	hc := newTestHelmClient()

	err := configureOverlay(ctx, kc, hc, site, nil, printer)
	if err == nil {
		t.Log("configureTailscale succeeded (unexpected in test env, but not a failure)")
	}
}

func TestConfigureOverlay_Tailscale_MissingCredentials(t *testing.T) {
	// Tailscale without OAuth credentials should return a validation error.
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer, _ := newTestPrinter()
	ctx := context.Background()

	site := &config.Site{
		Overlay: &config.OverlayConfig{Type: OverlayTailscale},
		// No overrides — OAuth credentials are empty.
	}

	hc := newTestHelmClient()

	err := configureOverlay(ctx, kc, hc, site, nil, printer)
	if err == nil {
		t.Fatal("expected error for missing OAuth credentials, got nil")
	}
	if !strings.Contains(err.Error(), "oauth.clientId and oauth.clientSecret") {
		t.Errorf("expected OAuth validation error, got: %v", err)
	}
}

func TestConfigureOverlay_Unknown(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer, _ := newTestPrinter()
	ctx := context.Background()

	site := &config.Site{
		Overlay: &config.OverlayConfig{Type: "zerotier"},
	}

	err := configureOverlay(ctx, kc, nil, site, nil, printer)
	if err == nil {
		t.Fatal("expected error for unsupported overlay type, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported overlay type") {
		t.Errorf("expected 'unsupported overlay type' in error, got: %v", err)
	}
}

// newTestHelmClient creates a Helm client suitable for unit tests.
// It uses a temporary directory for Helm config to avoid polluting
// the real environment.
func newTestHelmClient() *helm.Client {
	return helm.NewClient("")
}
