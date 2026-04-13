package mux

import (
	"reflect"
	"testing"
)

func TestBuildArgv(t *testing.T) {
	tests := []struct {
		name string
		in   Inputs
		dst  string
		want []string
	}{
		{
			name: "video only",
			in:   Inputs{Video: "v.m4v"},
			dst:  "out.mp4",
			want: []string{
				"-y",
				"-i", "v.m4v",
				"-c", "copy",
				"-map", "0:v",
				"out.mp4",
			},
		},
		{
			name: "video and audio",
			in:   Inputs{Video: "v.m4v", Audio: "a.m4a"},
			dst:  "out.mp4",
			want: []string{
				"-y",
				"-i", "v.m4v",
				"-i", "a.m4a",
				"-c", "copy",
				"-map", "0:v",
				"-map", "1:a",
				"out.mp4",
			},
		},
		{
			name: "video audio and subtitle",
			in:   Inputs{Video: "v.m4v", Audio: "a.m4a", Subtitle: "s.srt"},
			dst:  "out.mp4",
			want: []string{
				"-y",
				"-i", "v.m4v",
				"-i", "a.m4a",
				"-i", "s.srt",
				"-c", "copy",
				"-c:s", "mov_text",
				"-map", "0:v",
				"-map", "1:a",
				"-map", "2:s",
				"out.mp4",
			},
		},
		{
			name: "video and subtitle (no audio)",
			in:   Inputs{Video: "v.m4v", Subtitle: "s.srt"},
			dst:  "out.mp4",
			want: []string{
				"-y",
				"-i", "v.m4v",
				"-i", "s.srt",
				"-c", "copy",
				"-c:s", "mov_text",
				"-map", "0:v",
				"-map", "1:s",
				"out.mp4",
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildArgv(tc.in, tc.dst)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("buildArgv mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}
