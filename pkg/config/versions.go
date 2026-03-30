package config

// ResolveVersion returns the version for a component. Site config overrides
// take precedence over the compiled default.
func ResolveVersion(site *Site, component, defaultVersion string) string {
	if site != nil && site.Versions != nil {
		if v, ok := site.Versions[component]; ok && v != "" {
			return v
		}
	}
	return defaultVersion
}
