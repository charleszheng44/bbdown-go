// Package auth implements Bilibili QR-code login and persistent cookie storage
// for the bbdown-go CLI.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
)

// Sentinel errors returned by this package.
var (
	// ErrNotLoggedIn is returned by Load when the cookie file does not exist.
	ErrNotLoggedIn = errors.New("not logged in")
	// ErrQRExpired is returned by LoginQR when the QR code has expired.
	ErrQRExpired = errors.New("qr code expired")
	// ErrQRCanceled is returned by LoginQR when the user cancels scanning.
	ErrQRCanceled = errors.New("qr code login canceled by user")
)

// Cookies holds the Bilibili web-login cookies required for authenticated
// API access.
//
// SESSDATA/BiliJCT/DedeUserID/DedeUserIDCkMd5 come from the QR-login success
// URL. Buvid3 is a browser/device fingerprint cookie the site sets on first
// visit; the pgc/pugv playurl endpoints downgrade responses to preview-only
// clips when it is missing, even for purchased content. It is populated by
// the login flow via an extra fetch to /x/frontend/finger/spi and is
// optional for non-cheese/non-bangumi use.
//
// Extras carries any additional cookies provided via --cookie (e.g.
// buvid4, bili_ticket, _uuid, CURRENT_FNVAL) that the current endpoint
// set does not name explicitly. They are attached to the cookie jar and
// persisted alongside the named cookies, so users can paste a full
// browser cookie string and have every entry reach Bilibili verbatim.
type Cookies struct {
	SESSDATA        string            `json:"sessdata"`
	BiliJCT         string            `json:"bili_jct"`
	DedeUserID      string            `json:"dede_user_id"`
	DedeUserIDCkMd5 string            `json:"dede_user_id_ck_md5"`
	Buvid3          string            `json:"buvid3,omitempty"`
	Extras          map[string]string `json:"extras,omitempty"`
}

// Store writes c to path as JSON with mode 0600. The parent directory must
// already exist.
func Store(path string, c Cookies) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: marshal cookies: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("auth: write cookies: %w", err)
	}
	return nil
}

// Load reads and unmarshals a Cookies struct from path. If path does not
// exist, Load returns ErrNotLoggedIn.
func Load(path string) (Cookies, error) {
	var c Cookies
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Cookies{}, ErrNotLoggedIn
		}
		return Cookies{}, fmt.Errorf("auth: read cookies: %w", err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return Cookies{}, fmt.Errorf("auth: unmarshal cookies: %w", err)
	}
	return c, nil
}

// AsJar returns an http.CookieJar pre-populated with the Bilibili cookies
// scoped to api.bilibili.com. Buvid3 and Extras entries are included only
// when non-empty. Reserved-name collisions inside Extras (SESSDATA etc.)
// are ignored — the named fields always win. The returned jar is safe for
// concurrent use by multiple goroutines (see net/http/cookiejar).
func (c Cookies) AsJar() http.CookieJar {
	jar, _ := cookiejar.New(nil) // New with nil options never returns an error.
	u, _ := url.Parse("https://api.bilibili.com/")
	cookies := []*http.Cookie{
		{Name: "SESSDATA", Value: c.SESSDATA},
		{Name: "bili_jct", Value: c.BiliJCT},
		{Name: "DedeUserID", Value: c.DedeUserID},
		{Name: "DedeUserID__ckMd5", Value: c.DedeUserIDCkMd5},
	}
	if c.Buvid3 != "" {
		cookies = append(cookies, &http.Cookie{Name: "buvid3", Value: c.Buvid3})
	}
	for k, v := range c.Extras {
		if isReservedCookieName(k) || v == "" {
			continue
		}
		cookies = append(cookies, &http.Cookie{Name: k, Value: v})
	}
	jar.SetCookies(u, cookies)
	return jar
}

// isReservedCookieName reports whether k collides with a named field on
// Cookies. Callers must not shadow the named fields via Extras.
func isReservedCookieName(k string) bool {
	switch k {
	case "SESSDATA", "bili_jct", "DedeUserID", "DedeUserID__ckMd5", "buvid3":
		return true
	}
	return false
}

// ParseCookieString parses a cookie header-style string such as
// "SESSDATA=a; bili_jct=b; DedeUserID=c; DedeUserID__ckMd5=d" into a
// Cookies value. Whitespace around entries is trimmed. Named fields
// (SESSDATA/bili_jct/DedeUserID/DedeUserID__ckMd5/buvid3) map to their
// struct fields; any other key/value pair is preserved in Extras so a
// paste of a full browser cookie string reaches Bilibili verbatim.
func ParseCookieString(s string) (Cookies, error) {
	var c Cookies
	if strings.TrimSpace(s) == "" {
		return c, nil
	}
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			return Cookies{}, fmt.Errorf("auth: malformed cookie entry %q", part)
		}
		k := strings.TrimSpace(part[:eq])
		v := strings.TrimSpace(part[eq+1:])
		switch k {
		case "SESSDATA":
			c.SESSDATA = v
		case "bili_jct":
			c.BiliJCT = v
		case "DedeUserID":
			c.DedeUserID = v
		case "DedeUserID__ckMd5":
			c.DedeUserIDCkMd5 = v
		case "buvid3":
			c.Buvid3 = v
		default:
			if c.Extras == nil {
				c.Extras = map[string]string{}
			}
			c.Extras[k] = v
		}
	}
	return c, nil
}
