package main

import (
	"testing"
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
