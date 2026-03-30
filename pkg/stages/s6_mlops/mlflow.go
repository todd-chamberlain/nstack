package s6_mlops

import (
	"context"
	"fmt"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/todd-chamberlain/nstack/internal/assets"
	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

const (
	mlflowName    = "mlflow"
	mlflowPVName  = "mlflow-pv"
	mlflowPVCName = "mlflow-pvc"
)

// mlflowValues holds parsed values from the embedded MLflow configuration.
type mlflowValues struct {
	Image        string
	Port         int32
	NodePort     int32
	BackendStore string
	ArtifactRoot string
	Resources    corev1.ResourceRequirements
}

// loadMLflowValues reads and parses the embedded charts/mlflow/common.yaml file.
func loadMLflowValues() (*mlflowValues, error) {
	data, err := assets.FS.ReadFile("charts/mlflow/common.yaml")
	if err != nil {
		return nil, fmt.Errorf("reading mlflow common values: %w", err)
	}

	vals, err := helm.LoadValuesFile(data)
	if err != nil {
		return nil, fmt.Errorf("parsing mlflow common values: %w", err)
	}

	mv := &mlflowValues{
		Image:        "ghcr.io/mlflow/mlflow:v2.21.3",
		Port:         5000,
		NodePort:     30500,
		BackendStore: "sqlite:///data/db/mlflow.db",
		ArtifactRoot: "/data/artifacts",
	}

	if v, ok := vals["image"].(string); ok {
		mv.Image = v
	}
	if v, ok := vals["port"].(int); ok {
		mv.Port = int32(v)
	}
	if v, ok := vals["nodePort"].(int); ok {
		mv.NodePort = int32(v)
	}
	if v, ok := vals["backendStore"].(string); ok {
		mv.BackendStore = v
	}
	if v, ok := vals["artifactRoot"].(string); ok {
		mv.ArtifactRoot = v
	}

	// Parse resource requirements.
	mv.Resources = parseResources(vals)

	return mv, nil
}

// parseResources extracts CPU/memory resource requirements from the values map.
func parseResources(vals map[string]interface{}) corev1.ResourceRequirements {
	reqs := corev1.ResourceRequirements{}

	res, ok := vals["resources"].(map[string]interface{})
	if !ok {
		return reqs
	}

	if requests, ok := res["requests"].(map[string]interface{}); ok {
		reqs.Requests = corev1.ResourceList{}
		if cpu, ok := requests["cpu"].(string); ok {
			reqs.Requests[corev1.ResourceCPU] = resource.MustParse(cpu)
		}
		if mem, ok := requests["memory"].(string); ok {
			reqs.Requests[corev1.ResourceMemory] = resource.MustParse(mem)
		}
	}

	if limits, ok := res["limits"].(map[string]interface{}); ok {
		reqs.Limits = corev1.ResourceList{}
		if cpu, ok := limits["cpu"].(string); ok {
			reqs.Limits[corev1.ResourceCPU] = resource.MustParse(cpu)
		}
		if mem, ok := limits["memory"].(string); ok {
			reqs.Limits[corev1.ResourceMemory] = resource.MustParse(mem)
		}
	}

	return reqs
}

// deployMLflow creates the MLflow Deployment, Service, and storage resources
// in the cluster namespace.
func deployMLflow(ctx context.Context, kc *kube.Client, site *config.Site, profile *config.Profile, printer *output.Printer) error {
	mv, err := loadMLflowValues()
	if err != nil {
		return err
	}

	ns := config.ResolveCluster(site).Namespace

	// Ensure the cluster namespace exists.
	if err := kc.EnsureNamespace(ctx, ns); err != nil {
		return fmt.Errorf("ensuring %s namespace: %w", ns, err)
	}

	// 1. Create PV/PVC for MLflow data.
	if err := createMLflowStorage(ctx, kc, profile, ns, printer); err != nil {
		return fmt.Errorf("creating mlflow storage: %w", err)
	}

	// 2. Create Deployment.
	if err := createMLflowDeployment(ctx, kc, mv, ns, printer); err != nil {
		return fmt.Errorf("creating mlflow deployment: %w", err)
	}

	// 3. Create Service.
	if err := createMLflowService(ctx, kc, mv, ns, printer); err != nil {
		return fmt.Errorf("creating mlflow service: %w", err)
	}

	return nil
}

// createMLflowStorage creates the PV and PVC for MLflow data storage.
func createMLflowStorage(ctx context.Context, kc *kube.Client, profile *config.Profile, ns string, printer *output.Printer) error {
	cs := kc.Clientset()
	sc := config.ResolveStorage(profile)
	capacity := resource.MustParse("5Gi")

	switch sc.Type {
	case "hostPath":
		hostPath := sc.BasePath + "/mlflow"
		pathType := corev1.HostPathDirectoryOrCreate
		pv := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: mlflowPVName,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "nstack",
					"app.kubernetes.io/component":  "mlflow",
				},
			},
			Spec: corev1.PersistentVolumeSpec{
				Capacity: corev1.ResourceList{
					corev1.ResourceStorage: capacity,
				},
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: hostPath,
						Type: &pathType,
					},
				},
				ClaimRef: &corev1.ObjectReference{
					APIVersion: "v1",
					Kind:       "PersistentVolumeClaim",
					Name:       mlflowPVCName,
					Namespace:  ns,
				},
				PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
			},
		}

		_, err := cs.CoreV1().PersistentVolumes().Create(ctx, pv, metav1.CreateOptions{})
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Fix stale claimRef from previous namespace deletion.
				existing, getErr := cs.CoreV1().PersistentVolumes().Get(ctx, mlflowPVName, metav1.GetOptions{})
				if getErr == nil && existing.Status.Phase == corev1.VolumeReleased {
					existing.Spec.ClaimRef = pv.Spec.ClaimRef
					_, _ = cs.CoreV1().PersistentVolumes().Update(ctx, existing, metav1.UpdateOptions{})
				}
			} else {
				return fmt.Errorf("creating mlflow PV: %w", err)
			}
		}
		printer.Debugf("created PV %s at %s", mlflowPVName, hostPath)

		// Create PVC bound to the PV.
		empty := ""
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mlflowPVCName,
				Namespace: ns,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "nstack",
					"app.kubernetes.io/component":  "mlflow",
				},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: capacity,
					},
				},
				VolumeName:       mlflowPVName,
				StorageClassName: &empty,
			},
		}

		_, err = cs.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating mlflow PVC: %w", err)
		}
		printer.Debugf("created PVC %s", mlflowPVCName)

	case "pvc":
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mlflowPVCName,
				Namespace: ns,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "nstack",
					"app.kubernetes.io/component":  "mlflow",
				},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: capacity,
					},
				},
			},
		}
		if sc.StorageClass != "" {
			pvc.Spec.StorageClassName = &sc.StorageClass
		}

		_, err := cs.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating mlflow PVC: %w", err)
		}
		printer.Debugf("created PVC %s with storageClass %s", mlflowPVCName, sc.StorageClass)

	default:
		return fmt.Errorf("unsupported storage type: %s", sc.Type)
	}

	return nil
}

