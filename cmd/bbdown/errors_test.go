package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charleszheng44/bbdown-go/internal/api"
	"github.com/charleszheng44/bbdown-go/internal/auth"
	"github.com/charleszheng44/bbdown-go/internal/download"
	"github.com/charleszheng44/bbdown-go/internal/mux"
	"github.com/charleszheng44/bbdown-go/internal/parser"
)

func TestFormatError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		want    string
		substr  bool // if true, want is a substring match
		inDebug bool
	}{
		{name: "nil", err: nil, want: ""},
		{name: "not_logged_in", err: auth.ErrNotLoggedIn, want: "Not logged in. Run `bbdown login` first."},
		{name: "qr_expired", err: auth.ErrQRExpired, want: "QR code expired. Run `bbdown login` again."},
		{name: "qr_canceled", err: auth.ErrQRCanceled, want: "Login canceled."},
		{name: "content_locked", err: api.ErrContentLocked, want: "This content requires a purchase or is region-locked."},
		{name: "content_locked_wrapped", err: fmt.Errorf("%w: 87008", api.ErrContentLocked), want: "This content requires a purchase or is region-locked."},
		{name: "rate_limited", err: api.ErrRateLimited, want: "Rate-limited by Bilibili. Retry after a short wait."},
		{name: "unknown_response", err: api.ErrUnknownResponse, want: "Unexpected response from Bilibili", substr: true},
		{name: "unknown_response_debug", err: fmt.Errorf("%w: code 12345", api.ErrUnknownResponse), want: "Unexpected Bilibili response", substr: true, inDebug: true},
		{name: "ffmpeg_missing", err: mux.ErrFFmpegMissing, want: "ffmpeg not found on PATH. Install from https://ffmpeg.org."},
		{name: "download_server_error", err: download.ErrServerError, want: "Download failed", substr: true},
		{name: "download_canceled", err: download.ErrCanceled, want: "Download canceled."},
		{name: "download_partial", err: download.ErrPartialDownload, want: "Download ended before the full file was received. Retry."},
		{name: "parser_empty", err: parser.ErrEmptyInput, want: "No URL given."},
		{name: "parser_unknown_format", err: parser.ErrUnknownFormat, want: "Unrecognized Bilibili URL or ID."},
		{name: "parser_short_link", err: parser.ErrShortLinkUnsupported, want: "b23.tv short links are not supported; paste the resolved URL instead."},
		{name: "parts_spec", err: fmt.Errorf("%w: page 99 exceeds total 3", ErrPartSpec), want: "invalid --part specifier", substr: true},
		{name: "disk_full", err: errors.New("write /tmp/foo: no space left on device"), want: "Out of disk space", substr: true},
		{name: "generic", err: errors.New("random failure"), want: "random failure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := debugMode
			debugMode = tt.inDebug
			defer func() { debugMode = prev }()

			got := formatError(tt.err)
			if tt.substr {
				if !strings.Contains(got, tt.want) {
					t.Fatalf("want substring %q, got %q", tt.want, got)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
