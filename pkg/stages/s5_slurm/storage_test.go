package s5_slurm

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

var defaultCluster = config.ClusterConfig{Name: "slurm1", Namespace: "slurm"}

func TestCreateStorage_HostPath(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, true) // verbose for debug output
	ctx := context.Background()

	profile := &config.Profile{
		Name: "test",
		Storage: config.ProfileStorage{
			Type:     "hostPath",
			BasePath: "/storage/test-slurm",
		},
	}

	err := createStorage(ctx, kc, profile, defaultCluster, printer)
	if err != nil {
		t.Fatalf("createStorage hostPath: %v", err)
	}

	// Verify namespace was created.
	_, err = cs.CoreV1().Namespaces().Get(ctx, "slurm", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("slurm namespace not created: %v", err)
	}

	// Verify PVs were created (namespace-prefixed names).
	pvs := []string{"slurm-controller-spool-pv", "slurm-jail-pv"}
	for _, name := range pvs {
		pv, err := cs.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("PV %s not created: %v", name, err)
		}
		if pv.Spec.HostPath == nil {
			t.Errorf("PV %s: expected hostPath source", name)
		}
		if pv.Labels["app.kubernetes.io/managed-by"] != "nstack" {
			t.Errorf("PV %s: expected managed-by label=nstack", name)
		}
	}

	// Verify PVCs were created.
	pvcs := []string{"controller-spool-pvc", "jail-pvc"}
	for _, name := range pvcs {
		pvc, err := cs.CoreV1().PersistentVolumeClaims("slurm").Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("PVC %s not created: %v", name, err)
		}
		if pvc.Labels["app.kubernetes.io/managed-by"] != "nstack" {
			t.Errorf("PVC %s: expected managed-by label=nstack", name)
		}
	}

	// Verify jail-pv uses the correct path.
	jailPV, _ := cs.CoreV1().PersistentVolumes().Get(ctx, "slurm-jail-pv", metav1.GetOptions{})
	if jailPV.Spec.HostPath.Path != "/storage/test-slurm/jail" {
		t.Errorf("jail-pv: expected path=/storage/test-slurm/jail, got %s", jailPV.Spec.HostPath.Path)
	}

	// Verify jail-pvc has ReadWriteOnce access mode (hostPath cannot do RWX on multi-node).
	jailPVC, _ := cs.CoreV1().PersistentVolumeClaims("slurm").Get(ctx, "jail-pvc", metav1.GetOptions{})
	if len(jailPVC.Spec.AccessModes) == 0 || jailPVC.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Errorf("jail-pvc: expected access mode ReadWriteOnce")
	}
}

func TestCreateStorage_PVC(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, true)
	ctx := context.Background()

	profile := &config.Profile{
		Name: "test",
		Storage: config.ProfileStorage{
			Type: "pvc",
		},
		Kubernetes: config.ProfileKubernetes{
			StorageClass: "ceph-rbd",
		},
	}

	err := createStorage(ctx, kc, profile, defaultCluster, printer)
	if err != nil {
		t.Fatalf("createStorage pvc: %v", err)
	}

	// Verify PVCs were created.
	pvcs := []string{"controller-spool-pvc", "jail-pvc"}
	for _, name := range pvcs {
		pvc, err := cs.CoreV1().PersistentVolumeClaims("slurm").Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("PVC %s not created: %v", name, err)
		}
		if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "ceph-rbd" {
			t.Errorf("PVC %s: expected storageClassName=ceph-rbd", name)
		}
	}

	// Verify no PVs were created (dynamic provisioning).
	pvList, err := cs.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing PVs: %v", err)
	}
	if len(pvList.Items) != 0 {
		t.Errorf("expected 0 PVs for dynamic provisioning, got %d", len(pvList.Items))
	}
}

