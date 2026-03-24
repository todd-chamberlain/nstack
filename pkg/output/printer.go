package output

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// Printer handles all user-facing output. It supports text mode
// (human-readable with alignment) and JSON mode (JSONL for CI/scripting).
type Printer struct {
	format  string // "text" or "json"
	quiet   bool
	verbose bool
	isTTY   bool
	out     *os.File

	// mu guards writes so output is safe from concurrent goroutines.
	mu sync.Mutex

	// index tracks the current component index within a stage for alignment.
	index int
	total int
}

// New creates a Printer configured for the given format and verbosity.
func New(format string, quiet, verbose bool) *Printer {
	return &Printer{
		format:  format,
		quiet:   quiet,
		verbose: verbose,
		isTTY:   term.IsTerminal(int(os.Stdout.Fd())),
		out:     os.Stdout,
	}
}

// dotPad returns a dot-leader string that pads name+version to a fixed width.
func dotPad(text string, width int) string {
	pad := width - len(text)
	if pad < 3 {
		pad = 3
	}
	return " " + strings.Repeat(".", pad) + " "
}

// StageHeader prints a stage header line.
// Text mode: "\nStage N: Name\n"
func (p *Printer) StageHeader(stage int, name string) {
	if p.format == "json" {
		p.EmitJSON(Event{
			Stage:  stage,
			Status: "stage_start",
			Action: name,
		})
		return
	}
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.out, "\nStage %d: %s\n", stage, name)
}

// ComponentStart prints the beginning of a component action line. The line
// is left open (no newline) so ComponentDone can append the result.
// Text mode: "  [1/3] component-name v1.2.3 .............. action"
func (p *Printer) ComponentStart(index, total int, name, version, action string) {
	p.index = index
	p.total = total

	if p.format == "json" {
		p.EmitJSON(Event{
			Component: name,
			Action:    action,
			Status:    "start",
			Message:   version,
		})
		return
	}
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	label := fmt.Sprintf("[%d/%d] %s %s", index, total, name, version)
	dots := dotPad(label, 50)
	fmt.Fprintf(p.out, "  %s%s%s", label, dots, action)
}

// ComponentDone finishes the current component line with a success or failure
// indicator.
func (p *Printer) ComponentDone(name string, err error) {
	if p.format == "json" {
		if err != nil {
			p.EmitJSON(Event{
				Component: name,
				Status:    "failed",
				Error:     err.Error(),
			})
		} else {
			p.EmitJSON(Event{
				Component: name,
				Status:    "done",
			})
		}
		return
	}
	if p.quiet && err == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if err != nil {
		fmt.Fprintf(p.out, " FAILED: %v\n", err)
	} else {
		fmt.Fprintln(p.out, " \u2713")
	}
}

// ComponentSkipped prints a complete component line for a skipped component.
// Text mode: "  [1/3] component-name v1.2.3 .............. reason ✓"
func (p *Printer) ComponentSkipped(index, total int, name, version, reason string) {
	if p.format == "json" {
		p.EmitJSON(Event{
			Component: name,
			Status:    "skipped",
			Message:   reason,
		})
		return
	}
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	label := fmt.Sprintf("[%d/%d] %s %s", index, total, name, version)
	dots := dotPad(label, 50)
	fmt.Fprintf(p.out, "  %s%s%s \u2713\n", label, dots, reason)
}

// PatchApplied prints a sub-item line indicating a patch was applied.
// Text mode: "        → name ............ applied ✓"
func (p *Printer) PatchApplied(name string) {
	if p.format == "json" {
		p.EmitJSON(Event{
			Component: name,
			Action:    "patch",
			Status:    "applied",
		})
		return
	}
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	dots := dotPad(name, 30)
	fmt.Fprintf(p.out, "        \u2192 %s%sapplied \u2713\n", name, dots)
}

// WaitProgress prints a progress update for pod readiness.
// Text mode: "        → Waiting for pods (12s) .......... 3/5 ready"
func (p *Printer) WaitProgress(component string, elapsed time.Duration, ready, total int) {
	if p.format == "json" {
		p.EmitJSON(Event{
			Component: component,
			Status:    "waiting",
			Message:   fmt.Sprintf("%d/%d ready (%s)", ready, total, elapsed.Truncate(time.Second)),
		})
		return
	}
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	label := fmt.Sprintf("Waiting for pods (%s)", elapsed.Truncate(time.Second))
	dots := dotPad(label, 38)
	if p.isTTY {
		// Overwrite the current line for a cleaner progress display.
		fmt.Fprintf(p.out, "\r        \u2192 %s%s%d/%d ready", label, dots, ready, total)
	} else {
		fmt.Fprintf(p.out, "        \u2192 %s%s%d/%d ready\n", label, dots, ready, total)
	}
}

// DeployComplete prints the final deployment summary.
func (p *Printer) DeployComplete(duration time.Duration, services map[string]string) {
	if p.format == "json" {
		p.EmitJSON(Event{
			Status:  "complete",
			Message: fmt.Sprintf("deployed in %s", duration.Truncate(time.Second)),
		})
		for name, url := range services {
			p.EmitJSON(Event{
				Component: name,
				Status:    "service",
				Message:   url,
			})
		}
		return
	}
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	fmt.Fprintf(p.out, "\nDeploy complete in %s\n", duration.Truncate(time.Second))
	if len(services) > 0 {
		fmt.Fprintln(p.out, "\nServices:")
		for name, url := range services {
			fmt.Fprintf(p.out, "  %s: %s\n", name, url)
		}
	}
}

// Errorf prints an error message. It always prints, even in quiet mode.
func (p *Printer) Errorf(format string, args ...interface{}) {
	if p.format == "json" {
		p.EmitJSON(Event{
			Status:  "error",
			Message: fmt.Sprintf(format, args...),
		})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.out, "Error: "+format+"\n", args...)
}

// Infof prints an informational message. Suppressed in quiet mode.
func (p *Printer) Infof(format string, args ...interface{}) {
	if p.format == "json" {
		p.EmitJSON(Event{
			Status:  "info",
			Message: fmt.Sprintf(format, args...),
		})
		return
	}
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.out, format+"\n", args...)
}

// Debugf prints a debug message. Only shown in verbose mode.
func (p *Printer) Debugf(format string, args ...interface{}) {
	if !p.verbose {
		return
	}
	if p.format == "json" {
		p.EmitJSON(Event{
			Status:  "debug",
			Message: fmt.Sprintf(format, args...),
		})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.out, "[debug] "+format+"\n", args...)
}
