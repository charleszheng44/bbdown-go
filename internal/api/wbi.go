package api

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// mixinTable is the fixed 64-byte permutation used by Bilibili to derive the
// WBI mixin key from (img_key || sub_key). The table is a public constant
// reverse-engineered from the Bilibili web player and shared by every open
// WBI implementation (see e.g. upstream BBDown's GetMixinKey).
var mixinTable = [32]int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35,
	27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13,
}

// navResponse is a minimal decoding of x/web-interface/nav's data field.
// We only care about the WBI image URLs.
type navResponse struct {
	WbiImg struct {
		ImgURL string `json:"img_url"`
		SubURL string `json:"sub_url"`
	} `json:"wbi_img"`
}

// mixinKey returns the cached 32-byte WBI mixin key, fetching and deriving
// it from the nav endpoint if the cache is empty or stale. Thread-safe.
func (c *Client) mixinKeyLocked(ctx context.Context) (string, error) {
	c.mixinMu.Lock()
	defer c.mixinMu.Unlock()
	if c.mixinKey != "" && c.now().Before(c.mixinExpiry) {
		return c.mixinKey, nil
	}

	raw, err := c.doJSON(ctx, apiBase+"/x/web-interface/nav")
	if err != nil {
		return "", fmt.Errorf("api: fetch nav: %w", err)
	}
	var nav navResponse
	if err := json.Unmarshal(raw, &nav); err != nil {
		return "", fmt.Errorf("api: decode nav: %w", err)
	}
	img := filenameNoExt(nav.WbiImg.ImgURL)
	sub := filenameNoExt(nav.WbiImg.SubURL)
	if img == "" || sub == "" {
		return "", fmt.Errorf("%w: nav wbi_img missing img_url/sub_url", ErrUnknownResponse)
	}
	key := deriveMixinKey(img + sub)
	c.mixinKey = key
	c.mixinExpiry = c.now().Add(c.mixinTTL)
	return key, nil
}

// filenameNoExt returns the basename of u stripped of its extension.
// Example: "https://i0.hdslb.com/bfs/wbi/abc123.png" -> "abc123".
// Returns "" if the URL has no recognisable basename.
func filenameNoExt(u string) string {
	if u == "" {
		return ""
	}
	// Strip the URL prefix — path.Base handles both "/a/b.png" and
	// "https://host/a/b.png" because it splits on '/' only.
	base := path.Base(u)
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	return base
}

// deriveMixinKey applies the fixed mixinTable permutation to src and
// truncates to 32 bytes. src must be at least 64 bytes long; shorter inputs
// are accepted but produce a key derived only from what's available.
func deriveMixinKey(src string) string {
	var b strings.Builder
	b.Grow(32)
	for _, i := range mixinTable {
		if i < 0 || i >= len(src) {
			continue
		}
		b.WriteByte(src[i])
	}
	return b.String()
}

// signWBI returns a query string (without leading '?') containing params
// augmented with wts=<now> and w_rid=<md5(sorted+mixinKey)>. Values are URL-
// encoded using the same rules Bilibili applies: application/x-www-form-
// urlencoded, with '!' '\” '(' ')' and '*' left untouched.
//
// now and mixinKey are passed in so the function is fully deterministic and
// trivially unit-testable.
func signWBI(params map[string]string, mixinKey string, now time.Time) string {
	// wts is the unix timestamp in seconds.
	merged := make(map[string]string, len(params)+1)
	for k, v := range params {
		merged[k] = v
	}
	merged["wts"] = strconv.FormatInt(now.Unix(), 10)

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(wbiEscape(k))
		b.WriteByte('=')
		b.WriteString(wbiEscape(merged[k]))
	}
	query := b.String()

	sum := md5.Sum([]byte(query + mixinKey))
	wrid := hex.EncodeToString(sum[:])

	return query + "&w_rid=" + wrid
}

// wbiEscape percent-encodes s per JavaScript's encodeURIComponent, which is
// the rule Bilibili's web player uses to build the signable query. Relative
// to Go's url.QueryEscape there are two differences: space is encoded as
// %20 rather than '+', and '!', '\”, '(', ')', '*' are left unescaped.
func wbiEscape(s string) string {
	esc := url.QueryEscape(s)
	// QueryEscape encodes space as '+'; encodeURIComponent uses "%20".
	esc = strings.ReplaceAll(esc, "+", "%20")
	// Reverse-map the Bilibili-exempt characters back.
	esc = strings.ReplaceAll(esc, "%21", "!")
	esc = strings.ReplaceAll(esc, "%27", "'")
	esc = strings.ReplaceAll(esc, "%28", "(")
	esc = strings.ReplaceAll(esc, "%29", ")")
	esc = strings.ReplaceAll(esc, "%2A", "*")
	return esc
}
