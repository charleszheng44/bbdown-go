package mux

import (
	"context"
	"errors"
	"testing"
)

func TestEnsureFFmpeg(t *testing.T) {
	err := EnsureFFmpeg(context.Background())
	if err != nil {
		if errors.Is(err, ErrFFmpegMissing) {
			t.Skip("ffmpeg not installed; skipping")
		}
		t.Fatalf("EnsureFFmpeg: unexpected error: %v", err)
	}
}

func TestEnsureFFmpegCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := EnsureFFmpeg(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureFFmpeg with canceled ctx: got %v, want context.Canceled", err)
	}
}

func TestCombineValidation(t *testing.T) {
	ctx := context.Background()
	if err := Combine(ctx, Inputs{}, "out.mp4"); err == nil {
		t.Fatal("Combine with empty Video should fail")
	}
	if err := Combine(ctx, Inputs{Video: "v.m4v"}, ""); err == nil {
		t.Fatal("Combine with empty dst should fail")
	}
}
