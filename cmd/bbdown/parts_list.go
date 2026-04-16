package main

import "fmt"

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
