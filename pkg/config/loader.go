package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadConfig reads and parses an nstack configuration file.
// Site names are populated from the map keys in the YAML.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Populate site names from map keys.
	for name, site := range cfg.Sites {
		site.Name = name
	}

	return &cfg, nil
}

// GetSite returns the named site or an error if it does not exist.
func (c *Config) GetSite(name string) (*Site, error) {
	site, ok := c.Sites[name]
	if !ok {
		return nil, fmt.Errorf("site %q not found in configuration", name)
	}
	return site, nil
}

// DefaultConfigPath returns the default configuration file path (~/.nstack/config.yaml).
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".nstack", "config.yaml")
	}
	return filepath.Join(home, ".nstack", "config.yaml")
}
