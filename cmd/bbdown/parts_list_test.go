package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charleszheng44/bbdown-go/internal/api"
)

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"negative", -1, "--:--"},
		{"zero", 0, "--:--"},
		{"one_second", 1, "00:01"},
		{"fifty_nine_seconds", 59, "00:59"},
		{"one_minute", 60, "01:00"},
		{"just_under_one_hour", 3599, "59:59"},
		{"one_hour", 3600, "01:00:00"},
		{"over_one_hour", 3661, "01:01:01"},
		{"ten_hours", 36000, "10:00:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatDuration(tc.in)
			if got != tc.want {
				t.Errorf("formatDuration(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRenderParts(t *testing.T) {
	t.Run("single_part", func(t *testing.T) {
		var buf bytes.Buffer
		parts := []api.Part{
			{Page: 1, CID: "1", Title: "Only page", Duration: 125},
		}
		if err := renderParts(&buf, "My Title", parts); err != nil {
			t.Fatalf("renderParts: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "My Title\n") {
			t.Errorf("missing title header; got:\n%s", out)
		}
		if !strings.Contains(out, "P#") || !strings.Contains(out, "DURATION") || !strings.Contains(out, "TITLE") {
			t.Errorf("missing column header; got:\n%s", out)
		}
		if !strings.Contains(out, "02:05") || !strings.Contains(out, "Only page") {
			t.Errorf("missing row content; got:\n%s", out)
		}
	})
	t.Run("multiple_parts", func(t *testing.T) {
		var buf bytes.Buffer
		parts := []api.Part{
			{Page: 1, Title: "Opening", Duration: 201},
			{Page: 2, Title: "Chapter 1", Duration: 765},
			{Page: 3, Title: "Chapter 2", Duration: 1082},
		}
		if err := renderParts(&buf, "Series", parts); err != nil {
			t.Fatalf("renderParts: %v", err)
		}
		out := buf.String()
		for _, want := range []string{"Series", "03:21", "12:45", "18:02", "Opening", "Chapter 1", "Chapter 2"} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		// title + header + 3 rows
		if len(lines) != 5 {
			t.Errorf("want 5 lines, got %d:\n%s", len(lines), out)
		}
	})
	t.Run("over_one_hour", func(t *testing.T) {
		var buf bytes.Buffer
		parts := []api.Part{
			{Page: 1, Title: "Short", Duration: 120},
			{Page: 2, Title: "Long", Duration: 3725}, // 01:02:05
		}
		if err := renderParts(&buf, "Mixed", parts); err != nil {
			t.Fatalf("renderParts: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "01:02:05") {
			t.Errorf("expected hh:mm:ss row; got:\n%s", out)
		}
		// Both rows must use hh:mm:ss once any row crosses an hour.
		if !strings.Contains(out, "00:02:00") {
			t.Errorf("expected sub-hour row promoted to hh:mm:ss; got:\n%s", out)
		}
	})
	t.Run("empty_fallback", func(t *testing.T) {
		var buf bytes.Buffer
		if err := renderParts(&buf, "Solo", nil); err != nil {
			t.Fatalf("renderParts: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "Solo") {
			t.Errorf("missing title; got:\n%s", out)
		}
		if !strings.Contains(out, "--:--") {
			t.Errorf("empty Parts should render a synthetic row with --:--; got:\n%s", out)
		}
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) < 3 {
			t.Fatalf("want title + header + synthetic row, got %d lines:\n%s", len(lines), out)
		}
		lastRow := lines[len(lines)-1]
		if !strings.Contains(lastRow, "Solo") || !strings.Contains(lastRow, "--:--") {
			t.Errorf("synthetic row missing title or sentinel: %q", lastRow)
		}
	})
}
