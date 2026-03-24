package s5_slurm

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

func int32Ptr(i int32) *int32 { return &i }

func TestPatchK3sConfigMaps_Creates(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, false)
	ctx := context.Background()

	// Ensure the slurm namespace exists (required for ConfigMap creation).
	if err := kc.EnsureNamespace(ctx, slurmNamespace); err != nil {
		t.Fatalf("EnsureNamespace: %v", err)
	}

	err := patchK3sConfigMaps(ctx, kc, printer)
	if err != nil {
		t.Fatalf("patchK3sConfigMaps: %v", err)
	}

	// Verify the worker-entrypoint-fix ConfigMap was created.
	cm, err := cs.CoreV1().ConfigMaps(slurmNamespace).Get(ctx, "worker-entrypoint-fix", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("worker-entrypoint-fix ConfigMap not found: %v", err)
	}
	if cm.Labels["app.kubernetes.io/managed-by"] != "nstack" {
		t.Errorf("expected managed-by label=nstack, got %s", cm.Labels["app.kubernetes.io/managed-by"])
	}
	script, ok := cm.Data["supervisord_entrypoint.sh"]
	if !ok {
		t.Fatal("expected supervisord_entrypoint.sh key in ConfigMap data")
	}
	if len(script) == 0 {
		t.Fatal("script content is empty")
	}

	// Verify the plugstack-override ConfigMap was created.
	psCM, err := cs.CoreV1().ConfigMaps(slurmNamespace).Get(ctx, "plugstack-override", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("plugstack-override ConfigMap not found: %v", err)
	}
	if psCM.Labels["app.kubernetes.io/managed-by"] != "nstack" {
		t.Errorf("expected managed-by label=nstack on plugstack-override, got %s", psCM.Labels["app.kubernetes.io/managed-by"])
	}
	if _, ok := psCM.Data["plugstack.conf"]; !ok {
		t.Fatal("expected plugstack.conf key in plugstack-override ConfigMap data")
	}
}

func TestPatchK3sConfigMaps_AlreadyExists(t *testing.T) {
	// Pre-create both ConfigMaps.
	existingEntrypoint := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "worker-entrypoint-fix",
			Namespace: slurmNamespace,
		},
		Data: map[string]string{
			"supervisord_entrypoint.sh": "old content",
		},
	}
	existingPlugstack := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "plugstack-override",
			Namespace: slurmNamespace,
		},
		Data: map[string]string{
			"plugstack.conf": "old plugstack",
		},
	}
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: slurmNamespace},
	}
	cs := fake.NewSimpleClientset(ns, existingEntrypoint, existingPlugstack)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, false)
	ctx := context.Background()

	err := patchK3sConfigMaps(ctx, kc, printer)
	if err != nil {
		t.Fatalf("patchK3sConfigMaps should not error on existing ConfigMaps: %v", err)
	}

	// Verify the entrypoint ConfigMap was updated with new content.
	cm, err := cs.CoreV1().ConfigMaps(slurmNamespace).Get(ctx, "worker-entrypoint-fix", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("worker-entrypoint-fix ConfigMap not found: %v", err)
	}
	if cm.Data["supervisord_entrypoint.sh"] == "old content" {
		t.Error("worker-entrypoint-fix ConfigMap data was not updated")
	}

	// Verify the plugstack ConfigMap was updated.
	psCM, err := cs.CoreV1().ConfigMaps(slurmNamespace).Get(ctx, "plugstack-override", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("plugstack-override ConfigMap not found: %v", err)
	}
	if psCM.Data["plugstack.conf"] == "old plugstack" {
		t.Error("plugstack-override ConfigMap data was not updated")
	}
}

func TestPatchOperatorScaleDown(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "soperator-manager",
			Namespace: soperatorNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "soperator"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "soperator"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "manager",
						Image: "soperator:latest",
					}},
				},
			},
		},
	}
	cs := fake.NewSimpleClientset(dep)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, false)
	ctx := context.Background()

	err := patchOperatorScaleDown(ctx, kc, printer)
	if err != nil {
		t.Fatalf("patchOperatorScaleDown: %v", err)
	}

	// Verify the deployment was scaled to 0.
	updated, err := cs.AppsV1().Deployments(soperatorNamespace).Get(ctx, "soperator-manager", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("deployment not found: %v", err)
	}
	if updated.Spec.Replicas == nil || *updated.Spec.Replicas != 0 {
		var replicas int32
		if updated.Spec.Replicas != nil {
			replicas = *updated.Spec.Replicas
		}
		t.Errorf("expected replicas=0, got %d", replicas)
	}
}

func TestApplyK3sPatches_NilProfile(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, false)
	ctx := context.Background()

	err := applyK3sPatches(ctx, kc, nil, printer)
	if err != nil {
		t.Fatalf("applyK3sPatches with nil profile should not error: %v", err)
	}
}

func TestApplyK3sPatches_NoPatches(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, false)
	ctx := context.Background()

	profile := &config.Profile{
		Name: "test",
		Patches: config.ProfilePatches{
			CgroupEntrypoint:  false,
			OperatorScaleDown: false,
			WorkerInitSkip:    false,
			ProcMountDefault:  false,
		},
	}

	err := applyK3sPatches(ctx, kc, profile, printer)
	if err != nil {
		t.Fatalf("applyK3sPatches with all patches false should not error: %v", err)
	}

	// Verify no ConfigMaps were created.
	cms, err := cs.CoreV1().ConfigMaps(slurmNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing ConfigMaps: %v", err)
	}
	if len(cms.Items) != 0 {
		t.Errorf("expected 0 ConfigMaps, got %d", len(cms.Items))
	}
}

func TestApplyK3sPatches_CgroupOnly(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, false)
	ctx := context.Background()

	// Ensure the namespace exists.
	if err := kc.EnsureNamespace(ctx, slurmNamespace); err != nil {
		t.Fatalf("EnsureNamespace: %v", err)
	}

	profile := &config.Profile{
		Name: "test",
		Patches: config.ProfilePatches{
			CgroupEntrypoint: true,
		},
	}

	err := applyK3sPatches(ctx, kc, profile, printer)
	if err != nil {
		t.Fatalf("applyK3sPatches with CgroupEntrypoint=true: %v", err)
	}

	// Verify both ConfigMaps were created.
	_, err = cs.CoreV1().ConfigMaps(slurmNamespace).Get(ctx, "worker-entrypoint-fix", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected worker-entrypoint-fix ConfigMap to exist: %v", err)
	}
	_, err = cs.CoreV1().ConfigMaps(slurmNamespace).Get(ctx, "plugstack-override", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected plugstack-override ConfigMap to exist: %v", err)
	}
}
