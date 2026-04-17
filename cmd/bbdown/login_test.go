package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveImportedCookie(t *testing.T) {
	const sample = "SESSDATA=abc; bili_jct=xyz; DedeUserID=42; DedeUserID__ckMd5=deadbeef"

	t.Run("no_flags_returns_empty", func(t *testing.T) {
		got, err := resolveImportedCookie(strings.NewReader(""), &bytes.Buffer{}, "", "", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("want empty string for QR fallback, got %q", got)
		}
	})

	t.Run("flag_cookie", func(t *testing.T) {
		got, err := resolveImportedCookie(strings.NewReader(""), &bytes.Buffer{}, sample, "", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != sample {
			t.Errorf("got %q, want %q", got, sample)
		}
	})

	t.Run("cookie_file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "cookie.txt")
		if err := os.WriteFile(path, []byte("  "+sample+"\n"), 0o600); err != nil {
			t.Fatalf("write temp cookie: %v", err)
		}
		got, err := resolveImportedCookie(strings.NewReader(""), &bytes.Buffer{}, "", path, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != sample {
			t.Errorf("got %q, want %q", got, sample)
		}
	})

	t.Run("cookie_file_missing", func(t *testing.T) {
		_, err := resolveImportedCookie(strings.NewReader(""), &bytes.Buffer{}, "", "/does/not/exist", false)
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("cookie_stdin", func(t *testing.T) {
		var out bytes.Buffer
		got, err := resolveImportedCookie(strings.NewReader(sample+"\n"), &out, "", "", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != sample {
			t.Errorf("got %q, want %q", got, sample)
		}
		if !strings.Contains(out.String(), "Paste the cookie") {
			t.Errorf("expected stdin prompt on stdout, got %q", out.String())
		}
	})

	t.Run("two_flags_error", func(t *testing.T) {
		cases := []struct {
			name        string
			flagCookie  string
			cookieFile  string
			cookieStdin bool
		}{
			{"cookie_and_file", sample, "/tmp/x", false},
			{"cookie_and_stdin", sample, "", true},
			{"file_and_stdin", "", "/tmp/x", true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := resolveImportedCookie(strings.NewReader(""), &bytes.Buffer{}, tc.flagCookie, tc.cookieFile, tc.cookieStdin)
				if err == nil {
					t.Fatal("expected error when multiple sources are set")
				}
				if !strings.Contains(err.Error(), "at most one of") {
					t.Errorf("unexpected error text: %v", err)
				}
			})
		}
	})

	t.Run("all_three_flags_error", func(t *testing.T) {
		_, err := resolveImportedCookie(strings.NewReader(""), &bytes.Buffer{}, sample, "/tmp/x", true)
		if err == nil {
			t.Fatal("expected error when all three sources are set")
		}
	})
}
