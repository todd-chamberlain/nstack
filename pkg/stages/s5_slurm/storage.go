package s5_slurm

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

// createStorage ensures the slurm namespace exists and creates the required
// PersistentVolumes and PersistentVolumeClaims for the Slurm cluster.
// For hostPath storage, it creates static PV/PVC pairs.
// For PVC-based storage, it creates PVCs with a storageClassName for dynamic provisioning.
func createStorage(ctx context.Context, kc *kube.Client, profile *config.Profile, cluster config.ClusterConfig, printer *output.Printer) error {
	// Ensure the slurm namespace exists.
	if err := kc.EnsureNamespace(ctx, cluster.Namespace); err != nil {
		return fmt.Errorf("ensuring slurm namespace: %w", err)
	}

	sc := config.ResolveStorage(profile)

	switch sc.Type {
	case "hostPath":
		if err := createHostPathStorage(ctx, kc, sc.BasePath, profile, cluster, printer); err != nil {
			return err
		}
	case "pvc":
		if err := createDynamicStorage(ctx, kc, sc.StorageClass, cluster, printer); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported storage type: %s", sc.Type)
	}

	return nil
}

// createHostPathStorage creates static PV and PVC pairs backed by host directories.
// PV names are prefixed with the cluster namespace to avoid collisions (PVs are
// cluster-scoped). PVC names are left unprefixed since they are namespace-scoped.
func createHostPathStorage(ctx context.Context, kc *kube.Client, basePath string, profile *config.Profile, cluster config.ClusterConfig, printer *output.Printer) error {
	if profile != nil && profile.Kubernetes.MultiNode {
		printer.Infof("[WARNING] hostPath storage is not suitable for multi-node clusters; consider using PVC-based shared storage")
	}

	cs := kc.Clientset()
	ns := cluster.Namespace

	// Controller spool PV + PVC.
	spoolPVName := ns + "-controller-spool-pv"
	spoolPath := basePath + "/controller-spool"
	if err := ensureHostPathPV(ctx, cs, spoolPVName, "controller-spool-pvc", ns, spoolPath,
		resource.MustParse("10Gi"), corev1.ReadWriteOnce); err != nil {
		return fmt.Errorf("creating controller-spool PV: %w", err)
	}
	printer.Debugf("created PV %s at %s", spoolPVName, spoolPath)

	if err := ensurePVC(ctx, cs, "controller-spool-pvc", ns, spoolPVName, "",
		resource.MustParse("10Gi"), corev1.ReadWriteOnce); err != nil {
		return fmt.Errorf("creating controller-spool PVC: %w", err)
	}
	printer.Debugf("created PVC controller-spool-pvc")

	// Jail PV + PVC (ReadWriteOnce — hostPath volumes cannot be shared across nodes).
	jailPVName := ns + "-jail-pv"
	jailPath := basePath + "/jail"
	if err := ensureHostPathPV(ctx, cs, jailPVName, "jail-pvc", ns, jailPath,
		resource.MustParse("50Gi"), corev1.ReadWriteOnce); err != nil {
		return fmt.Errorf("creating jail PV: %w", err)
	}
	printer.Debugf("created PV %s at %s", jailPVName, jailPath)

	if err := ensurePVC(ctx, cs, "jail-pvc", ns, jailPVName, "",
		resource.MustParse("50Gi"), corev1.ReadWriteOnce); err != nil {
		return fmt.Errorf("creating jail PVC: %w", err)
	}
	printer.Debugf("created PVC jail-pvc")

	return nil
}

