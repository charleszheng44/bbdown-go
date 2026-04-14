package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBCCToSRT_Fixture(t *testing.T) {
	path := filepath.Join("testdata", "bcc.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := BCCToSRT(raw)
	if err != nil {
		t.Fatalf("BCCToSRT: %v", err)
	}

	wantLines := []string{
		"1",
		"00:00:00,000 --> 00:00:02,500",
		"Hello world",
		"",
		"2",
		"00:00:02,500 --> 00:00:05,123",
		"第二行字幕",
		"",
		"3",
		"00:00:05,123 --> 00:00:07,000",
		"Third line",
		"",
		"",
	}
	want := strings.Join(wantLines, "\n")
	if string(got) != want {
		t.Errorf("SRT mismatch.\n got: %q\nwant: %q", string(got), want)
	}
}

func TestBCCToSRT_MissingFromDefaultsToZero(t *testing.T) {
	// Absent "from" fields decode to zero (Go's JSON default) and must
	// render as 00:00:00,000 without crashing.
	doc := []byte(`{"body":[{"to":1.0,"content":"only to"}]}`)
	got, err := BCCToSRT(doc)
	if err != nil {
		t.Fatalf("BCCToSRT: %v", err)
	}
	if !strings.Contains(string(got), "00:00:00,000 --> 00:00:01,000") {
		t.Errorf("expected 0→1s cue, got %q", string(got))
	}
}

func TestFormatSRTTime(t *testing.T) {
	cases := []struct {
		sec  float64
		want string
	}{
		{0, "00:00:00,000"},
		{0.001, "00:00:00,001"},
		{1.5, "00:00:01,500"},
		{61.25, "00:01:01,250"},
		{3723.999, "01:02:03,999"},
		{-5, "00:00:00,000"},
	}
	for _, c := range cases {
		got := formatSRTTime(c.sec)
		if got != c.want {
			t.Errorf("formatSRTTime(%v) = %q, want %q", c.sec, got, c.want)
		}
	}
}
