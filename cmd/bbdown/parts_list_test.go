package main

import "testing"

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{-1, "--:--"},
		{0, "--:--"},
		{1, "00:01"},
		{59, "00:59"},
		{60, "01:00"},
		{3599, "59:59"},
		{3600, "01:00:00"},
		{3661, "01:01:01"},
		{36000, "10:00:00"},
	}
	for _, tc := range cases {
		got := formatDuration(tc.in)
		if got != tc.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
