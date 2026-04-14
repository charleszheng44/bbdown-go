// Package planner selects concrete video + audio DASH streams from a
// PlayInfo payload according to user preferences.
//
// The planner is deliberately pure: it performs no I/O and returns a
// Selection value that the download stage consumes. Callers supply a Prefs
// struct describing the desired quality label, codec preference order, and
// whether the request is video-only or audio-only.
package planner

import (
	"errors"
	"strconv"
	"strings"

	"github.com/charleszheng44/bbdown-go/internal/api"
)

// Sentinel errors returned by Pick.
var (
	// ErrNoVideoStream is returned when a video track is required but the
	// PlayInfo contains no video streams.
	ErrNoVideoStream = errors.New("planner: no video streams available")
	// ErrNoAudioStream is returned when an audio track is required but the
	// PlayInfo contains no audio streams.
	ErrNoAudioStream = errors.New("planner: no audio streams available")
)

// Prefs captures the user-facing selection knobs.
type Prefs struct {
	// Quality is a human-visible label such as "1080P 60" or "720P". When
	// empty the highest available quality for the chosen codec is picked.
	Quality string
	// CodecOrder lists codec families in descending preference, e.g.
	// []string{"hevc", "avc"}. Empty means no codec preference — the first
	// stream with any codec is eligible.
	CodecOrder []string
	// VideoOnly skips audio-stream selection.
	VideoOnly bool
	// AudioOnly skips video-stream selection.
	AudioOnly bool
	// Interactive is reserved for a future interactive picker. The planner
	// itself is non-interactive and ignores this flag; it exists so callers
	// can thread the user preference through without a second struct.
	Interactive bool
}

// Selection is the planner's output: the concrete streams chosen for the
// requested preferences plus convenience labels echoing what was picked.
type Selection struct {
	// Video is the chosen video stream, or nil when AudioOnly is set or the
	// PlayInfo contained no video streams and AudioOnly was set.
	Video *api.Stream
	// Audio is the chosen audio stream, or nil when VideoOnly is set.
	Audio *api.Stream
	// Quality is the Video stream's quality label, or empty when Video is
	// nil.
	Quality string
	// Codec is the video stream's codec family ("avc", "hevc", "av1"), or
	// empty when Video is nil.
	Codec string
}

// Pick chooses the best matching video + audio streams from info given
// prefs. It returns ErrNoVideoStream or ErrNoAudioStream when a required
// track is missing; codec/quality mismatches never error — they fall back
// to the next-lower (then next-higher) option per the documented policy.
func Pick(info api.PlayInfo, prefs Prefs) (Selection, error) {
	var sel Selection

	if !prefs.AudioOnly {
		if len(info.Videos) == 0 {
			return Selection{}, ErrNoVideoStream
		}
		v := pickVideo(info.Videos, prefs)
		if v == nil {
			// Should not happen: with a non-empty Videos slice pickVideo
			// always returns something (it falls back across codecs and
			// quality rungs). Guard anyway for future edits.
			return Selection{}, ErrNoVideoStream
		}
		sel.Video = v
		sel.Quality = v.Quality
		sel.Codec = codecFamily(v.Codecs)
	}

	if !prefs.VideoOnly {
		if len(info.Audios) == 0 {
			return Selection{}, ErrNoAudioStream
		}
		sel.Audio = pickAudio(info.Audios)
	}

	return sel, nil
}

// pickVideo walks the codec preference list and returns the first codec's
// best quality match. With an empty CodecOrder every stream is eligible.
func pickVideo(videos []api.Stream, prefs Prefs) *api.Stream {
	order := prefs.CodecOrder
	if len(order) == 0 {
		// Synthetic single pass: "any codec".
		return pickByQuality(videos, prefs.Quality)
	}
	for _, codec := range order {
		codec = strings.ToLower(codec)
		var subset []api.Stream
		for _, s := range videos {
			if codecFamily(s.Codecs) == codec {
				subset = append(subset, s)
			}
		}
		if len(subset) == 0 {
			continue
		}
		if v := pickByQuality(subset, prefs.Quality); v != nil {
			return v
		}
	}
	// Fallback: none of the preferred codecs had any streams. Pick from the
	// full list so the caller still gets something rather than nil.
	return pickByQuality(videos, prefs.Quality)
}

