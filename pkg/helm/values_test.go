package helm

import (
	"testing"
)

func TestMergeValues_CommonThenProfile(t *testing.T) {
	common := map[string]interface{}{
		"replicaCount": 1,
		"image": map[string]interface{}{
			"repository": "nginx",
			"tag":        "latest",
		},
	}
	profile := map[string]interface{}{
		"image": map[string]interface{}{
			"tag": "1.21",
		},
	}

	result := MergeValues(common, profile)

	img, ok := result["image"].(map[string]interface{})
	if !ok {
		t.Fatal("expected image to be a map")
	}
	if img["tag"] != "1.21" {
		t.Errorf("expected tag=1.21, got %v", img["tag"])
	}
	if img["repository"] != "nginx" {
		t.Errorf("expected repository=nginx, got %v", img["repository"])
	}
	if result["replicaCount"] != 1 {
		t.Errorf("expected replicaCount=1, got %v", result["replicaCount"])
	}
}

func TestMergeValues_OverridesWin(t *testing.T) {
	base := map[string]interface{}{
		"port": 8080,
	}
	profile := map[string]interface{}{
		"port": 9090,
	}
	override := map[string]interface{}{
		"port": 3000,
	}

	result := MergeValues(base, profile, override)
	if result["port"] != 3000 {
		t.Errorf("expected port=3000, got %v", result["port"])
	}
}

func TestMergeValues_EmptyLayers(t *testing.T) {
	result := MergeValues(nil, nil, nil)
	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}

	result = MergeValues(nil, map[string]interface{}{"a": 1}, nil)
	if result["a"] != 1 {
		t.Errorf("expected a=1, got %v", result["a"])
	}

	result = MergeValues()
	if len(result) != 0 {
		t.Errorf("expected empty map for no args, got %v", result)
	}
}

func TestMergeValues_DeepMerge(t *testing.T) {
	base := map[string]interface{}{
		"global": map[string]interface{}{
			"storage": map[string]interface{}{
				"class": "local-path",
				"size":  "10Gi",
			},
			"network": "flannel",
		},
	}
	overlay := map[string]interface{}{
		"global": map[string]interface{}{
			"storage": map[string]interface{}{
				"class": "ceph",
			},
		},
	}

	result := MergeValues(base, overlay)

	global, ok := result["global"].(map[string]interface{})
	if !ok {
		t.Fatal("expected global to be a map")
	}
	storage, ok := global["storage"].(map[string]interface{})
	if !ok {
		t.Fatal("expected global.storage to be a map")
	}

	if storage["class"] != "ceph" {
		t.Errorf("expected class=ceph, got %v", storage["class"])
	}
	if storage["size"] != "10Gi" {
		t.Errorf("expected size=10Gi to be preserved, got %v", storage["size"])
	}
	if global["network"] != "flannel" {
		t.Errorf("expected network=flannel to be preserved, got %v", global["network"])
	}
}

func TestParseSetValues(t *testing.T) {
	result, err := ParseSetValues([]string{"a.b=c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a, ok := result["a"].(map[string]interface{})
	if !ok {
		t.Fatal("expected a to be a map")
	}
	if a["b"] != "c" {
		t.Errorf("expected a.b=c, got %v", a["b"])
	}
}

func TestParseSetValues_Multiple(t *testing.T) {
	result, err := ParseSetValues([]string{
		"image.tag=v1.0",
		"image.pullPolicy=Always",
		"replicas=3",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	img, ok := result["image"].(map[string]interface{})
	if !ok {
		t.Fatal("expected image to be a map")
	}
	if img["tag"] != "v1.0" {
		t.Errorf("expected tag=v1.0, got %v", img["tag"])
	}
	if img["pullPolicy"] != "Always" {
		t.Errorf("expected pullPolicy=Always, got %v", img["pullPolicy"])
	}
	if result["replicas"] != int64(3) {
		t.Errorf("expected replicas=int64(3), got %v (%T)", result["replicas"], result["replicas"])
	}
}

func TestParseSetValues_TypeCoercion(t *testing.T) {
	result, err := ParseSetValues([]string{
		"enabled=true",
		"disabled=false",
		"count=42",
		"ratio=3.14",
		"name=hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result["enabled"] != true {
		t.Errorf("expected enabled=true (bool), got %v (%T)", result["enabled"], result["enabled"])
	}
	if result["disabled"] != false {
		t.Errorf("expected disabled=false (bool), got %v (%T)", result["disabled"], result["disabled"])
	}
	if result["count"] != int64(42) {
		t.Errorf("expected count=int64(42), got %v (%T)", result["count"], result["count"])
	}
	if result["ratio"] != 3.14 {
		t.Errorf("expected ratio=3.14 (float64), got %v (%T)", result["ratio"], result["ratio"])
	}
	if result["name"] != "hello" {
		t.Errorf("expected name=hello (string), got %v (%T)", result["name"], result["name"])
	}
}

func TestParseSetValues_InvalidFormat(t *testing.T) {
	_, err := ParseSetValues([]string{"noequals"})
	if err == nil {
		t.Error("expected error for missing '='")
	}

	_, err = ParseSetValues([]string{"=value"})
	if err == nil {
		t.Error("expected error for empty key")
	}
}

func TestLoadValuesFile(t *testing.T) {
	data := []byte(`
image:
  repository: nginx
  tag: "1.21"
replicaCount: 3
`)
	vals, err := LoadValuesFile(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	img, ok := vals["image"].(map[string]interface{})
	if !ok {
		t.Fatal("expected image to be a map")
	}
	if img["repository"] != "nginx" {
		t.Errorf("expected repository=nginx, got %v", img["repository"])
	}
	if vals["replicaCount"] != 3 {
		t.Errorf("expected replicaCount=3, got %v", vals["replicaCount"])
	}
}
