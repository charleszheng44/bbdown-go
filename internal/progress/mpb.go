package progress

import (
	"fmt"
	"io"
	"sync"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// mpbManager renders animated bars to a TTY via vbauerster/mpb.
type mpbManager struct {
	p    *mpb.Progress
	mu   sync.Mutex
	out  io.Writer
	done bool
}

func newMpbManager(out io.Writer) *mpbManager {
	p := mpb.New(
		mpb.WithOutput(out),
		mpb.WithAutoRefresh(),
	)
	return &mpbManager{p: p, out: out}
}

func (m *mpbManager) Track(label string, total int64) Tracker {
	bar := m.p.New(
		total, // negative ok — mpb shows an indeterminate bar
		mpb.BarStyle().Lbound("[").Filler("#").Tip(">").Padding(" ").Rbound("]"),
		mpb.PrependDecorators(
			decor.Name(fmt.Sprintf("%-7s", label)),
			decor.CountersKibiByte("% .2f / % .2f", decor.WCSyncWidth),
		),
		mpb.AppendDecorators(
			decor.Percentage(decor.WCSyncWidth),
			decor.Name(" "),
			decor.AverageSpeed(decor.SizeB1024(0), "% .2f", decor.WCSyncWidth),
			decor.Name(" "),
			decor.AverageETA(decor.ET_STYLE_GO, decor.WCSyncWidth),
		),
	)
	return &mpbTracker{bar: bar}
}

func (m *mpbManager) Println(format string, args ...any) {
	// mpb routes writes via its surface so they appear above the bars.
	fmt.Fprintf(m.p, format+"\n", args...)
}

func (m *mpbManager) Wait() {
	m.mu.Lock()
	if m.done {
		m.mu.Unlock()
		return
	}
	m.done = true
	m.mu.Unlock()
	m.p.Wait()
}

type mpbTracker struct {
	bar  *mpb.Bar
	last int64
}

func (t *mpbTracker) Update(downloaded, total int64) {
	if total > 0 {
		t.bar.SetTotal(total, downloaded >= total)
	}
	t.bar.SetCurrent(downloaded)
	t.last = downloaded
}

func (t *mpbTracker) Abort() {
	t.bar.Abort(false) // false = leave the partial bar visible
}
