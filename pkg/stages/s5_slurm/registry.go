package s5_slurm

import (
	"strings"

	"github.com/todd-chamberlain/nstack/pkg/config"
)

// defaultRegistries are the known soperator image registry prefixes that
// should be replaced when a profile overrides the registry.
var defaultRegistries = []string{
	"ghcr.io/nebius/soperator/",
	"cr.eu-north1.nebius.cloud/soperator/",
}

// applyRegistryOverride replaces the default soperator registry in image
// values with the profile's configured registry. It modifies the values
// map in place.
func applyRegistryOverride(values map[string]interface{}, profile *config.Profile) {
	if profile == nil || profile.Images.Registry == "" {
		return
	}
	images, ok := values["images"].(map[string]interface{})
	if !ok {
		return
	}
	for key, val := range images {
		str, ok := val.(string)
		if !ok {
			continue
		}
		for _, prefix := range defaultRegistries {
			str = strings.Replace(str, prefix, profile.Images.Registry+"/", 1)
		}
		images[key] = str
	}
}
