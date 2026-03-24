package state

import "time"

// State represents the full deployment state tracked by nstack in a ConfigMap.
type State struct {
	Site    string              `json:"site"`
	Profile string              `json:"profile"`
	Stages  map[int]*StageState `json:"stages"`
}

// StageState tracks the deployment status of a single stage.
type StageState struct {
	Status     string                     `json:"status"` // "deployed", "failed", "not-installed"
	Version    string                     `json:"version"`
	Applied    time.Time                  `json:"applied"`
	Components map[string]*ComponentState `json:"components"`
	Error      string                     `json:"error,omitempty"`
}

// ComponentState tracks the status of an individual component within a stage.
type ComponentState struct {
	Version string `json:"version"`
	Status  string `json:"status"` // "running", "failed", "pending"
}

// NewState returns an initialized State with the given site and profile
// and an empty Stages map.
func NewState(site, profile string) *State {
	return &State{
		Site:    site,
		Profile: profile,
		Stages:  make(map[int]*StageState),
	}
}