// createMLflowDeployment creates the MLflow server Deployment.
func createMLflowDeployment(ctx context.Context, kc *kube.Client, mv *mlflowValues, ns string, printer *output.Printer) error {
	cs := kc.Clientset()

	replicas := int32(1)
	labels := map[string]string{
		"app.kubernetes.io/name":       mlflowName,
		"app.kubernetes.io/managed-by": "nstack",
		"app.kubernetes.io/component":  "mlflow",
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mlflowName,
			Namespace: ns,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  mlflowName,
							Image: mv.Image,
							Command: []string{
								"mlflow", "server",
							},
							Args: []string{
								"--host=0.0.0.0",
								"--port=" + strconv.Itoa(int(mv.Port)),
								"--backend-store-uri=" + mv.BackendStore,
								"--default-artifact-root=" + mv.ArtifactRoot,
								"--serve-artifacts",
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: mv.Port,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "mlflow-data",
									MountPath: "/data",
								},
							},
							Resources: mv.Resources,
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "mlflow-data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: mlflowPVCName,
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := cs.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			printer.Debugf("mlflow deployment already exists, updating")
			_, err = cs.AppsV1().Deployments(ns).Update(ctx, dep, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("updating mlflow deployment: %w", err)
			}
			return nil
		}
		return fmt.Errorf("creating mlflow deployment: %w", err)
	}

	printer.Debugf("created mlflow deployment")
	return nil
}

// createMLflowService creates the MLflow NodePort service.
func createMLflowService(ctx context.Context, kc *kube.Client, mv *mlflowValues, ns string, printer *output.Printer) error {
	cs := kc.Clientset()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mlflowName,
			Namespace: ns,
			Labels: map[string]string{
				"app.kubernetes.io/name":       mlflowName,
				"app.kubernetes.io/managed-by": "nstack",
				"app.kubernetes.io/component":  "mlflow",
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Selector: map[string]string{
				"app.kubernetes.io/name": mlflowName,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       mv.Port,
					TargetPort: intstr.FromInt32(mv.Port),
					NodePort:   mv.NodePort,
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	_, err := cs.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			printer.Debugf("mlflow service already exists, updating")
			// Get existing service to preserve ClusterIP.
			existing, getErr := cs.CoreV1().Services(ns).Get(ctx, mlflowName, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("getting existing mlflow service: %w", getErr)
			}
			svc.ObjectMeta.ResourceVersion = existing.ObjectMeta.ResourceVersion
			svc.Spec.ClusterIP = existing.Spec.ClusterIP
			_, err = cs.CoreV1().Services(ns).Update(ctx, svc, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("updating mlflow service: %w", err)
			}
			return nil
		}
		return fmt.Errorf("creating mlflow service: %w", err)
	}

	printer.Debugf("created mlflow service (NodePort %d)", mv.NodePort)
	return nil
}

// destroyMLflow removes the MLflow Deployment, Service, PVC, and PV.
func destroyMLflow(ctx context.Context, kc *kube.Client, ns string, printer *output.Printer) error {
	cs := kc.Clientset()

	// Delete Service.
	err := cs.CoreV1().Services(ns).Delete(ctx, mlflowName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting mlflow service: %w", err)
	}
	printer.Debugf("deleted mlflow service")

	// Delete Deployment.
	err = cs.AppsV1().Deployments(ns).Delete(ctx, mlflowName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting mlflow deployment: %w", err)
	}
	printer.Debugf("deleted mlflow deployment")

	// Delete PVC.
	err = cs.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, mlflowPVCName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting mlflow PVC: %w", err)
	}
	printer.Debugf("deleted mlflow PVC")

	// Delete PV.
	err = cs.CoreV1().PersistentVolumes().Delete(ctx, mlflowPVName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting mlflow PV: %w", err)
	}
	printer.Debugf("deleted mlflow PV")

	return nil
}
