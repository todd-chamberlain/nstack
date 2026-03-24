package kube

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewClient_EmptyKubeconfig(t *testing.T) {
	// With an empty kubeconfig and no in-cluster environment,
	// NewClient should return an error rather than panic.
	_, err := NewClient("")
	if err == nil {
		t.Error("expected error when kubeconfig is empty and not in-cluster, got nil")
	}
}

func TestEnsureNamespace_Creates(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := NewClientFromInterfaces(cs, nil, nil)

	err := c.EnsureNamespace(context.Background(), "test-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ns, err := cs.CoreV1().Namespaces().Get(context.Background(), "test-ns", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("namespace not found: %v", err)
	}
	if ns.Name != "test-ns" {
		t.Errorf("expected name=test-ns, got %s", ns.Name)
	}
}

func TestEnsureNamespace_AlreadyExists(t *testing.T) {
	existing := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "existing-ns",
		},
	}
	cs := fake.NewSimpleClientset(existing)
	c := NewClientFromInterfaces(cs, nil, nil)

	err := c.EnsureNamespace(context.Background(), "existing-ns")
	if err != nil {
		t.Fatalf("expected no error for existing namespace, got: %v", err)
	}
}

func TestScaleDeployment(t *testing.T) {
	replicas := int32(2)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-deploy",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test", Image: "nginx"},
					},
				},
			},
		},
	}

	cs := fake.NewSimpleClientset(dep)
	c := NewClientFromInterfaces(cs, nil, nil)

	err := c.ScaleDeployment(context.Background(), "default", "my-deploy", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, err := cs.AppsV1().Deployments("default").Get(context.Background(), "my-deploy", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("could not get deployment: %v", err)
	}
	if updated.Spec.Replicas == nil || *updated.Spec.Replicas != 5 {
		t.Errorf("expected replicas=5, got %v", updated.Spec.Replicas)
	}
}
