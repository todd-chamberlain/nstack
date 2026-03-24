package helm

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// MergeValues performs a deep merge of multiple value layers.
// Later layers override earlier ones. Nested maps merge recursively;
// scalar values and slices are replaced entirely.
func MergeValues(layers ...map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for _, layer := range layers {
		if layer == nil {
			continue
		}
		mergeMaps(result, layer)
	}
	return result
}

// mergeMaps recursively merges src into dst. Values in src override dst.
// Nested maps are merged recursively rather than replaced wholesale.
func mergeMaps(dst, src map[string]interface{}) {
	for key, srcVal := range src {
		dstVal, exists := dst[key]
		if !exists {
			dst[key] = srcVal
			continue
		}

		// If both are maps, merge recursively.
		srcMap, srcIsMap := srcVal.(map[string]interface{})
		dstMap, dstIsMap := dstVal.(map[string]interface{})
		if srcIsMap && dstIsMap {
			mergeMaps(dstMap, srcMap)
			continue
		}

		// Otherwise the source value wins.
		dst[key] = srcVal
	}
}

// LoadValuesFile parses YAML bytes into a values map.
func LoadValuesFile(data []byte) (map[string]interface{}, error) {
	vals := make(map[string]interface{})
	if err := yaml.Unmarshal(data, &vals); err != nil {
		return nil, fmt.Errorf("parsing values YAML: %w", err)
	}
	return vals, nil
}

// ParseSetValues converts --set style key=value pairs into a nested map.
// Keys use dot-separated paths: "a.b.c=val" becomes {"a":{"b":{"c":"val"}}}.
func ParseSetValues(sets []string) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	for _, s := range sets {
		idx := strings.Index(s, "=")
		if idx < 0 {
			return nil, fmt.Errorf("invalid --set format %q: missing '='", s)
		}
		key := s[:idx]
		val := s[idx+1:]

		if key == "" {
			return nil, fmt.Errorf("invalid --set format %q: empty key", s)
		}

		parts := strings.Split(key, ".")
		setNestedValue(result, parts, val)
	}
	return result, nil
}

// setNestedValue sets a value at the given path in a nested map structure.
func setNestedValue(m map[string]interface{}, path []string, value string) {
	for i := 0; i < len(path)-1; i++ {
		key := path[i]
		next, ok := m[key]
		if !ok {
			next = make(map[string]interface{})
			m[key] = next
		}
		nextMap, ok := next.(map[string]interface{})
		if !ok {
			nextMap = make(map[string]interface{})
			m[key] = nextMap
		}
		m = nextMap
	}
	m[path[len(path)-1]] = value
}
