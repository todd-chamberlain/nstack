package kube

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const pollInterval = 5 * time.Second

// WaitForDeployment polls until a Deployment has all replicas ready or the
// context times out.
func (c *Client) WaitForDeployment(ctx context.Context, namespace, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		dep, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("getting deployment %s/%s: %w", namespace, name, err)
		}

		desired := int32(1)
		if dep.Spec.Replicas != nil {
			desired = *dep.Spec.Replicas
		}

		if dep.Status.ReadyReplicas >= desired && dep.Status.UpdatedReplicas >= desired {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for deployment %s/%s: %d/%d ready",
				namespace, name, dep.Status.ReadyReplicas, desired)
		case <-time.After(pollInterval):
		}
	}
}

// WaitForStatefulSet polls until a StatefulSet has all replicas ready or the
// context times out.
func (c *Client) WaitForStatefulSet(ctx context.Context, namespace, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		sts, err := c.clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("getting statefulset %s/%s: %w", namespace, name, err)
		}

		desired := int32(1)
		if sts.Spec.Replicas != nil {
			desired = *sts.Spec.Replicas
		}

		if sts.Status.ReadyReplicas >= desired && sts.Status.UpdatedReplicas >= desired {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for statefulset %s/%s: %d/%d ready",
				namespace, name, sts.Status.ReadyReplicas, desired)
		case <-time.After(pollInterval):
		}
	}
}

// WaitForPodsReady polls until all pods matching the label selector are ready,
// or the context times out. Returns the count of ready and total pods.
func (c *Client) WaitForPodsReady(ctx context.Context, namespace, labelSelector string, timeout time.Duration) (ready, total int, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return 0, 0, fmt.Errorf("listing pods in %s with selector %s: %w", namespace, labelSelector, err)
		}

		total = len(pods.Items)
		ready = 0
		for _, pod := range pods.Items {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == "Ready" && cond.Status == "True" {
					ready++
					break
				}
			}
		}

		if total > 0 && ready == total {
			return ready, total, nil
		}

		select {
		case <-ctx.Done():
			return ready, total, fmt.Errorf("timed out waiting for pods in %s (selector=%s): %d/%d ready",
				namespace, labelSelector, ready, total)
		case <-time.After(pollInterval):
		}
	}
}
