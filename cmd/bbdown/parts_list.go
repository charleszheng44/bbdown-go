package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/charleszheng44/bbdown-go/internal/api"
	"github.com/charleszheng44/bbdown-go/internal/parser"
	"github.com/spf13/cobra"
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

// newPartsCmd returns the `bbdown parts <url>` subcommand. It prints an
// aligned page / duration / title table for the given URL so the user can
// build a --part specifier. Reuses the same cookie + client setup as the
// download path.
func newPartsCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:           "parts <url>",
		Short:         "List the pages of a Bilibili item",
		Long:          "Fetch metadata for the given URL or ID and print page number, duration, and title for each page.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			debugMode = flags.Debug
			return runParts(cmd.Context(), cmd.OutOrStdout(), flags, args[0])
		},
	}
}

// runParts fetches the page list for rawURL and writes the aligned
// parts table to w. Returns any cookie, network, or parse error
// encountered along the way; surface-level formatting is delegated
// to renderParts.
func runParts(ctx context.Context, w io.Writer, flags *rootFlags, rawURL string) error {
	cookies, err := loadCookies(flags)
	if err != nil {
		return err
	}
	client := api.NewClient(cookies.AsJar(), "")
	if cookies.TV != nil {
		client.SetAppAuth(cookies.TV)
	}
	target, err := parser.Classify(rawURL)
	if err != nil {
		return err
	}
	info, err := client.FetchPlayInfo(ctx, target, 1)
	if err != nil {
		return err
	}
	return renderParts(w, info.Title, info.Parts)
}
