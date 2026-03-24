package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_ValidFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	yaml := `version: "1"
sites:
  lab:
    profile: k3s-single
    kubeconfig: ~/.kube/config
    overlay:
      type: wireguard
      endpoint: 10.0.0.1
    nodes:
      - name: node1
        ip: 192.168.1.100
        role: server
        gpus:
          - model: A100
            count: 8
            vram: 80GB
  cloud:
    profile: nebius
    kubeconfig: /etc/kube/nebius.yaml
`
	if err := os.WriteFile(configPath, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if cfg.Version != "1" {
		t.Errorf("expected version '1', got %q", cfg.Version)
	}

	if len(cfg.Sites) != 2 {
		t.Fatalf("expected 2 sites, got %d", len(cfg.Sites))
	}

	lab, err := cfg.GetSite("lab")
	if err != nil {
		t.Fatalf("GetSite(lab) error: %v", err)
	}
	if lab.Name != "lab" {
		t.Errorf("expected site name 'lab', got %q", lab.Name)
	}
	if lab.Profile != "k3s-single" {
		t.Errorf("expected profile 'k3s-single', got %q", lab.Profile)
	}
	if lab.Overlay == nil || lab.Overlay.Type != "wireguard" {
		t.Error("expected overlay type 'wireguard'")
	}
	if len(lab.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(lab.Nodes))
	}
	if lab.Nodes[0].Name != "node1" {
		t.Errorf("expected node name 'node1', got %q", lab.Nodes[0].Name)
	}
	if len(lab.Nodes[0].GPUs) != 1 || lab.Nodes[0].GPUs[0].Model != "A100" {
		t.Error("expected GPU model 'A100'")
	}

	cloud, err := cfg.GetSite("cloud")
	if err != nil {
		t.Fatalf("GetSite(cloud) error: %v", err)
	}
	if cloud.Profile != "nebius" {
		t.Errorf("expected profile 'nebius', got %q", cloud.Profile)
	}
}

func TestLoadConfig_MissingSite(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	yaml := `version: "1"
sites:
  lab:
    profile: k3s-single
`
	if err := os.WriteFile(configPath, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	_, err = cfg.GetSite("nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent site, got nil")
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/tmp/nstack-test-does-not-exist.yaml")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}
