package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPrinter_StageHeader(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "text", false, false)

	p.StageHeader(4, "GPU Stack")

	output := buf.String()
	if !strings.Contains(output, "Stage 4: GPU Stack") {
		t.Errorf("StageHeader output did not contain expected text, got: %q", output)
	}
}

func TestPrinter_ComponentStart(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "text", false, false)

	p.ComponentStart(2, 5, "cert-manager", "v1.17.2", "installing")

	output := buf.String()
	if !strings.Contains(output, "[2/5]") {
		t.Errorf("expected index [2/5] in output, got: %q", output)
	}
	if !strings.Contains(output, "cert-manager") {
		t.Errorf("expected component name in output, got: %q", output)
	}
	if !strings.Contains(output, "v1.17.2") {
		t.Errorf("expected version in output, got: %q", output)
	}
	if !strings.Contains(output, "installing") {
		t.Errorf("expected action in output, got: %q", output)
	}
}

func TestPrinter_ComponentDone_Success(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "text", false, false)

	p.ComponentDone("cert-manager", nil)

	output := buf.String()
	// Check mark character.
	if !strings.Contains(output, "\u2713") {
		t.Errorf("expected check mark in success output, got: %q", output)
	}
}

func TestPrinter_ComponentDone_Error(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "text", false, false)

	p.ComponentDone("cert-manager", errors.New("install failed"))

	output := buf.String()
	if !strings.Contains(output, "FAILED") {
		t.Errorf("expected FAILED in error output, got: %q", output)
	}
	if !strings.Contains(output, "install failed") {
		t.Errorf("expected error message in output, got: %q", output)
	}
}

func TestPrinter_QuietMode(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "text", true, false)

	// Infof should be suppressed in quiet mode.
	p.Infof("this should be hidden")
	if buf.Len() != 0 {
		t.Errorf("Infof should be suppressed in quiet mode, got: %q", buf.String())
	}

	// StageHeader should be suppressed in quiet mode.
	p.StageHeader(1, "Test Stage")
	if buf.Len() != 0 {
		t.Errorf("StageHeader should be suppressed in quiet mode, got: %q", buf.String())
	}

	// ComponentStart should be suppressed in quiet mode.
	p.ComponentStart(1, 1, "test", "v1.0", "installing")
	if buf.Len() != 0 {
		t.Errorf("ComponentStart should be suppressed in quiet mode, got: %q", buf.String())
	}

	// Errorf should NOT be suppressed in quiet mode.
	p.Errorf("this should appear")
	if buf.Len() == 0 {
		t.Error("Errorf should not be suppressed in quiet mode")
	}
	if !strings.Contains(buf.String(), "this should appear") {
		t.Errorf("Errorf output unexpected: %q", buf.String())
	}
}

func TestPrinter_JSONMode_StageHeader(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "json", false, false)

	p.StageHeader(4, "GPU Stack")

	output := strings.TrimSpace(buf.String())
	var event Event
	if err := json.Unmarshal([]byte(output), &event); err != nil {
		t.Fatalf("failed to parse JSON output: %v\nraw: %q", err, output)
	}
	if event.Stage != 4 {
		t.Errorf("expected stage=4, got %d", event.Stage)
	}
	if event.Status != "stage_start" {
		t.Errorf("expected status=stage_start, got %s", event.Status)
	}
	if event.Action != "GPU Stack" {
		t.Errorf("expected action=GPU Stack, got %s", event.Action)
	}
	if event.Timestamp == "" {
		t.Error("expected timestamp to be set")
	}
}

func TestPrinter_JSONMode_ComponentStart(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "json", false, false)

	p.ComponentStart(1, 3, "gpu-operator", "v25.10.1", "installing")

	output := strings.TrimSpace(buf.String())
	var event Event
	if err := json.Unmarshal([]byte(output), &event); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}
	if event.Component != "gpu-operator" {
		t.Errorf("expected component=gpu-operator, got %s", event.Component)
	}
	if event.Action != "installing" {
		t.Errorf("expected action=installing, got %s", event.Action)
	}
	if event.Status != "start" {
		t.Errorf("expected status=start, got %s", event.Status)
	}
}

func TestPrinter_JSONMode_Errorf(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "json", false, false)

	p.Errorf("something %s", "broke")

	output := strings.TrimSpace(buf.String())
	var event Event
	if err := json.Unmarshal([]byte(output), &event); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}
	if event.Status != "error" {
		t.Errorf("expected status=error, got %s", event.Status)
	}
	if !strings.Contains(event.Message, "something broke") {
		t.Errorf("expected message containing 'something broke', got %s", event.Message)
	}
}

func TestPrinter_JSONMode_Infof(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "json", false, false)

	p.Infof("deploy complete")

	output := strings.TrimSpace(buf.String())
	var event Event
	if err := json.Unmarshal([]byte(output), &event); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}
	if event.Status != "info" {
		t.Errorf("expected status=info, got %s", event.Status)
	}
}

func TestPrinter_Debugf_VerboseMode(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "text", false, true) // verbose=true

	p.Debugf("debug message: %d", 42)

	output := buf.String()
	if !strings.Contains(output, "[debug]") {
		t.Errorf("expected [debug] prefix in verbose mode, got: %q", output)
	}
	if !strings.Contains(output, "debug message: 42") {
		t.Errorf("expected debug message in output, got: %q", output)
	}
}

func TestPrinter_Debugf_NonVerboseMode(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "text", false, false) // verbose=false

	p.Debugf("debug message")

	if buf.Len() != 0 {
		t.Errorf("Debugf should be suppressed in non-verbose mode, got: %q", buf.String())
	}
}

func TestPrinter_ComponentSkipped(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "text", false, false)

	p.ComponentSkipped(1, 3, "cert-manager", "v1.17.2", "already installed")

	output := buf.String()
	if !strings.Contains(output, "[1/3]") {
		t.Errorf("expected [1/3] in output, got: %q", output)
	}
	if !strings.Contains(output, "already installed") {
		t.Errorf("expected 'already installed' in output, got: %q", output)
	}
}

func TestPrinter_PatchApplied(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "text", false, false)

	p.PatchApplied("cgroup-entrypoint")

	output := buf.String()
	if !strings.Contains(output, "cgroup-entrypoint") {
		t.Errorf("expected patch name in output, got: %q", output)
	}
	if !strings.Contains(output, "applied") {
		t.Errorf("expected 'applied' in output, got: %q", output)
	}
}

func TestNewWithWriter_NotTTY(t *testing.T) {
	var buf bytes.Buffer
	p := NewWithWriter(&buf, "text", false, false)

	// isTTY should be false for non-file writers.
	if p.isTTY {
		t.Error("expected isTTY=false for buffer writer")
	}
}
