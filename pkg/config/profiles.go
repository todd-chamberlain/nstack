package config

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/todd-chamberlain/nstack/internal/assets"
	"gopkg.in/yaml.v3"
)

// LoadProfile reads a named profile from the embedded assets filesystem.
func LoadProfile(name string) (*Profile, error) {
	filename := name + ".yaml"
	data, err := fs.ReadFile(assets.FS, filepath.Join("profiles", filename))
	if err != nil {
		return nil, fmt.Errorf("profile %q not found: %w", name, err)
	}

	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing profile %q: %w", name, err)
	}

	return &p, nil
}

// ListProfiles returns the names of all available embedded profiles.
func ListProfiles() []string {
	entries, err := fs.ReadDir(assets.FS, "profiles")
	if err != nil {
		return nil
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".yaml") {
			names = append(names, strings.TrimSuffix(name, ".yaml"))
		}
	}
	return names
}
