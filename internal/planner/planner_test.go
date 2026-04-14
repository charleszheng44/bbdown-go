package planner

import (
	"errors"
	"testing"

	"github.com/charleszheng44/bbdown-go/internal/api"
)

// videoStreams returns a canonical fixture covering three qualities per
// codec family. Tests opt-in to subsets by filtering this slice.
func videoStreams() []api.Stream {
	return []api.Stream{
		{ID: 116, Bandwidth: 6_000_000, Codecs: "avc1.640032", Quality: "1080P 60"},
		{ID: 80, Bandwidth: 3_000_000, Codecs: "avc1.640028", Quality: "1080P"},
		{ID: 64, Bandwidth: 1_500_000, Codecs: "avc1.64001F", Quality: "720P"},
		{ID: 116, Bandwidth: 5_000_000, Codecs: "hev1.1.6.L150.90", Quality: "1080P 60"},
		{ID: 80, Bandwidth: 2_500_000, Codecs: "hev1.1.6.L120.90", Quality: "1080P"},
		{ID: 64, Bandwidth: 1_200_000, Codecs: "hev1.1.6.L93.90", Quality: "720P"},
	}
}

func audioStreams() []api.Stream {
	return []api.Stream{
		{ID: 30216, Bandwidth: 64_000, Codecs: "mp4a.40.2"},
		{ID: 30232, Bandwidth: 128_000, Codecs: "mp4a.40.2"},
		{ID: 30280, Bandwidth: 192_000, Codecs: "mp4a.40.2"},
	}
}

