package config

import "testing"

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name           string
		site           *Site
		component      string
		defaultVersion string
		want           string
	}{
		{
			name:           "nil site returns default",
			site:           nil,
			component:      "gpu-operator",
			defaultVersion: "v25.10.1",
			want:           "v25.10.1",
		},
		{
			name:           "nil versions map returns default",
			site:           &Site{},
			component:      "gpu-operator",
			defaultVersion: "v25.10.1",
			want:           "v25.10.1",
		},
		{
			name: "component not in versions returns default",
			site: &Site{
				Versions: map[string]string{"cert-manager": "v1.18.0"},
			},
			component:      "gpu-operator",
			defaultVersion: "v25.10.1",
			want:           "v25.10.1",
		},
		{
			name: "empty string override returns default",
			site: &Site{
				Versions: map[string]string{"gpu-operator": ""},
			},
			component:      "gpu-operator",
			defaultVersion: "v25.10.1",
			want:           "v25.10.1",
		},
		{
			name: "override takes precedence",
			site: &Site{
				Versions: map[string]string{"gpu-operator": "v25.11.0"},
			},
			component:      "gpu-operator",
			defaultVersion: "v25.10.1",
			want:           "v25.11.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveVersion(tt.site, tt.component, tt.defaultVersion)
			if got != tt.want {
				t.Errorf("ResolveVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
