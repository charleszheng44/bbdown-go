package api

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestDeriveMixinKey pins the mixin-table permutation. The expected output
// was computed by hand from the 64-char input:
//
//	"7cd084941338484aae1ad9425b84077c" + "4932caff0ff746eab6f01bf08b70ac45"
//
// applying mixinTable (46,47,18,2,...,41,13) index by index.
func TestDeriveMixinKey(t *testing.T) {
	const (
		imgKey = "7cd084941338484aae1ad9425b84077c"
		subKey = "4932caff0ff746eab6f01bf08b70ac45"
		want   = "ea1db124af3c7062474693fa704f4ff8"
	)
	got := deriveMixinKey(imgKey + subKey)
	if got != want {
		t.Errorf("deriveMixinKey = %q, want %q", got, want)
	}
	if len(got) != 32 {
		t.Errorf("mixin key length = %d, want 32", len(got))
	}
}

// TestFilenameNoExt covers the basename-stripping used to extract img_key
// and sub_key from their CDN URLs.
func TestFilenameNoExt(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png", "7cd084941338484aae1ad9425b84077c"},
		{"https://i0.hdslb.com/bfs/wbi/abc.jpg", "abc"},
		{"abc.jpg", "abc"},
		{"", ""},
	}
	for _, c := range cases {
		if got := filenameNoExt(c.in); got != c.want {
			t.Errorf("filenameNoExt(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSignWBI_StableWRID checks that signWBI:
//  1. adds wts=<unix seconds>,
//  2. sorts params lexicographically,
//  3. appends w_rid=md5(query+mixinKey).
//
// The expected w_rid is recomputed in-test using the documented algorithm
// (md5 of the sorted encoded query concatenated with the mixin key).
func TestSignWBI_StableWRID(t *testing.T) {
	params := map[string]string{
		"foo":  "bar",
		"aid":  "12345",
		"mid":  "987",
		"char": "a b&c", // exercises URL-encoding of spaces and '&'.
	}
	const mixinKey = "ea1db124af3c7062474693fa704f4ff8"
	fixed := time.Unix(1700000000, 0)

	got := signWBI(params, mixinKey, fixed)

	// Independently reconstruct the expected query and w_rid to verify the
	// algorithm end-to-end.
	merged := map[string]string{}
	for k, v := range params {
		merged[k] = v
	}
	merged["wts"] = "1700000000"
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, wbiEscape(k)+"="+wbiEscape(merged[k]))
	}
	query := strings.Join(parts, "&")
	sum := md5.Sum([]byte(query + mixinKey))
	expected := query + "&w_rid=" + hex.EncodeToString(sum[:])

	if got != expected {
		t.Errorf("signWBI mismatch.\n got: %s\nwant: %s", got, expected)
	}

	// Sanity: w_rid is a 32-char hex string.
	if !strings.Contains(got, "&w_rid=") {
		t.Fatalf("signed query has no w_rid: %s", got)
	}
	after := got[strings.LastIndex(got, "w_rid=")+len("w_rid="):]
	if len(after) != 32 {
		t.Errorf("w_rid length = %d, want 32", len(after))
	}
}

// TestMixinKeyCaching verifies that mixinKeyLocked fetches nav exactly once
// within the TTL window.
func TestMixinKeyCaching(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/web-interface/nav" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		hits.Add(1)
		resp := map[string]any{
			"code":    0,
			"message": "0",
			"data": map[string]any{
				"wbi_img": map[string]any{
					"img_url": "https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png",
					"sub_url": "https://i0.hdslb.com/bfs/wbi/4932caff0ff746eab6f01bf08b70ac45.png",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	withBase(t, apiBaseField, srv.URL, func() {
		c := NewClient(nil, "test-ua")
		ctx := context.Background()
		for i := 0; i < 3; i++ {
			k, err := c.mixinKeyLocked(ctx)
			if err != nil {
				t.Fatalf("mixinKeyLocked: %v", err)
			}
			if k != "ea1db124af3c7062474693fa704f4ff8" {
				t.Fatalf("unexpected mixin key %q", k)
			}
		}
		if got := hits.Load(); got != 1 {
			t.Errorf("nav hits = %d, want 1 (cache miss on subsequent calls)", got)
		}
	})
}

// TestMixinKeyCacheExpiry verifies that a stale cache triggers a re-fetch.
func TestMixinKeyCacheExpiry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"code":0,"data":{"wbi_img":{"img_url":"https://x/7cd084941338484aae1ad9425b84077c.png","sub_url":"https://x/4932caff0ff746eab6f01bf08b70ac45.png"}}}`)
	}))
	defer srv.Close()

	withBase(t, apiBaseField, srv.URL, func() {
		c := NewClient(nil, "")
		// Make time controllable.
		start := time.Unix(1700000000, 0)
		current := start
		c.now = func() time.Time { return current }
		c.mixinTTL = time.Minute

		if _, err := c.mixinKeyLocked(context.Background()); err != nil {
			t.Fatalf("first call: %v", err)
		}
		// Advance past TTL and expect a re-fetch.
		current = start.Add(2 * time.Minute)
		if _, err := c.mixinKeyLocked(context.Background()); err != nil {
			t.Fatalf("second call: %v", err)
		}
		if got := hits.Load(); got != 2 {
			t.Errorf("nav hits = %d, want 2 after cache expiry", got)
		}
	})
}