// filter returns every stream for which keep returns true. Small helper to
// keep fixture munging in tests tidy.
func filter(in []api.Stream, keep func(api.Stream) bool) []api.Stream {
	out := make([]api.Stream, 0, len(in))
	for _, s := range in {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}

func TestPick_CodecFallback_HEVCPreferredButOnlyAVC(t *testing.T) {
	t.Parallel()

	info := api.PlayInfo{
		Videos: filter(videoStreams(), func(s api.Stream) bool {
			return codecFamily(s.Codecs) == "avc"
		}),
		Audios: audioStreams(),
	}

	sel, err := Pick(info, Prefs{
		Quality:    "1080P 60",
		CodecOrder: []string{"hevc", "avc"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Codec != "avc" {
		t.Fatalf("codec: got %q want %q", sel.Codec, "avc")
	}
	if sel.Quality != "1080P 60" {
		t.Fatalf("quality: got %q want %q", sel.Quality, "1080P 60")
	}
}

func TestPick_ExactQualityMatch(t *testing.T) {
	t.Parallel()

	info := api.PlayInfo{Videos: videoStreams(), Audios: audioStreams()}

	sel, err := Pick(info, Prefs{
		Quality:    "720P",
		CodecOrder: []string{"hevc", "avc"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Video == nil {
		t.Fatal("expected a video stream, got nil")
	}
	if sel.Quality != "720P" {
		t.Fatalf("quality: got %q want %q", sel.Quality, "720P")
	}
	if sel.Codec != "hevc" {
		t.Fatalf("codec: got %q want %q (hevc preferred, exact match exists)", sel.Codec, "hevc")
	}
}

func TestPick_QualityFallback_PrefersNextLower(t *testing.T) {
	t.Parallel()

	// Strip every "1080P 60" entry so the planner must fall back from
	// "1080P 60" to "1080P" (next-lower rung).
	videos := filter(videoStreams(), func(s api.Stream) bool {
		return s.Quality != "1080P 60"
	})
	info := api.PlayInfo{Videos: videos, Audios: audioStreams()}

	sel, err := Pick(info, Prefs{
		Quality:    "1080P 60",
		CodecOrder: []string{"avc"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Quality != "1080P" {
		t.Fatalf("quality: got %q want %q", sel.Quality, "1080P")
	}
	if sel.Codec != "avc" {
		t.Fatalf("codec: got %q want %q", sel.Codec, "avc")
	}
}

func TestPick_QualityFallback_PrefersNextHigherWhenNothingLower(t *testing.T) {
	t.Parallel()

	// Only 720P and above; ask for 360P → next-higher is 720P.
	videos := filter(videoStreams(), func(s api.Stream) bool {
		return codecFamily(s.Codecs) == "avc" &&
			(s.Quality == "720P" || s.Quality == "1080P" || s.Quality == "1080P 60")
	})
	info := api.PlayInfo{Videos: videos, Audios: audioStreams()}

	sel, err := Pick(info, Prefs{
		Quality:    "360P",
		CodecOrder: []string{"avc"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Quality != "720P" {
		t.Fatalf("quality: got %q want %q", sel.Quality, "720P")
	}
}

func TestPick_AudioOnly_SkipsVideoWithoutError(t *testing.T) {
	t.Parallel()

	// Deliberately omit video streams — AudioOnly must not error on them.
	info := api.PlayInfo{Audios: audioStreams()}

	sel, err := Pick(info, Prefs{AudioOnly: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Video != nil {
		t.Fatalf("video: got %+v want nil", sel.Video)
	}
	if sel.Audio == nil {
		t.Fatal("audio: got nil want highest-bandwidth stream")
	}
	if sel.Audio.Bandwidth != 192_000 {
		t.Fatalf("audio bandwidth: got %d want %d (highest)", sel.Audio.Bandwidth, 192_000)
	}
}

func TestPick_VideoOnly_SkipsAudioWithoutError(t *testing.T) {
	t.Parallel()

	// Deliberately omit audio streams — VideoOnly must not error on them.
	info := api.PlayInfo{Videos: videoStreams()}

	sel, err := Pick(info, Prefs{
		VideoOnly:  true,
		Quality:    "1080P 60",
		CodecOrder: []string{"hevc", "avc"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Audio != nil {
		t.Fatalf("audio: got %+v want nil", sel.Audio)
	}
	if sel.Video == nil {
		t.Fatal("video: got nil want stream")
	}
	if sel.Codec != "hevc" {
		t.Fatalf("codec: got %q want %q", sel.Codec, "hevc")
	}
}

func TestPick_NoVideoStream_ReturnsSentinel(t *testing.T) {
	t.Parallel()

	info := api.PlayInfo{Audios: audioStreams()}

	_, err := Pick(info, Prefs{})
	if !errors.Is(err, ErrNoVideoStream) {
		t.Fatalf("error: got %v want ErrNoVideoStream", err)
	}
}

func TestPick_NoAudioStream_ReturnsSentinel(t *testing.T) {
	t.Parallel()

	info := api.PlayInfo{Videos: videoStreams()}

	_, err := Pick(info, Prefs{CodecOrder: []string{"avc"}})
	if !errors.Is(err, ErrNoAudioStream) {
		t.Fatalf("error: got %v want ErrNoAudioStream", err)
	}
}

func TestPick_HighestBandwidthAudio(t *testing.T) {
	t.Parallel()

	info := api.PlayInfo{Videos: videoStreams(), Audios: audioStreams()}

	sel, err := Pick(info, Prefs{CodecOrder: []string{"avc"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Audio == nil || sel.Audio.Bandwidth != 192_000 {
		t.Fatalf("audio: got %+v want bandwidth=%d", sel.Audio, 192_000)
	}
}

func TestCodecFamily(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
	}{
		{"avc1.640032", "avc"},
		{"AVC3.640032", "avc"},
		{"hev1.1.6.L150.90", "hevc"},
		{"hvc1.2.4.L120.90", "hevc"},
		{"av01.0.08M.08", "av1"},
		{"mp4a.40.2", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := codecFamily(tc.in); got != tc.want {
			t.Errorf("codecFamily(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestQualityRank_OrderedSensibly(t *testing.T) {
	t.Parallel()

	// Sanity check: the ordering used by pickByQuality must rank the four
	// labels called out in the design spec correctly.
	order := []string{"360P", "720P", "1080P", "1080P 60"}
	for i := 1; i < len(order); i++ {
		prev, cur := qualityRank(order[i-1]), qualityRank(order[i])
		if !(cur > prev) {
			t.Fatalf("qualityRank(%q)=%d not > qualityRank(%q)=%d", order[i], cur, order[i-1], prev)
		}
	}
}
