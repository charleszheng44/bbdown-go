package progress

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{"auto", ModeAuto, false},
		{"always", ModeAlways, false},
		{"never", ModeNever, false},
		{"plain", ModePlain, false},
		{"AUTO", ModeAuto, false}, // case-insensitive
		{"", 0, true},             // empty rejected
		{"verbose", 0, true},      // unknown rejected
	}
	for _, c := range cases {
		got, err := ParseMode(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseMode(%q): want error, got nil", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMode(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseMode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestNew_NeverProducesNoOutput is the contract for ModeNever: no bytes,
// no panics, even when callers feed bogus values.
func TestNew_NeverProducesNoOutput(t *testing.T) {
	var buf bytes.Buffer
	mgr := New(&buf, ModeNever)
	defer mgr.Wait()

	mgr.Println("ignored line %d", 1)
	tr := mgr.Track("video", 100)
	tr.Update(50, 100)
	tr.Update(100, 100)
	tr.Abort()

	if buf.Len() != 0 {
		t.Errorf("ModeNever wrote %d bytes (%q), want 0", buf.Len(), buf.String())
	}
}

// TestNew_PlainEmitsPeriodicLines drives the plain renderer with a fake
// clock and asserts: (1) Println goes through, (2) the throttled tick
// emits one line per stream after the interval has elapsed, and
// (3) completion forces a final line even before the next tick.
func TestNew_PlainEmitsPeriodicLines(t *testing.T) {
	var buf bytes.Buffer
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	mgr := newPlainManager(&buf, clock)
	defer mgr.Wait()

	mgr.Println("Part 1/2: Episode One")

	tr := mgr.Track("video", 1000)
	// First Update establishes baseline; nothing emitted yet because
	// no time has passed since Track.
	tr.Update(100, 1000)

	clock.advance(plainTickInterval - time.Millisecond)
	tr.Update(400, 1000) // still under the throttle, nothing
	clock.advance(2 * time.Millisecond)
	tr.Update(500, 1000) // now over the threshold, one line should land

	tr.Update(1000, 1000) // completion forces a final line regardless of clock

	out := buf.String()
	if !strings.Contains(out, "Part 1/2: Episode One") {
		t.Errorf("output missing Println line: %q", out)
	}
	// The 50% tick line and the 100% completion line must both land.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if got := countContaining(lines, "video:"); got != 2 {
		t.Errorf("video tick lines = %d, want 2; full output:\n%s", got, out)
	}
	if !strings.Contains(out, "100%") {
		t.Errorf("output missing 100%% completion line: %q", out)
	}
}

func countContaining(lines []string, sub string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }
