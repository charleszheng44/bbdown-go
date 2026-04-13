package config

import (
	"strings"
	"testing"
)

func TestRenderSubstitutesAllVariables(t *testing.T) {
	vars := TemplateVars{
		Title:     "Hello",
		Page:      3,
		PageTitle: "Intro",
		BVID:      "BV1xx411c7mD",
		AID:       "170001",
		Quality:   "1080P 60",
		Codec:     "hevc",
	}
	got := Render("<title>-P<page>-<pageTitle>-<bvid>-<aid>-<quality>-<codec>", vars)
	want := "Hello-P3-Intro-BV1xx411c7mD-170001-1080P 60-hevc"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderStripsSlashesInTitle(t *testing.T) {
	// A '/' inside a value must never introduce a new path segment.
	vars := TemplateVars{Title: "foo/bar\\baz"}
	got := Render("<title>", vars)
	if strings.ContainsAny(got, `/\`) {
		t.Fatalf("Render = %q; must not contain path separators from value", got)
	}
	if got != "foobarbaz" {
		t.Fatalf("Render = %q, want %q", got, "foobarbaz")
	}
}

func TestRenderStripsWindowsUnsafeChars(t *testing.T) {
	vars := TemplateVars{Title: `a:b*c?d"e<f>g|h`}
	got := Render("<title>", vars)
	for _, r := range `\/:*?"<>|` {
		if strings.ContainsRune(got, r) {
			t.Fatalf("Render = %q still contains %q", got, r)
		}
	}
	if got != "abcdefgh" {
		t.Fatalf("Render = %q, want %q", got, "abcdefgh")
	}
}

func TestRenderKeepsTemplatePathSeparator(t *testing.T) {
	// Slashes written directly in the template (not in substituted values)
	// are preserved so the user can express subdirectory layouts.
	vars := TemplateVars{Title: "Show", Page: 2, PageTitle: "Ep"}
	got := Render("<title>/P<page>-<pageTitle>", vars)
	want := "Show/P2-Ep"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderCollapsesWhitespace(t *testing.T) {
	vars := TemplateVars{Title: "a   b\t\tc\n\nd"}
	got := Render("<title>", vars)
	want := "a b c d"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderTrimsTrailingDotsAndSpacesPerSegment(t *testing.T) {
	vars := TemplateVars{Title: "ShowName.. ", PageTitle: "Ep1.  "}
	got := Render("<title>/<pageTitle>", vars)
	want := "ShowName/Ep1"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderWithEmptyVars(t *testing.T) {
	// Every placeholder present, every value empty. Page is an int and so
	// renders as "0"; this is the intended behavior (callers that want to
	// elide the page number choose a template without <page>).
	got := Render("<title>-<pageTitle>-<bvid>-<aid>-<quality>-<codec>-P<page>", TemplateVars{})
	want := "------P0"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderLiteralTemplate(t *testing.T) {
	// Templates without any placeholder are returned as-is, modulo
	// sanitization. This keeps Render safe to call on user-supplied
	// strings that happen not to reference any variable.
	got := Render("plain-name", TemplateVars{Title: "ignored"})
	if got != "plain-name" {
		t.Fatalf("Render = %q, want %q", got, "plain-name")
	}
}

func TestRenderStripsUnsafeCharsWrittenDirectlyInTemplate(t *testing.T) {
	// Unsafe characters in the template itself (not just in values) are
	// also stripped so the output is always a legal path.
	got := Render("name:with?bad*chars", TemplateVars{})
	if strings.ContainsAny(got, `\:*?"<>|`) {
		t.Fatalf("Render = %q still contains unsafe chars", got)
	}
	if got != "namewithbadchars" {
		t.Fatalf("Render = %q, want %q", got, "namewithbadchars")
	}
}
