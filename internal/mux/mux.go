// Package mux wraps ffmpeg to combine a video stream with optional audio and
// subtitle streams into a single MP4 using stream copy.
//
// It is a thin, Bilibili-agnostic helper: callers hand it absolute paths to
// the segments already on disk and a destination path, and it shells out to
// ffmpeg with a deterministic argv.
package mux

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// ErrFFmpegMissing is returned when the ffmpeg binary cannot be located on
// PATH. cmd/bbdown translates it into an install-instruction message.
var ErrFFmpegMissing = errors.New("ffmpeg not found on PATH")

// Inputs describes the source files to mux together. Video is required;
// Audio and Subtitle are optional. Paths are passed to ffmpeg verbatim.
type Inputs struct {
	Video    string // path to video track (e.g. .m4v); required
	Audio    string // path to audio track (e.g. .m4a); optional
	Subtitle string // path to subtitle file (e.g. .srt); optional
}

// EnsureFFmpeg verifies that the ffmpeg executable is reachable on PATH.
// It returns ErrFFmpegMissing if the binary is not found; any other lookup
// error is returned as-is.
//
// The context is accepted for API symmetry with Combine so callers can keep
// a consistent signature; LookPath itself does not perform I/O that honors
// cancellation.
func EnsureFFmpeg(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrFFmpegMissing
		}
		return err
	}
	return nil
}

// Combine invokes ffmpeg to mux in into an MP4 written to dst. All inputs
// are copied without re-encoding (-c copy); if Subtitle is set, the subtitle
// stream is converted to mov_text so it can live inside an MP4 container.
//
// Combine requires ffmpeg on PATH and returns ErrFFmpegMissing otherwise.
// Any non-zero ffmpeg exit is returned wrapped with its combined output to
// aid debugging.
func Combine(ctx context.Context, in Inputs, dst string) error {
	if in.Video == "" {
		return errors.New("mux: Inputs.Video is required")
	}
	if dst == "" {
		return errors.New("mux: dst is required")
	}
	if err := EnsureFFmpeg(ctx); err != nil {
		return err
	}

	args := buildArgv(in, dst)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mux: ffmpeg failed: %w: %s", err, string(out))
	}
	return nil
}

// buildArgv constructs the ffmpeg argument vector for the given inputs.
// The layout is:
//
//	-y -i <video> [-i <audio>] [-i <subtitle>]
//	-c copy [-c:s mov_text]
//	-map 0:v [-map 1:a] [-map 2:s]
//	<dst>
//
// Audio is always input index 1 when present; subtitle takes index 2 when
// audio is also present, otherwise index 1. -c:s mov_text is only emitted
// when a subtitle is supplied.
func buildArgv(in Inputs, dst string) []string {
	args := []string{"-y", "-i", in.Video}

	audioIdx := -1
	subIdx := -1
	next := 1
	if in.Audio != "" {
		args = append(args, "-i", in.Audio)
		audioIdx = next
		next++
	}
	if in.Subtitle != "" {
		args = append(args, "-i", in.Subtitle)
		subIdx = next
		next++
	}

	args = append(args, "-c", "copy")
	if in.Subtitle != "" {
		args = append(args, "-c:s", "mov_text")
	}

	args = append(args, "-map", "0:v")
	if audioIdx >= 0 {
		args = append(args, "-map", fmt.Sprintf("%d:a", audioIdx))
	}
	if subIdx >= 0 {
		args = append(args, "-map", fmt.Sprintf("%d:s", subIdx))
	}

	args = append(args, dst)
	return args
}
