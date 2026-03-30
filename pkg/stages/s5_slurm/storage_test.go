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

	err := createStorage(ctx, kc, profile, printer)
	if err != nil {
		t.Fatalf("createStorage hostPath: %v", err)
	}

	// Verify namespace was created.
	_, err = cs.CoreV1().Namespaces().Get(ctx, slurmNamespace, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("slurm namespace not created: %v", err)
	}

	// Verify PVs were created.
	pvs := []string{"controller-spool-pv", "jail-pv"}
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
		pvc, err := cs.CoreV1().PersistentVolumeClaims(slurmNamespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("PVC %s not created: %v", name, err)
		}
		if pvc.Labels["app.kubernetes.io/managed-by"] != "nstack" {
			t.Errorf("PVC %s: expected managed-by label=nstack", name)
		}
	}

	// Verify jail-pv uses the correct path.
	jailPV, _ := cs.CoreV1().PersistentVolumes().Get(ctx, "jail-pv", metav1.GetOptions{})
	if jailPV.Spec.HostPath.Path != "/storage/test-slurm/jail" {
		t.Errorf("jail-pv: expected path=/storage/test-slurm/jail, got %s", jailPV.Spec.HostPath.Path)
	}

	// Verify jail-pvc has ReadWriteMany access mode.
	jailPVC, _ := cs.CoreV1().PersistentVolumeClaims(slurmNamespace).Get(ctx, "jail-pvc", metav1.GetOptions{})
	if len(jailPVC.Spec.AccessModes) == 0 || jailPVC.Spec.AccessModes[0] != corev1.ReadWriteMany {
		t.Errorf("jail-pvc: expected access mode ReadWriteMany")
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

	err := createStorage(ctx, kc, profile, printer)
	if err != nil {
		t.Fatalf("createStorage pvc: %v", err)
	}

	// Verify PVCs were created.
	pvcs := []string{"controller-spool-pvc", "jail-pvc"}
	for _, name := range pvcs {
		pvc, err := cs.CoreV1().PersistentVolumeClaims(slurmNamespace).Get(ctx, name, metav1.GetOptions{})
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
		ObjectMeta: metav1.ObjectMeta{Name: slurmNamespace},
	}
	spoolPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "controller-spool-pvc",
			Namespace: slurmNamespace,
		},
	}
	jailPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jail-pvc",
			Namespace: slurmNamespace,
		},
	}
	// Also pre-create PVs for hostPath.
	spoolPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "controller-spool-pv"},
	}
	jailPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "jail-pv"},
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

	err := createStorage(ctx, kc, profile, printer)
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
	err := createStorage(ctx, kc, nil, printer)
	if err != nil {
		t.Fatalf("createStorage with nil profile: %v", err)
	}

	// Verify PVs were created with default path.
	spoolPV, err := cs.CoreV1().PersistentVolumes().Get(ctx, "controller-spool-pv", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("controller-spool-pv not created: %v", err)
	}
	if spoolPV.Spec.HostPath.Path != "/var/lib/nstack/slurm/controller-spool" {
		t.Errorf("expected default path /var/lib/nstack/slurm/controller-spool, got %s", spoolPV.Spec.HostPath.Path)
	}
}

func TestDestroyStorage(t *testing.T) {
	// Pre-create PVCs and PVs.
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: slurmNamespace},
	}
	spoolPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "controller-spool-pvc",
			Namespace: slurmNamespace,
		},
	}
	jailPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jail-pvc",
			Namespace: slurmNamespace,
		},
	}
	spoolPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "controller-spool-pv"},
	}
	jailPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "jail-pv"},
	}

	cs := fake.NewSimpleClientset(ns, spoolPVC, jailPVC, spoolPV, jailPV)
	kc := kube.NewClientFromInterfaces(cs, nil, nil)
	printer := output.New("text", true, true)
	ctx := context.Background()

	err := destroyStorage(ctx, kc, printer)
	if err != nil {
		t.Fatalf("destroyStorage: %v", err)
	}

	// Verify PVCs were deleted.
	pvcs, _ := cs.CoreV1().PersistentVolumeClaims(slurmNamespace).List(ctx, metav1.ListOptions{})
	if len(pvcs.Items) != 0 {
		t.Errorf("expected 0 PVCs after destroy, got %d", len(pvcs.Items))
	}

	// Verify PVs were deleted.
	pvList, _ := cs.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if len(pvList.Items) != 0 {
		t.Errorf("expected 0 PVs after destroy, got %d", len(pvList.Items))
	}
}
