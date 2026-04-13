package parser

import (
	"errors"
	"testing"
)

func TestClassify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    Target
		wantErr error
	}{
		{
			name:  "bare BV uppercase",
			input: "BV1xx411c7mD",
			want:  Target{Kind: KindRegular, BVID: "BV1xx411c7mD", Raw: "BV1xx411c7mD"},
		},
		{
			name:  "bare bv lowercase prefix preserves body",
			input: "bv1xx411c7mD",
			want:  Target{Kind: KindRegular, BVID: "BV1xx411c7mD", Raw: "bv1xx411c7mD"},
		},
		{
			name:  "bare av",
			input: "av170001",
			want:  Target{Kind: KindRegular, AID: "170001", Raw: "av170001"},
		},
		{
			name:  "bare ep",
			input: "ep12345",
			want:  Target{Kind: KindBangumi, EPID: "12345", Raw: "ep12345"},
		},
		{
			name:  "bare ss",
			input: "ss1234",
			want:  Target{Kind: KindBangumi, SSID: "1234", Raw: "ss1234"},
		},
		{
			name:  "bare EP uppercase",
			input: "EP42",
			want:  Target{Kind: KindBangumi, EPID: "42", Raw: "EP42"},
		},
		{
			name:  "regular video URL with query",
			input: "https://www.bilibili.com/video/BV1xx411c7mD/?p=2&spm_id_from=333",
			want:  Target{Kind: KindRegular, BVID: "BV1xx411c7mD", Raw: "https://www.bilibili.com/video/BV1xx411c7mD/?p=2&spm_id_from=333"},
		},
		{
			name:  "regular video URL av form",
			input: "https://www.bilibili.com/video/av170001",
			want:  Target{Kind: KindRegular, AID: "170001", Raw: "https://www.bilibili.com/video/av170001"},
		},
		{
			name:  "bangumi ep URL",
			input: "https://www.bilibili.com/bangumi/play/ep12345",
			want:  Target{Kind: KindBangumi, EPID: "12345", Raw: "https://www.bilibili.com/bangumi/play/ep12345"},
		},
		{
			name:  "bangumi ss URL trailing slash",
			input: "https://www.bilibili.com/bangumi/play/ss1234/",
			want:  Target{Kind: KindBangumi, SSID: "1234", Raw: "https://www.bilibili.com/bangumi/play/ss1234/"},
		},
		{
			name:  "course ep URL",
			input: "https://www.bilibili.com/cheese/play/ep12345",
			want:  Target{Kind: KindCourse, EPID: "12345", Raw: "https://www.bilibili.com/cheese/play/ep12345"},
		},
		{
			name:  "course ss URL",
			input: "https://www.bilibili.com/cheese/play/ss777",
			want:  Target{Kind: KindCourse, SSID: "777", Raw: "https://www.bilibili.com/cheese/play/ss777"},
		},
		{
			name:  "leading and trailing whitespace is trimmed",
			input: "   BV1xx411c7mD\n",
			want:  Target{Kind: KindRegular, BVID: "BV1xx411c7mD", Raw: "BV1xx411c7mD"},
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: ErrEmptyInput,
		},
		{
			name:    "whitespace-only input",
			input:   "   \t\n",
			wantErr: ErrEmptyInput,
		},
		{
			name:    "b23.tv short link rejected",
			input:   "https://b23.tv/abc123",
			wantErr: ErrShortLinkUnsupported,
		},
		{
			name:    "b23.tv subdomain rejected",
			input:   "https://m.b23.tv/abc",
			wantErr: ErrShortLinkUnsupported,
		},
		{
			name:    "garbage string",
			input:   "not-a-bilibili-url",
			wantErr: ErrUnknownFormat,
		},
		{
			name:    "bilibili homepage without known path",
			input:   "https://www.bilibili.com/",
			wantErr: ErrUnknownFormat,
		},
		{
			name:    "av with non-digits",
			input:   "avXYZ",
			wantErr: ErrUnknownFormat,
		},
		{
			name:    "ep with trailing junk is not a bare ID",
			input:   "ep12345abc",
			wantErr: ErrUnknownFormat,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Classify(tc.input)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Classify(%q) error = %v, want %v", tc.input, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Classify(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("Classify(%q) = %+v, want %+v", tc.input, got, tc.want)
			}
		})
	}
}

func TestKindConstants(t *testing.T) {
	// Lock the iota values so downstream packages and serialized forms do
	// not silently drift if someone reorders the const block.
	if KindRegular != 0 {
		t.Errorf("KindRegular = %d, want 0", KindRegular)
	}
	if KindBangumi != 1 {
		t.Errorf("KindBangumi = %d, want 1", KindBangumi)
	}
	if KindCourse != 2 {
		t.Errorf("KindCourse = %d, want 2", KindCourse)
	}
}
