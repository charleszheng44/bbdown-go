// Package parser classifies Bilibili URLs and bare IDs into a normalized
// Target descriptor used by the rest of the pipeline.
//
// See docs/superpowers/specs/2026-04-13-bbdown-go-port-design.md §4 and §6.
package parser

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Kind identifies the Bilibili content family an input points at.
type Kind int

const (
	// KindRegular covers standard BV / av videos (x/web-interface/view).
	KindRegular Kind = iota
	// KindBangumi covers ep / ss links served by the pgc endpoints.
	KindBangumi
	// KindCourse covers purchased cheese (pugv) courses.
	KindCourse
)

// Target is the normalized descriptor produced by Classify. Exactly one of
// BVID, AID, EPID, or SSID is populated, reflecting the most specific ID
// encoded in the caller-supplied input. Raw preserves the original string,
// trimmed of surrounding whitespace.
type Target struct {
	Kind Kind
	BVID string
	AID  string
	EPID string
	SSID string
	Raw  string
}

// Sentinel errors returned by Classify. Callers should use errors.Is.
var (
	// ErrEmptyInput is returned when input is empty or whitespace only.
	ErrEmptyInput = errors.New("parser: empty input")
	// ErrUnknownFormat is returned when the input does not match any
	// supported Bilibili URL or ID pattern.
	ErrUnknownFormat = errors.New("parser: unknown format")
	// ErrShortLinkUnsupported is returned for b23.tv short links, which
	// require an HTTP redirect lookup and are out of scope for this package.
	ErrShortLinkUnsupported = errors.New("parser: b23.tv short links are not supported")
)

// Bare-ID patterns. Prefixes are matched case-insensitively but the captured
// numeric/alphanumeric body is preserved as-is for IDs that are not purely
// numeric (BV IDs). Anchored to the whole string so partial matches do not
// leak through.
var (
	bvIDRE = regexp.MustCompile(`^[Bb][Vv]([0-9A-Za-z]+)$`)
	avIDRE = regexp.MustCompile(`^[Aa][Vv](\d+)$`)
	epIDRE = regexp.MustCompile(`^[Ee][Pp](\d+)$`)
	ssIDRE = regexp.MustCompile(`^[Ss][Ss](\d+)$`)
)

// URL path patterns. The substring anchoring is intentionally loose because
// real-world Bilibili URLs append trailing segments (e.g. "/?p=2") and vary
// in casing of the BV prefix.
var (
	pathBVRE     = regexp.MustCompile(`/video/[Bb][Vv]([0-9A-Za-z]+)`)
	pathAVRE     = regexp.MustCompile(`/video/[Aa][Vv](\d+)`)
	pathBangumiE = regexp.MustCompile(`/bangumi/play/[Ee][Pp](\d+)`)
	pathBangumiS = regexp.MustCompile(`/bangumi/play/[Ss][Ss](\d+)`)
	pathCourseE  = regexp.MustCompile(`/cheese/play/[Ee][Pp](\d+)`)
	pathCourseS  = regexp.MustCompile(`/cheese/play/[Ss][Ss](\d+)`)
)

// Classify inspects input and returns a populated Target, or one of the
// sentinel errors. Inputs may be bare IDs (BV/av/ep/ss) or full HTTPS URLs;
// leading and trailing whitespace is ignored.
func Classify(input string) (Target, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return Target{}, ErrEmptyInput
	}

	// Try bare-ID forms first: the overwhelmingly common CLI case and
	// cheaper than URL parsing.
	if m := bvIDRE.FindStringSubmatch(raw); m != nil {
		return Target{Kind: KindRegular, BVID: "BV" + m[1], Raw: raw}, nil
	}
	if m := avIDRE.FindStringSubmatch(raw); m != nil {
		return Target{Kind: KindRegular, AID: m[1], Raw: raw}, nil
	}
	if m := epIDRE.FindStringSubmatch(raw); m != nil {
		return Target{Kind: KindBangumi, EPID: m[1], Raw: raw}, nil
	}
	if m := ssIDRE.FindStringSubmatch(raw); m != nil {
		return Target{Kind: KindBangumi, SSID: m[1], Raw: raw}, nil
	}

	// URL forms. Only consider strings that look URL-shaped so stray text
	// does not get silently accepted.
	if !looksLikeURL(raw) {
		return Target{}, ErrUnknownFormat
	}

	u, err := url.Parse(raw)
	if err != nil {
		return Target{}, ErrUnknownFormat
	}
	host := strings.ToLower(u.Host)

	// b23.tv short links resolve only via HTTP redirect; explicitly reject
	// so callers know to fetch before re-classifying.
	if host == "b23.tv" || strings.HasSuffix(host, ".b23.tv") {
		return Target{}, ErrShortLinkUnsupported
	}

	// Match against the path. Courses must be tested before bangumi because
	// "/cheese/play/ep..." and "/bangumi/play/ep..." share the same ep
	// capture shape but map to different Kinds.
	path := u.Path
	if m := pathCourseE.FindStringSubmatch(path); m != nil {
		return Target{Kind: KindCourse, EPID: m[1], Raw: raw}, nil
	}
	if m := pathCourseS.FindStringSubmatch(path); m != nil {
		return Target{Kind: KindCourse, SSID: m[1], Raw: raw}, nil
	}
	if m := pathBangumiE.FindStringSubmatch(path); m != nil {
		return Target{Kind: KindBangumi, EPID: m[1], Raw: raw}, nil
	}
	if m := pathBangumiS.FindStringSubmatch(path); m != nil {
		return Target{Kind: KindBangumi, SSID: m[1], Raw: raw}, nil
	}
	if m := pathBVRE.FindStringSubmatch(path); m != nil {
		return Target{Kind: KindRegular, BVID: "BV" + m[1], Raw: raw}, nil
	}
	if m := pathAVRE.FindStringSubmatch(path); m != nil {
		return Target{Kind: KindRegular, AID: m[1], Raw: raw}, nil
	}

	return Target{}, ErrUnknownFormat
}

// looksLikeURL is a cheap gate so arbitrary strings like "hello/world" do not
// reach url.Parse, which is permissive and happily accepts them.
func looksLikeURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
