package s1_provision

import (
	"context"
	"fmt"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

var bmhGVR = schema.GroupVersionResource{
	Group:    "metal3.io",
	Version:  "v1alpha1",
	Resource: "baremetalhosts",
}

// createBareMetalHost creates a BareMetalHost CRD for a node.
// The BMH resource tells Metal3/Ironic about the physical server and how to manage it.
func createBareMetalHost(ctx context.Context, kc *kube.Client, node config.Node, namespace string, imageURL string, printer *output.Printer) error {
	printer.Debugf("creating BareMetalHost for %s (BMC: %s)", node.Name, node.BMC.IP)

	bmh := &unstructured.Unstructured{}
	bmh.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "metal3.io",
		Version: "v1alpha1",
		Kind:    "BareMetalHost",
	})
	bmh.SetName(node.Name)
	bmh.SetNamespace(namespace)

	spec := map[string]interface{}{
		"online": true,
		"bmc": map[string]interface{}{
			"address":         formatBMCAddress(node.BMC),
			"credentialsName": node.Name + "-bmc-credentials",
		},
		"bootMACAddress": "",
		"image": map[string]interface{}{
			"url":      imageURL,
			"checksum": "",
		},
		"automatedCleaningMode": "disabled",
	}

	bmh.Object["spec"] = spec

	dc := kc.DynamicClient()
	_, err := dc.Resource(bmhGVR).Namespace(namespace).Create(ctx, bmh, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			printer.Debugf("BareMetalHost %s already exists", node.Name)
			return nil
		}
		return fmt.Errorf("creating BareMetalHost %s: %w", node.Name, err)
	}

	return nil
}

// formatBMCAddress builds the BMC address string for Metal3.
// Redfish: "redfish://192.168.1.10/redfish/v1/Systems/1"
// IPMI: "ipmi://192.168.1.10"
func formatBMCAddress(bmc *config.BMCConfig) string {
	if bmc == nil {
		return ""
	}
	switch bmc.Protocol {
	case "redfish":
		return fmt.Sprintf("redfish://%s/redfish/v1/Systems/1", bmc.IP)
	case "ipmi":
		return fmt.Sprintf("ipmi://%s", bmc.IP)
	default:
		return fmt.Sprintf("ipmi://%s", bmc.IP)
	}
}

// createBMCSecret creates a Kubernetes Secret with BMC credentials for a node.
// The credentials reference in the BMH spec points to this secret.
func createBMCSecret(ctx context.Context, kc *kube.Client, node config.Node, namespace string, printer *output.Printer) error {
	printer.Debugf("creating BMC secret for %s", node.Name)

	cs := kc.Clientset()

	// Resolve credentials from node BMC config.
	var username, password string
	if node.BMC != nil && node.BMC.Credentials != "" {
		var err error
		username, password, err = resolveCredentials(node.BMC.Credentials)
		if err != nil {
			return fmt.Errorf("resolving BMC credentials for %s: %w", node.Name, err)
		}
	} else {
		return fmt.Errorf("BMC credentials for %s: not configured (set node.bmc.credentials)", node.Name)
	}

	if password == "" {
		return fmt.Errorf("BMC credentials for %s: password is empty (configure node.bmc.credentials)", node.Name)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      node.Name + "-bmc-credentials",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "nstack",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"username": username,
			"password": password,
		},
	}

	_, err := cs.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			printer.Debugf("BMC secret for %s already exists", node.Name)
			return nil
		}
		return fmt.Errorf("creating BMC secret for %s: %w", node.Name, err)
	}

	return nil
}

// resolveCredentials resolves a credential reference.
// Supports: "env://VAR_NAME", "file:///path", "user:pass", or a single password value.
func resolveCredentials(ref string) (username, password string, err error) {
	if ref == "" {
		return "", "", fmt.Errorf("BMC credentials not configured")
	}
	if strings.HasPrefix(ref, "env://") {
		envVar := strings.TrimPrefix(ref, "env://")
		val := os.Getenv(envVar)
		parts := strings.SplitN(val, ":", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
		return "admin", val, nil
	}
	if strings.HasPrefix(ref, "file://") {
		path := strings.TrimPrefix(ref, "file://")
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", "", fmt.Errorf("reading credentials file %s: %w", path, readErr)
		}
		parts := strings.SplitN(strings.TrimSpace(string(data)), ":", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
		return "admin", strings.TrimSpace(string(data)), nil
	}
	// Plain user:pass format.
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}
	return "admin", ref, nil
}
