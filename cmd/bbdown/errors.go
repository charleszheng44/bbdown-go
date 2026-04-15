package main

import (
	"errors"
	"fmt"

	"github.com/charleszheng44/bbdown-go/internal/api"
	"github.com/charleszheng44/bbdown-go/internal/auth"
	"github.com/charleszheng44/bbdown-go/internal/download"
	"github.com/charleszheng44/bbdown-go/internal/mux"
	"github.com/charleszheng44/bbdown-go/internal/parser"
)

// formatError translates a typed error from any internal package into a
// single-line, user-facing message per design spec §9. The debug flag, when
// true, appends the wrapped error's text for API responses we could not
// classify.
func formatError(err error) string {
	if err == nil {
		return ""
	}

	switch {
	case errors.Is(err, auth.ErrNotLoggedIn):
		return "Not logged in. Run `bbdown login` first."
	case errors.Is(err, auth.ErrQRExpired):
		return "QR code expired. Run `bbdown login` again."
	case errors.Is(err, auth.ErrQRCanceled):
		return "Login canceled."
	case errors.Is(err, api.ErrContentLocked):
		return "This content requires a purchase or is region-locked."
	case errors.Is(err, api.ErrRateLimited):
		return "Rate-limited by Bilibili. Retry after a short wait."
	case errors.Is(err, api.ErrUnknownResponse):
		if debugMode {
			return fmt.Sprintf("Unexpected Bilibili response: %v", err)
		}
		return "Unexpected response from Bilibili. Re-run with --debug to see the raw payload."
	case errors.Is(err, mux.ErrFFmpegMissing):
		return "ffmpeg not found on PATH. Install from https://ffmpeg.org."
	case errors.Is(err, download.ErrServerError):
		return fmt.Sprintf("Download failed: %v", err)
	case errors.Is(err, download.ErrCanceled):
		return "Download canceled."
	case errors.Is(err, download.ErrPartialDownload):
		return "Download ended before the full file was received. Retry."
	case errors.Is(err, parser.ErrEmptyInput):
		return "No URL given."
	case errors.Is(err, parser.ErrUnknownFormat):
		return "Unrecognized Bilibili URL or ID."
	case errors.Is(err, parser.ErrShortLinkUnsupported):
		return "b23.tv short links are not supported; paste the resolved URL instead."
	case errors.Is(err, ErrPartSpec):
		return fmt.Sprintf("%v", err)
	}

	// Heuristic match for ENOSPC / disk-full conditions surfaced as raw
	// syscall errors by the downloader. spec §9 lists download.ErrDiskFull
	// even though the package does not export one yet; future-proof the
	// table so the message still fires by name.
	if isDiskFull(err) {
		return fmt.Sprintf("Out of disk space: %v", err)
	}

	return err.Error()
}

// isDiskFull reports whether err's chain contains a "no space left" message.
// The Go runtime surfaces ENOSPC as a *os.PathError whose underlying syscall
// error matches; rather than taking a syscall dependency (which is GOOS-
// specific) we do a string match, which is adequate for a single user-facing
// message.
func isDiskFull(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, needle := range []string{"no space left", "disk full", "ENOSPC"} {
		if containsFold(msg, needle) {
			return true
		}
	}
	return false
}

// containsFold is a small, dependency-free case-insensitive substring check.
func containsFold(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a := s[i+j]
			b := sub[j]
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