func TestCreateStorage_AlreadyExists(t *testing.T) {
	// Pre-create namespace and PVCs.
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "slurm"},
	}
	spoolPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "controller-spool-pvc",
			Namespace: "slurm",
		},
	}
	jailPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jail-pvc",
			Namespace: "slurm",
		},
	}
	// Also pre-create PVs for hostPath (namespace-prefixed names).
	spoolPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "slurm-controller-spool-pv"},
	}
	jailPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "slurm-jail-pv"},
	}

	cs := fake.NewSimpleClientset(ns, spoolPVC, jailPVC, spoolPV, jailPV)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, true)
	ctx := context.Background()

	profile := &config.Profile{
		Name: "test",
		Storage: config.ProfileStorage{
			Type:     "hostPath",
			BasePath: "/storage/slurm",
		},
	}

	err := createStorage(ctx, kc, profile, defaultCluster, printer)
	if err != nil {
		t.Fatalf("createStorage should not error when resources already exist: %v", err)
	}
}

func TestCreateStorage_DefaultProfile(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, true)
	ctx := context.Background()

	// nil profile should use defaults (hostPath, /var/lib/nstack/slurm).
	err := createStorage(ctx, kc, nil, defaultCluster, printer)
	if err != nil {
		t.Fatalf("createStorage with nil profile: %v", err)
	}

	// Verify PVs were created with default path (namespace-prefixed names).
	spoolPV, err := cs.CoreV1().PersistentVolumes().Get(ctx, "slurm-controller-spool-pv", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("slurm-controller-spool-pv not created: %v", err)
	}
	if spoolPV.Spec.HostPath.Path != "/var/lib/nstack/slurm/controller-spool" {
		t.Errorf("expected default path /var/lib/nstack/slurm/controller-spool, got %s", spoolPV.Spec.HostPath.Path)
	}
}

func TestCreateStorage_CustomNamespace(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, true)
	ctx := context.Background()

	cluster := config.ClusterConfig{Name: "prod", Namespace: "slurm-prod"}
	profile := &config.Profile{
		Name: "test",
		Storage: config.ProfileStorage{
			Type:     "hostPath",
			BasePath: "/storage/slurm",
		},
	}

	err := createStorage(ctx, kc, profile, cluster, printer)
	if err != nil {
		t.Fatalf("createStorage with custom namespace: %v", err)
	}

	// Verify namespace was created with custom name.
	_, err = cs.CoreV1().Namespaces().Get(ctx, "slurm-prod", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("slurm-prod namespace not created: %v", err)
	}

	// Verify PVs have the custom namespace prefix.
	_, err = cs.CoreV1().PersistentVolumes().Get(ctx, "slurm-prod-controller-spool-pv", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("slurm-prod-controller-spool-pv not created: %v", err)
	}
	_, err = cs.CoreV1().PersistentVolumes().Get(ctx, "slurm-prod-jail-pv", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("slurm-prod-jail-pv not created: %v", err)
	}

	// Verify PVCs are in the custom namespace.
	_, err = cs.CoreV1().PersistentVolumeClaims("slurm-prod").Get(ctx, "controller-spool-pvc", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("controller-spool-pvc not in slurm-prod: %v", err)
	}
}

func TestDestroyStorage(t *testing.T) {
	// Pre-create PVCs and PVs (namespace-prefixed PV names).
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "slurm"},
	}
	spoolPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "controller-spool-pvc",
			Namespace: "slurm",
		},
	}
	jailPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jail-pvc",
			Namespace: "slurm",
		},
	}
	spoolPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "slurm-controller-spool-pv"},
	}
	jailPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "slurm-jail-pv"},
	}

	cs := fake.NewSimpleClientset(ns, spoolPVC, jailPVC, spoolPV, jailPV)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, true)
	ctx := context.Background()

	err := destroyStorage(ctx, kc, defaultCluster, printer)
	if err != nil {
		t.Fatalf("destroyStorage: %v", err)
	}

	// Verify PVCs were deleted.
	pvcs, _ := cs.CoreV1().PersistentVolumeClaims("slurm").List(ctx, metav1.ListOptions{})
	if len(pvcs.Items) != 0 {
		t.Errorf("expected 0 PVCs after destroy, got %d", len(pvcs.Items))
	}

	// Verify PVs were deleted.
	pvList, _ := cs.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if len(pvList.Items) != 0 {
		t.Errorf("expected 0 PVs after destroy, got %d", len(pvList.Items))
	}
}
