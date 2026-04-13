package config

import (
	"strconv"
	"strings"
)

// TemplateVars holds the substitution values available when rendering an
// output filename template. Zero values are allowed; any unreferenced field
// is simply ignored.
type TemplateVars struct {
	Title     string
	Page      int
	PageTitle string
	BVID      string
	AID       string
	Quality   string
	Codec     string
}

// unsafeRunes is the set of characters stripped from every substituted value
// and from the final rendered path. It is the union of characters that are
// illegal on Windows NTFS (\ / : * ? " < > |) and that would otherwise be
// interpreted as path separators on POSIX. Forward slashes are removed so a
// title containing "/" does not accidentally create a subdirectory.
const unsafeRunes = `/\:*?"<>|`

// stripUnsafe removes every rune in unsafeRunes from s.
func stripUnsafe(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if strings.ContainsRune(unsafeRunes, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// collapseWhitespace collapses any run of Unicode whitespace (spaces, tabs,
// newlines, etc.) to a single ASCII space. It does not trim leading or
// trailing whitespace; trimPathSegments handles segment edges.
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if isSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// isSpace reports whether r is whitespace for template-rendering purposes.
// It matches ASCII space, tab, CR, LF, vertical tab, and form feed. Matching
// strings.Fields-style whitespace here avoids pulling in unicode tables for
// what is, in practice, always ASCII-dominated filename input.
func isSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	// Also treat non-breaking space as whitespace; it is common in
	// Bilibili-sourced titles and would otherwise survive as U+00A0.
	return r == '\u00A0'
}

// trimPathSegments trims trailing dots and spaces from each path segment of
// s. Windows refuses to create files or directories whose names end in "."
// or " "; POSIX allows them but they are an ergonomic hazard. The separator
// itself ('/') is preserved so a template that deliberately embeds a path
// (e.g. "<title>/P<page>") still produces a multi-segment result.
func trimPathSegments(s string) string {
	if s == "" {
		return s
	}
	parts := strings.Split(s, "/")
	for i, p := range parts {
		parts[i] = strings.TrimRight(p, ". ")
	}
	return strings.Join(parts, "/")
}

// Render substitutes the template variables in tmpl and sanitizes the
// result. Recognized placeholders are:
//
//	<title>, <page>, <pageTitle>, <bvid>, <aid>, <quality>, <codec>
//
// Sanitization steps applied in order:
//  1. Each substituted value has unsafe runes stripped before insertion so
//     that a slash inside a title cannot introduce a new path segment.
//  2. The whole rendered string is stripped of any remaining unsafe runes
//     other than '/' (templates may legitimately embed path separators).
//  3. Runs of whitespace are collapsed to a single ASCII space.
//  4. Trailing dots and spaces are trimmed from every path segment.
func Render(template string, vars TemplateVars) string {
	replacer := strings.NewReplacer(
		"<title>", stripUnsafe(vars.Title),
		"<page>", strconv.Itoa(vars.Page),
		"<pageTitle>", stripUnsafe(vars.PageTitle),
		"<bvid>", stripUnsafe(vars.BVID),
		"<aid>", stripUnsafe(vars.AID),
		"<quality>", stripUnsafe(vars.Quality),
		"<codec>", stripUnsafe(vars.Codec),
	)
	out := replacer.Replace(template)

	// Strip unsafe runes from the rendered result, but keep '/' so a
	// template like "<title>/P<page>" retains its path structure.
	var b strings.Builder
	b.Grow(len(out))
	for _, r := range out {
		if r != '/' && strings.ContainsRune(unsafeRunes, r) {
			continue
		}
		b.WriteRune(r)
	}
	out = b.String()

	out = collapseWhitespace(out)
	out = trimPathSegments(out)
	return out
}
