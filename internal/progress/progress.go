// Package progress renders download progress to the terminal. It owns
// nothing about HTTP — callers feed it byte counts via the Tracker
// interface, which matches internal/download.Options.OnProgress.
package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// Mode controls how progress is displayed.
type Mode int

const (
	// ModeAuto renders animated bars when the writer is a TTY and
	// periodic plain lines otherwise. The default.
	ModeAuto Mode = iota
	// ModeAlways forces animated bars regardless of TTY detection.
	ModeAlways
	// ModeNever suppresses all progress output.
	ModeNever
	// ModePlain forces periodic plain text lines regardless of TTY.
	ModePlain
)

// ParseMode maps a flag value to a Mode. Empty and unknown strings
// return an error suitable for surfacing through cobra.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(s) {
	case "auto":
		return ModeAuto, nil
	case "always":
		return ModeAlways, nil
	case "never":
		return ModeNever, nil
	case "plain":
		return ModePlain, nil
	default:
		return 0, fmt.Errorf("progress: unknown mode %q (want auto|always|never|plain)", s)
	}
}

// Manager owns the rendering surface for one URL. Construct one per
// call to processPart; defer Wait() to flush.
type Manager interface {
	// Track registers a new bar. label is "video", "audio", etc. total
	// may be -1 if unknown at registration time; subsequent Update calls
	// may pass a corrected non-negative total.
	Track(label string, total int64) Tracker

	// Println writes a line above the bars. Safe to call when no bars
	// are active.
	Println(format string, args ...any)

	// Wait flushes all bars to a final state and releases the rendering
	// surface. Idempotent.
	Wait()
}

// Tracker matches the shape internal/download.Options.OnProgress expects.
type Tracker interface {
	// Update reports cumulative bytes downloaded so far. total mirrors
	// the OnProgress contract: -1 if unknown.
	Update(downloaded, total int64)

	// Abort marks this stream as failed; the renderer leaves the bar
	// in a "stopped" state instead of completing it.
	Abort()
}

// New picks the implementation based on mode and whether out is a TTY.
// Output is whatever the caller passes — typically os.Stderr so stdout
// stays clean for redirection.
func New(out io.Writer, mode Mode) Manager {
	switch mode {
	case ModeNever:
		return noopManager{}
	case ModeAlways:
		return newMpbManager(out)
	case ModePlain:
		return newPlainManager(out, realClock{})
	case ModeAuto:
		fallthrough
	default:
		if isTerminal(out) {
			return newMpbManager(out)
		}
		return newPlainManager(out, realClock{})
	}
}

// isTerminal reports whether out is a TTY. Anything that isn't an
// *os.File is treated as non-TTY (test buffers, pipes, etc.).
func isTerminal(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// ─── clock indirection (so tests don't sleep) ───────────────────────

type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// ─── noop ──────────────────────────────────────────────────────────

type noopManager struct{}

func (noopManager) Track(string, int64) Tracker      { return noopTracker{} }
func (noopManager) Println(string, ...any)           {}
func (noopManager) Wait()                             {}

type noopTracker struct{}

func (noopTracker) Update(int64, int64) {}
func (noopTracker) Abort()              {}

// ─── plain (periodic text lines) ───────────────────────────────────

// plainTickInterval is how often each tracker is allowed to emit a
// progress line. Completion and abort bypass the throttle.
const plainTickInterval = 5 * time.Second

type plainManager struct {
	mu  sync.Mutex
	out io.Writer
	clk clock
}

func newPlainManager(out io.Writer, clk clock) *plainManager {
	return &plainManager{out: out, clk: clk}
}

func (m *plainManager) Track(label string, total int64) Tracker {
	return &plainTracker{
		mgr:      m,
		label:    label,
		total:    total,
		lastEmit: m.clk.Now(),
	}
}

func (m *plainManager) Println(format string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fmt.Fprintf(m.out, format+"\n", args...)
}

func (m *plainManager) Wait() {}

func (m *plainManager) write(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fmt.Fprintln(m.out, s)
}

type plainTracker struct {
	mu       sync.Mutex
	mgr      *plainManager
	label    string
	total    int64
	lastEmit time.Time
	done     bool
}

func (t *plainTracker) Update(downloaded, total int64) {
	t.mu.Lock()
	if total > 0 {
		t.total = total
	}
	complete := t.total > 0 && downloaded >= t.total
	now := t.mgr.clk.Now()
	tickDue := now.Sub(t.lastEmit) >= plainTickInterval
	if !complete && !tickDue {
		t.mu.Unlock()
		return
	}
	if complete && t.done {
		t.mu.Unlock()
		return
	}
	if complete {
		t.done = true
	}
	t.lastEmit = now
	line := t.format(downloaded)
	t.mu.Unlock()
	t.mgr.write(line)
}

func (t *plainTracker) Abort() {
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return
	}
	t.done = true
	t.mu.Unlock()
	t.mgr.write(t.label + ": aborted")
}

func (t *plainTracker) format(downloaded int64) string {
	if t.total <= 0 {
		return fmt.Sprintf("%s: %s", t.label, formatBytes(downloaded))
	}
	pct := int(downloaded * 100 / t.total)
	return fmt.Sprintf("%s: %d%% (%s/%s)",
		t.label, pct, formatBytes(downloaded), formatBytes(t.total))
}

// formatBytes renders n as a short human string (B, KiB, MiB, GiB).
func formatBytes(n int64) string {
	const (
		k = 1 << 10
		m = 1 << 20
		g = 1 << 30
	)
	switch {
	case n >= g:
		return fmt.Sprintf("%.2f GiB", float64(n)/float64(g))
	case n >= m:
		return fmt.Sprintf("%.2f MiB", float64(n)/float64(m))
	case n >= k:
		return fmt.Sprintf("%.2f KiB", float64(n)/float64(k))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