// createDynamicStorage creates PVCs that rely on a StorageClass for dynamic provisioning.
func createDynamicStorage(ctx context.Context, kc *kube.Client, storageClass string, cluster config.ClusterConfig, printer *output.Printer) error {
	cs := kc.Clientset()
	ns := cluster.Namespace

	if err := ensurePVC(ctx, cs, "controller-spool-pvc", ns, "", storageClass,
		resource.MustParse("10Gi"), corev1.ReadWriteOnce); err != nil {
		return fmt.Errorf("creating controller-spool PVC: %w", err)
	}
	printer.Debugf("created PVC controller-spool-pvc with storageClass %s", storageClass)

	if err := ensurePVC(ctx, cs, "jail-pvc", ns, "", storageClass,
		resource.MustParse("50Gi"), corev1.ReadWriteMany); err != nil {
		return fmt.Errorf("creating jail PVC: %w", err)
	}
	printer.Debugf("created PVC jail-pvc with storageClass %s", storageClass)

	return nil
}

// ensureHostPathPV creates a hostPath PersistentVolume if it does not already exist.
// pvcName is the name of the PVC this PV should be bound to via ClaimRef.
func ensureHostPathPV(ctx context.Context, cs kubernetes.Interface, name, pvcName, namespace, hostPath string, capacity resource.Quantity, accessMode corev1.PersistentVolumeAccessMode) error {
	pathType := corev1.HostPathDirectoryOrCreate
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "nstack",
				"app.kubernetes.io/component":  "slurm-storage",
			},
		},
		Spec: corev1.PersistentVolumeSpec{
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: capacity,
			},
			AccessModes: []corev1.PersistentVolumeAccessMode{accessMode},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: hostPath,
					Type: &pathType,
				},
			},
			ClaimRef: &corev1.ObjectReference{
				APIVersion: "v1",
				Kind:       "PersistentVolumeClaim",
				Name:       pvcName,
				Namespace:  namespace,
			},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
		},
	}

	_, err := cs.CoreV1().PersistentVolumes().Create(ctx, pv, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			// PV exists — check if it's in Released state with a stale claimRef.
			// This happens after namespace delete/recreate cycles.
			existing, getErr := cs.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
			if getErr == nil && existing.Status.Phase == corev1.VolumeReleased {
				existing.Spec.ClaimRef = pv.Spec.ClaimRef
				_, _ = cs.CoreV1().PersistentVolumes().Update(ctx, existing, metav1.UpdateOptions{})
			}
			return nil
		}
		return err
	}
	return nil
}

// ensurePVC creates a PersistentVolumeClaim if it does not already exist.
// If pvName is set, the PVC is bound to a specific PV (static provisioning).
// If storageClass is set, it uses dynamic provisioning.
func ensurePVC(ctx context.Context, cs kubernetes.Interface, name, namespace, pvName, storageClass string, capacity resource.Quantity, accessMode corev1.PersistentVolumeAccessMode) error {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "nstack",
				"app.kubernetes.io/component":  "slurm-storage",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{accessMode},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: capacity,
				},
			},
		},
	}

	if pvName != "" {
		pvc.Spec.VolumeName = pvName
		// Use empty storage class for static binding.
		empty := ""
		pvc.Spec.StorageClassName = &empty
	}
	if storageClass != "" {
		pvc.Spec.StorageClassName = &storageClass
	}

	_, err := cs.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	return nil
}

// destroyStorage removes the PVCs and PVs created by createStorage.
func destroyStorage(ctx context.Context, kc *kube.Client, cluster config.ClusterConfig, printer *output.Printer) error {
	cs := kc.Clientset()
	ns := cluster.Namespace

	// Delete PVCs first.
	pvcs := []string{"controller-spool-pvc", "jail-pvc"}
	for _, name := range pvcs {
		err := cs.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting PVC %s: %w", name, err)
		}
		printer.Debugf("deleted PVC %s", name)
	}

	// Delete PVs (namespace-prefixed names).
	pvs := []string{ns + "-controller-spool-pv", ns + "-jail-pv"}
	for _, name := range pvs {
		err := cs.CoreV1().PersistentVolumes().Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting PV %s: %w", name, err)
		}
		printer.Debugf("deleted PV %s", name)
	}

	return nil
}
