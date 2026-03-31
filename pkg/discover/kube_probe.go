package discover

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// K8sProbeResult holds information from a K8s API version endpoint probe.
type K8sProbeResult struct {
	Version string
	Distro  string
}

// k8sVersionResponse is the structure returned by /version on a K8s API server.
type k8sVersionResponse struct {
	Major        string `json:"major"`
	Minor        string `json:"minor"`
	GitVersion   string `json:"gitVersion"`
	Platform     string `json:"platform"`
}

// K8s probe constants.
const (
	maxK8sBody     = 64 << 10 // Maximum K8s API response body size (64 KB).
	k8sStdPort     = "6443"   // Standard Kubernetes API port.
	k8sMicroK8sPort = "16443"  // MicroK8s API port.
)

// probeK8sAPI tries to connect to the K8s API server version endpoint.
// It tries port 6443 first (standard), then 16443 (microk8s).
func probeK8sAPI(ctx context.Context, ip string, timeout time.Duration) (*K8sProbeResult, error) {
	httpClient := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // probing unknown clusters
			DisableKeepAlives: true,
		},
	}

	// Try standard K8s port first, then microk8s
	result, err := tryK8sPort(ctx, httpClient, ip, k8sStdPort)
	if err == nil {
		return result, nil
	}

	result, err = tryK8sPort(ctx, httpClient, ip, k8sMicroK8sPort)
	if err == nil {
		return result, nil
	}

	return nil, fmt.Errorf("no K8s API found on %s", ip)
}

// tryK8sPort attempts an HTTPS GET to /version on the given port.
func tryK8sPort(ctx context.Context, httpClient *http.Client, ip, port string) (*K8sProbeResult, error) {
	// Quick TCP check first
	conn, err := (&net.Dialer{Timeout: tcpDialTimeout}).DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
	if err != nil {
		return nil, err
	}
	conn.Close()

	url := fmt.Sprintf("https://%s/version", net.JoinHostPort(ip, port))
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxK8sBody))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("K8s API returned %d on %s:%s", resp.StatusCode, ip, port)
	}

	var ver k8sVersionResponse
	if err := json.Unmarshal(body, &ver); err != nil {
		return nil, fmt.Errorf("parsing K8s version response: %w", err)
	}

	if ver.GitVersion == "" {
		return nil, fmt.Errorf("empty K8s version from %s:%s", ip, port)
	}

	result := &K8sProbeResult{
		Version: ver.GitVersion,
		Distro:  detectK8sDistro(ver.GitVersion),
	}

	return result, nil
}

// detectK8sDistro identifies the K8s distribution from the version string.
func detectK8sDistro(version string) string {
	switch {
	case strings.Contains(version, "+k3s"):
		return "k3s"
	case strings.Contains(version, "-eks"):
		return "eks"
	case strings.Contains(version, "-gke"):
		return "gke"
	case strings.Contains(version, "-aks"):
		return "aks"
	default:
		return "kubeadm"
	}
}
