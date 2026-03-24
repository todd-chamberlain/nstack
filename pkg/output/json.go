package output

import (
	"encoding/json"
	"time"
)

// Event is a structured output event for JSON mode (JSONL format).
type Event struct {
	Timestamp string `json:"timestamp"`
	Stage     int    `json:"stage,omitempty"`
	Component string `json:"component,omitempty"`
	Action    string `json:"action,omitempty"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
}

// EmitJSON marshals a single Event as JSON and writes it to output followed by
// a newline (JSONL / newline-delimited JSON format).
func (p *Printer) EmitJSON(event Event) {
	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(event)
	if err != nil {
		// Should never happen with simple struct types, but be defensive.
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _ = p.out.Write(append(data, '\n'))
}
