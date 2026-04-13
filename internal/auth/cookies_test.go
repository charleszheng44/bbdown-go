package auth

import (
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoreLoadRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")

	want := Cookies{
		SESSDATA:        "sess-value",
		BiliJCT:         "jct-value",
		DedeUserID:      "12345",
		DedeUserIDCkMd5: "abcdef",
	}

	if err := Store(path, want); err != nil {
		t.Fatalf("Store: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Fatalf("round-trip mismatch: want %+v, got %+v", want, got)
	}
}

func TestStoreMode0600(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode is not meaningful on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")

	if err := Store(path, Cookies{SESSDATA: "x"}); err != nil {
		t.Fatalf("Store: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != fs.FileMode(0o600) {
		t.Fatalf("want mode 0600, got %#o", perm)
	}
}

func TestLoadMissingReturnsNotLoggedIn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	_, err := Load(path)
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("want ErrNotLoggedIn, got %v", err)
	}
}

func TestLoadMalformedJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatalf("expected error loading malformed cookies")
	}
}

func TestParseCookieString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		want    Cookies
		wantErr bool
	}{
		{
			name: "all fields",
			in:   "SESSDATA=a; bili_jct=b; DedeUserID=c; DedeUserID__ckMd5=d",
			want: Cookies{SESSDATA: "a", BiliJCT: "b", DedeUserID: "c", DedeUserIDCkMd5: "d"},
		},
		{
			name: "extra whitespace and unknown fields",
			in:   "  SESSDATA=a ;  bili_jct=b ; ignored=x ; DedeUserID=c;DedeUserID__ckMd5=d  ",
			want: Cookies{SESSDATA: "a", BiliJCT: "b", DedeUserID: "c", DedeUserIDCkMd5: "d"},
		},
		{
			name: "partial",
			in:   "SESSDATA=only",
			want: Cookies{SESSDATA: "only"},
		},
		{
			name: "empty string",
			in:   "",
			want: Cookies{},
		},
		{
			name:    "malformed entry without equals",
			in:      "SESSDATA=a; garbage; bili_jct=b",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCookieString(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %+v, got %+v", tc.want, got)
			}
		})
	}
}

func TestAsJarPopulatesAPICookies(t *testing.T) {
	t.Parallel()

	c := Cookies{
		SESSDATA:        "sess",
		BiliJCT:         "jct",
		DedeUserID:      "42",
		DedeUserIDCkMd5: "md5",
	}
	jar := c.AsJar()
	u, _ := url.Parse("https://api.bilibili.com/x/web-interface/nav")

	got := map[string]string{}
	for _, ck := range jar.Cookies(u) {
		got[ck.Name] = ck.Value
	}
	want := map[string]string{
		"SESSDATA":          "sess",
		"bili_jct":          "jct",
		"DedeUserID":        "42",
		"DedeUserID__ckMd5": "md5",
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("cookie %q: want %q, got %q", k, v, got[k])
		}
	}
}