// pickByQuality returns the stream whose quality exactly matches want; if
// absent, the highest stream strictly below want; if none below, the lowest
// stream strictly above want. With an empty want the highest-quality
// stream is returned.
func pickByQuality(streams []api.Stream, want string) *api.Stream {
	if len(streams) == 0 {
		return nil
	}

	// Empty preference: highest-quality stream.
	if want == "" {
		best := 0
		for i := 1; i < len(streams); i++ {
			if qualityRank(streams[i].Quality) > qualityRank(streams[best].Quality) {
				best = i
			}
		}
		return &streams[best]
	}

	target := qualityRank(want)

	var (
		exactIdx = -1
		// bestLowerIdx: highest rank strictly below target.
		bestLowerIdx = -1
		// bestHigherIdx: lowest rank strictly above target.
		bestHigherIdx = -1
	)

	for i, s := range streams {
		r := qualityRank(s.Quality)
		switch {
		case r == target:
			if exactIdx == -1 {
				exactIdx = i
			}
		case r < target:
			if bestLowerIdx == -1 || r > qualityRank(streams[bestLowerIdx].Quality) {
				bestLowerIdx = i
			}
		case r > target:
			if bestHigherIdx == -1 || r < qualityRank(streams[bestHigherIdx].Quality) {
				bestHigherIdx = i
			}
		}
	}

	switch {
	case exactIdx != -1:
		return &streams[exactIdx]
	case bestLowerIdx != -1:
		return &streams[bestLowerIdx]
	case bestHigherIdx != -1:
		return &streams[bestHigherIdx]
	default:
		return nil
	}
}

// pickAudio returns the audio stream with the highest bandwidth.
func pickAudio(audios []api.Stream) *api.Stream {
	best := 0
	for i := 1; i < len(audios); i++ {
		if audios[i].Bandwidth > audios[best].Bandwidth {
			best = i
		}
	}
	return &audios[best]
}

// codecFamily maps a DASH codec string to one of the canonical family
// names used by Prefs.CodecOrder. Unknown or empty input yields "".
//
// The mapping reflects the codec identifiers Bilibili actually emits in
// the DASH payload: avc1.* / avc3.* for H.264, hev1.* / hvc1.* for HEVC,
// and av01.* for AV1.
func codecFamily(codecs string) string {
	s := strings.ToLower(strings.TrimSpace(codecs))
	switch {
	case strings.HasPrefix(s, "avc1"), strings.HasPrefix(s, "avc3"), s == "avc":
		return "avc"
	case strings.HasPrefix(s, "hev1"), strings.HasPrefix(s, "hvc1"), s == "hevc":
		return "hevc"
	case strings.HasPrefix(s, "av01"), s == "av1":
		return "av1"
	default:
		return ""
	}
}

// qualityRank converts a human-visible quality label to a comparable int.
// "1080P 60" -> 1080*1000 + 60. "720P" -> 720*1000. Unknown labels rank 0
// so they sort below every real quality.
//
// Splitting on the first space keeps the parser forgiving of future labels
// like "1080P 高码率" (rank degrades to 1080*1000, ignoring the suffix).
func qualityRank(label string) int {
	label = strings.TrimSpace(label)
	if label == "" {
		return 0
	}

	// Split off a trailing " 60"-style frame-rate hint.
	var resPart, fpsPart string
	if idx := strings.IndexByte(label, ' '); idx >= 0 {
		resPart = label[:idx]
		fpsPart = strings.TrimSpace(label[idx+1:])
	} else {
		resPart = label
	}

	res := parseResolution(resPart)
	fps, _ := strconv.Atoi(fpsPart) // best-effort; garbage → 0

	return res*1000 + fps
}

// parseResolution extracts the numeric resolution from labels such as
// "1080P", "720p", or "4K". Unrecognized input returns 0.
func parseResolution(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	upper := strings.ToUpper(s)

	// Handle "4K" / "8K" style labels by convention: 4K ≈ 2160, 8K ≈ 4320.
	if strings.HasSuffix(upper, "K") {
		if n, err := strconv.Atoi(strings.TrimSuffix(upper, "K")); err == nil {
			switch n {
			case 4:
				return 2160
			case 8:
				return 4320
			default:
				return n * 540 // rough scaling; keeps ordering monotone
			}
		}
	}

	// "1080P" / "720p": strip a single trailing P.
	trimmed := strings.TrimRight(upper, "P")
	if n, err := strconv.Atoi(trimmed); err == nil {
		return n
	}
	return 0
}
