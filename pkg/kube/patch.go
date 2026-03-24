package kube

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// PatchResource applies a patch to a dynamic resource identified by GVR, namespace, and name.
func (c *Client) PatchResource(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, patchType types.PatchType, data []byte) error {
	_, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(
		ctx, name, patchType, data, metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patching %s %s/%s: %w", gvr.Resource, namespace, name, err)
	}
	return nil
}

// ScaleDeployment sets the replica count of a deployment.
func (c *Client) ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) error {
	dep, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting deployment %s/%s: %w", namespace, name, err)
	}

	dep.Spec.Replicas = &replicas
	_, err = c.clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("scaling deployment %s/%s to %d: %w", namespace, name, replicas, err)
	}
	return nil
}

// EnsureNamespace creates the namespace if it does not already exist.
func (c *Client) EnsureNamespace(ctx context.Context, namespace string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}

	_, err := c.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("creating namespace %s: %w", namespace, err)
	}
	return nil
}

// patchScalePayload is a helper for building JSON merge patch payloads for scale operations.
// It is exported for potential reuse in other packages.
func patchScalePayload(replicas int32) ([]byte, error) {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"replicas": replicas,
		},
	}
	return json.Marshal(patch)
}
