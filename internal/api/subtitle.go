package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// bccDoc is the Bilibili Closed Caption JSON document. Each body entry is
// one subtitle cue. Only the fields we render are decoded.
type bccDoc struct {
	Body []struct {
		From    float64 `json:"from"`
		To      float64 `json:"to"`
		Content string  `json:"content"`
	} `json:"body"`
}

// FetchSubtitle downloads a BCC (Bilibili Closed Caption) JSON document and
// converts it to SRT bytes. url must be an absolute HTTPS URL; callers
// should have normalized "//" protocol-relative URLs beforehand.
//
// The SRT uses Bilibili's canonical formatting: 1-based cue index, HH:MM:SS,ms
// timestamps separated by " --> ", the content on the next line, and a blank
// line between cues. Trailing newline is included so that appending is safe.
func (c *Client) FetchSubtitle(ctx context.Context, url string) ([]byte, error) {
	body, err := c.doRaw(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("api: fetch subtitle: %w", err)
	}
	return BCCToSRT(body)
}

// BCCToSRT converts a BCC JSON document to SRT. Exposed separately so tests
// can feed fixtures without spinning up an HTTP server.
func BCCToSRT(bcc []byte) ([]byte, error) {
	var doc bccDoc
	if err := json.Unmarshal(bcc, &doc); err != nil {
		return nil, fmt.Errorf("api: decode bcc: %w", err)
	}
	var b strings.Builder
	for i, cue := range doc.Body {
		fmt.Fprintf(&b, "%d\n", i+1)
		fmt.Fprintf(&b, "%s --> %s\n", formatSRTTime(cue.From), formatSRTTime(cue.To))
		b.WriteString(cue.Content)
		b.WriteString("\n\n")
	}
	return []byte(b.String()), nil
}

// formatSRTTime renders sec as "HH:MM:SS,mmm". Negative values are clamped
// to zero; milliseconds are rounded to the nearest integer.
func formatSRTTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	total := int64(sec*1000 + 0.5) // round to nearest ms
	ms := total % 1000
	total /= 1000
	s := total % 60
	total /= 60
	m := total % 60
	h := total / 60
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}
