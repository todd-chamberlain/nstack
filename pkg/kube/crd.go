package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// ApplyCRDs parses multi-document YAML and applies each CRD using server-side apply.
// Returns the count of CRDs successfully applied.
func (c *Client) ApplyCRDs(ctx context.Context, yamlData []byte) (int, error) {
	extClient, err := apiextensionsclientset.NewForConfig(c.restConfig)
	if err != nil {
		return 0, fmt.Errorf("creating apiextensions client: %w", err)
	}

	docs := splitYAMLDocuments(yamlData)
	applied := 0

	for _, doc := range docs {
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}

		var crd apiextensionsv1.CustomResourceDefinition
		decoder := yaml.NewYAMLOrJSONDecoder(io.NopCloser(bytes.NewReader(doc)), 4096)
		if err := decoder.Decode(&crd); err != nil {
			return applied, fmt.Errorf("decoding CRD YAML: %w", err)
		}

		if crd.Kind != "CustomResourceDefinition" && crd.Kind != "" {
			// Skip non-CRD documents silently.
			continue
		}
		if crd.Name == "" {
			continue
		}

		data, err := json.Marshal(&crd)
		if err != nil {
			return applied, fmt.Errorf("marshaling CRD %s to JSON: %w", crd.Name, err)
		}

		_, err = extClient.ApiextensionsV1().CustomResourceDefinitions().Patch(
			ctx,
			crd.Name,
			types.ApplyPatchType,
			data,
			metav1.PatchOptions{
				FieldManager: "nstack",
			},
		)
		if err != nil {
			return applied, fmt.Errorf("applying CRD %s: %w", crd.Name, err)
		}
		applied++
	}

	return applied, nil
}

// splitYAMLDocuments splits multi-document YAML on "---" separators.
func splitYAMLDocuments(data []byte) [][]byte {
	parts := strings.Split(string(data), "\n---")
	docs := make([][]byte, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			docs = append(docs, []byte(trimmed))
		}
	}
	return docs
}
