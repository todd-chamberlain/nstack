package kube

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// ExtractImageVersion returns the image tag from the first container, or "unknown".
func ExtractImageVersion(containers []corev1.Container) string {
	if len(containers) == 0 {
		return "unknown"
	}
	img := containers[0].Image
	if idx := strings.LastIndex(img, ":"); idx >= 0 {
		return img[idx+1:]
	}
	return "unknown"
}
