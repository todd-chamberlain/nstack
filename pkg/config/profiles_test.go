package config

import (
	"testing"
)

func TestLoadProfile_K3sSingle(t *testing.T) {
	p, err := LoadProfile("k3s-single")
	if err != nil {
		t.Fatalf("LoadProfile(k3s-single) error: %v", err)
	}

	if p.Name != "k3s-single" {
		t.Errorf("expected name 'k3s-single', got %q", p.Name)
	}
	if p.Kubernetes.Distribution != "k3s" {
		t.Errorf("expected distribution 'k3s', got %q", p.Kubernetes.Distribution)
	}
	if p.Kubernetes.MultiNode != false {
		t.Error("expected multiNode=false")
	}
	if !p.Patches.ContainerdSocketBind {
		t.Error("expected containerdSocketBind=true")
	}
	// These patches were eliminated by moving to proper Helm values:
	// cgroupEntrypoint, operatorScaleDown, workerInitSkip, prologToBinTrue, procMountDefault
	if p.Patches.CgroupEntrypoint {
		t.Error("expected cgroupEntrypoint=false (handled by patched worker image)")
	}
	if p.Patches.OperatorScaleDown {
		t.Error("expected operatorScaleDown=false (operator stays running)")
	}
	if p.Storage.Type != "hostPath" {
		t.Errorf("expected storage type 'hostPath', got %q", p.Storage.Type)
	}
}

func TestLoadProfile_Nebius(t *testing.T) {
	p, err := LoadProfile("nebius")
	if err != nil {
		t.Fatalf("LoadProfile(nebius) error: %v", err)
	}

	if p.Kubernetes.Distribution != "managed" {
		t.Errorf("expected distribution 'managed', got %q", p.Kubernetes.Distribution)
	}
	if p.Patches.CgroupEntrypoint {
		t.Error("expected cgroupEntrypoint=false")
	}
	if p.Networking.Fabric != "infiniband" {
		t.Errorf("expected fabric 'infiniband', got %q", p.Networking.Fabric)
	}
	if p.Storage.Type != "pvc" {
		t.Errorf("expected storage type 'pvc', got %q", p.Storage.Type)
	}
	if p.Kubernetes.StorageClass != "nebius-network-ssd" {
		t.Errorf("expected storageClass 'nebius-network-ssd', got %q", p.Kubernetes.StorageClass)
	}
}

func TestLoadProfile_NotFound(t *testing.T) {
	_, err := LoadProfile("nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent profile, got nil")
	}
}
