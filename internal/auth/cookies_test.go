package auth

import (
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
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
	if !reflect.DeepEqual(got, want) {
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
			name: "includes buvid3",
			in:   "SESSDATA=a; bili_jct=b; DedeUserID=c; DedeUserID__ckMd5=d; buvid3=bv3",
			want: Cookies{SESSDATA: "a", BiliJCT: "b", DedeUserID: "c", DedeUserIDCkMd5: "d", Buvid3: "bv3"},
		},
		{
			name: "whitespace and unknown fields preserved in extras",
			in:   "  SESSDATA=a ;  bili_jct=b ; buvid4=bv4 ; DedeUserID=c;DedeUserID__ckMd5=d  ",
			want: Cookies{
				SESSDATA: "a", BiliJCT: "b", DedeUserID: "c", DedeUserIDCkMd5: "d",
				Extras: map[string]string{"buvid4": "bv4"},
			},
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
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("want %+v, got %+v", tc.want, got)
			}
		})
	}
}

// TestAsJarEncodesSESSDATACommas guards the one behaviour Bilibili's
// server is strict about: SESSDATA's comma separators must travel on
// the wire as the literal three-character sequence "%2C". A raw comma
// in the Cookie header makes the server truncate or reject the value,
// causing pgc/pugv playurl to return preview-only responses for
// purchased content. The encoding is applied at send time by AsJar, so
// existing cookies.json files (which store raw commas as extracted
// from the passport redirect URL) keep working without re-login.
func TestAsJarEncodesSESSDATACommas(t *testing.T) {
	t.Parallel()

	raw := "b41a5c15,1791866058,ce3a6*41"
	c := Cookies{SESSDATA: raw, BiliJCT: "j", DedeUserID: "1", DedeUserIDCkMd5: "m"}
	jar := c.AsJar()
	u, _ := url.Parse("https://api.bilibili.com/x/web-interface/nav")

	var sess string
	for _, ck := range jar.Cookies(u) {
		if ck.Name == "SESSDATA" {
			sess = ck.Value
			break
		}
	}
	want := "b41a5c15%2C1791866058%2Cce3a6*41"
	if sess != want {
		t.Fatalf("SESSDATA on wire = %q, want %q", sess, want)
	}
}

// TestAsJarSESSDATAEncodingIsIdempotent verifies that a caller supplying
// an already-encoded SESSDATA (e.g. pasted from DevTools "Request
// Headers → cookie:") is not double-encoded. %25 on the wire would be
// treated as a literal %, breaking auth in the same way a raw comma
// does.
func TestAsJarSESSDATAEncodingIsIdempotent(t *testing.T) {
	t.Parallel()

	encoded := "b41a5c15%2C1791866058%2Cce3a6*41"
	c := Cookies{SESSDATA: encoded, BiliJCT: "j", DedeUserID: "1", DedeUserIDCkMd5: "m"}
	jar := c.AsJar()
	u, _ := url.Parse("https://api.bilibili.com/x/web-interface/nav")

	var sess string
	for _, ck := range jar.Cookies(u) {
		if ck.Name == "SESSDATA" {
			sess = ck.Value
			break
		}
	}
	if sess != encoded {
		t.Fatalf("SESSDATA was re-encoded; got %q, want %q (unchanged)", sess, encoded)
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
	if _, ok := got["buvid3"]; ok {
		t.Errorf("buvid3 should be absent when Buvid3 is empty, got %q", got["buvid3"])
	}
}

func TestAsJarIncludesExtras(t *testing.T) {
	t.Parallel()

	c := Cookies{
		SESSDATA: "s", BiliJCT: "j", DedeUserID: "1", DedeUserIDCkMd5: "m",
		Extras: map[string]string{"buvid4": "bv4-val", "bili_ticket": "tkt", "SESSDATA": "ignored"},
	}
	jar := c.AsJar()
	u, _ := url.Parse("https://api.bilibili.com/x/web-interface/nav")

	got := map[string]string{}
	for _, ck := range jar.Cookies(u) {
		got[ck.Name] = ck.Value
	}
	if got["buvid4"] != "bv4-val" {
		t.Errorf("buvid4 = %q, want bv4-val", got["buvid4"])
	}
	if got["bili_ticket"] != "tkt" {
		t.Errorf("bili_ticket = %q, want tkt", got["bili_ticket"])
	}
	// Reserved-name collisions must not overwrite the real SESSDATA.
	if got["SESSDATA"] != "s" {
		t.Errorf("SESSDATA = %q, want s (extras must not shadow named fields)", got["SESSDATA"])
	}
}

func TestAsJarIncludesBuvid3WhenSet(t *testing.T) {
	t.Parallel()

	c := Cookies{SESSDATA: "s", BiliJCT: "j", DedeUserID: "1", DedeUserIDCkMd5: "m", Buvid3: "bv3-val"}
	jar := c.AsJar()
	u, _ := url.Parse("https://api.bilibili.com/x/web-interface/nav")

	for _, ck := range jar.Cookies(u) {
		if ck.Name == "buvid3" {
			if ck.Value != "bv3-val" {
				t.Errorf("buvid3 value = %q, want %q", ck.Value, "bv3-val")
			}
			return
		}
	}
	t.Errorf("buvid3 cookie not found in jar")
}

func TestStoreLoadRoundTripWithBuvid3(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	want := Cookies{
		SESSDATA:        "s",
		BiliJCT:         "j",
		DedeUserID:      "1",
		DedeUserIDCkMd5: "m",
		Buvid3:          "bv3",
	}
	if err := Store(path, want); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip mismatch: want %+v, got %+v", want, got)
	}
}
