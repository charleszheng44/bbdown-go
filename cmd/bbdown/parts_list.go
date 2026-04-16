package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/charleszheng44/bbdown-go/internal/api"
)

// formatDuration renders a duration in seconds as mm:ss when under one hour
// and hh:mm:ss otherwise. Zero or negative input renders as "--:--" (used
// when the upstream API omitted the duration).
func formatDuration(seconds int) string {
	if seconds <= 0 {
		return "--:--"
	}
	if seconds < 3600 {
		return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, (seconds%3600)/60, seconds%60)
}

// renderParts writes title on the first line, then an aligned table of
// Page #, duration, and title — one row per Part. If parts is empty, a
// single synthetic row is emitted so callers always see a uniform shape.
//
// Duration column width is uniform per invocation: if any part is >= 1h,
// every row renders as hh:mm:ss; otherwise mm:ss.
func renderParts(w io.Writer, title string, parts []api.Part) error {
	if _, err := fmt.Fprintln(w, title); err != nil {
		return err
	}

	if len(parts) == 0 {
		parts = []api.Part{{Page: 1, Duration: 0, Title: title}}
	}

	useHours := false
	for _, p := range parts {
		if p.Duration >= 3600 {
			useHours = true
			break
		}
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "P#\tDURATION\tTITLE"); err != nil {
		return err
	}
	for _, p := range parts {
		dur := formatDuration(p.Duration)
		// Promote sub-hour rows so the column width is uniform.
		// Duration<=0 stays as the "--:--" sentinel, unchanged.
		if useHours && p.Duration > 0 && p.Duration < 3600 {
			dur = fmt.Sprintf("00:%02d:%02d", p.Duration/60, p.Duration%60)
		}
		if _, err := fmt.Fprintf(tw, "%d\t%s\t%s\n", p.Page, dur, p.Title); err != nil {
			return err
		}
	}
	return tw.Flush()
}
