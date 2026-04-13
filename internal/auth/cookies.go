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

// Cookies holds the four Bilibili web-login cookies required for authenticated
// API access.
type Cookies struct {
	SESSDATA        string `json:"sessdata"`
	BiliJCT         string `json:"bili_jct"`
	DedeUserID      string `json:"dede_user_id"`
	DedeUserIDCkMd5 string `json:"dede_user_id_ck_md5"`
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

// AsJar returns an http.CookieJar pre-populated with the four Bilibili
// cookies scoped to api.bilibili.com. The returned jar is safe for
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
	jar.SetCookies(u, cookies)
	return jar
}

// ParseCookieString parses a cookie header-style string such as
// "SESSDATA=a; bili_jct=b; DedeUserID=c; DedeUserID__ckMd5=d" into a
// Cookies value. Unknown entries are ignored; missing entries yield empty
// fields. Whitespace around entries is trimmed.
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
		}
	}
	return c, nil
}
