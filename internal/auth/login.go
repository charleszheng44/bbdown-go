package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"
)

// passportBase is the base URL of the Bilibili passport service. It is a
// package-level variable (not a constant) so tests can point the login flow at
// an httptest server.
var passportBase = "https://passport.bilibili.com"

// apiBase is the base URL used for the /x/frontend/finger/spi buvid3 fetch.
// Kept here (rather than importing from internal/api) so the auth package
// stays dependency-free.
var apiBase = "https://api.bilibili.com"

// pollInterval controls how often LoginQR polls the passport service. Exposed
// as a variable so tests can shorten it.
var pollInterval = 1 * time.Second

// qrWriter is the destination for the rendered terminal QR code. Tests may
// redirect it to io.Discard.
var qrWriter io.Writer = os.Stdout

// logWriter receives a one-line status message when the QR code has been
// scanned but not yet confirmed. Tests may swap it.
var logWriter io.Writer = os.Stderr

// generateResponse mirrors the JSON returned by .../qrcode/generate.
type generateResponse struct {
	Data struct {
		URL       string `json:"url"`
		QRCodeKey string `json:"qrcode_key"`
	} `json:"data"`
}

// pollResponse mirrors the JSON returned by .../qrcode/poll.
type pollResponse struct {
	Data struct {
		URL          string `json:"url"`
		Code         int    `json:"code"`
		RefreshToken string `json:"refresh_token"`
	} `json:"data"`
}

// LoginQR runs the full QR-code login flow:
//  1. Request a QR code URL + key from passport.bilibili.com.
//  2. Render the URL as a QR code in the terminal via qrterminal.
//  3. Poll the passport service at 1 Hz until the user completes login,
//     the code expires, or ctx is canceled.
//  4. Parse the four Bilibili cookies out of the success redirect URL and
//     return them.
//
// The passed-in client is used for all HTTP traffic; if nil, http.DefaultClient
// is used.
func LoginQR(ctx context.Context, client *http.Client) (Cookies, error) {
	if client == nil {
		client = http.DefaultClient
	}

	loginURL, qrKey, err := requestQRCode(ctx, client)
	if err != nil {
		return Cookies{}, err
	}

	qrterminal.GenerateWithConfig(loginURL, qrterminal.Config{
		Level:     qrterminal.L,
		Writer:    qrWriter,
		BlackChar: qrterminal.BLACK,
		WhiteChar: qrterminal.WHITE,
		QuietZone: 1,
	})

	cookies, err := pollLogin(ctx, client, qrKey)
	if err != nil {
		return Cookies{}, err
	}
	// Best-effort buvid3 fetch. The four cookies above are sufficient for
	// most endpoints; cheese/bangumi playurl, however, silently downgrades
	// responses to preview-only clips without buvid3. We do not fail the
	// login when this call fails — the rest of the cookie set is still
	// valuable — but we log a warning so a later playurl failure is
	// diagnosable.
	if b3, err := fetchBuvid3(ctx, client); err != nil {
		fmt.Fprintf(logWriter, "warning: could not fetch buvid3 (%v); pgc/pugv playurl may return preview-only responses\n", err)
	} else {
		cookies.Buvid3 = b3
	}
	return cookies, nil
}

// fingerSPIResponse mirrors the JSON returned by /x/frontend/finger/spi.
type fingerSPIResponse struct {
	Code int `json:"code"`
	Data struct {
		B3 string `json:"b_3"`
		B4 string `json:"b_4"`
	} `json:"data"`
}

// fetchBuvid3 calls /x/frontend/finger/spi and returns the b_3 value.
// Bilibili's web front-end uses this endpoint to seed fresh buvid3/buvid4
// cookies on first visit; we call it once at login time and persist b_3.
func fetchBuvid3(ctx context.Context, client *http.Client) (string, error) {
	endpoint := apiBase + "/x/frontend/finger/spi"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("auth: build finger/spi request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("auth: finger/spi: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth: finger/spi: status %s", resp.Status)
	}
	var parsed fingerSPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("auth: decode finger/spi: %w", err)
	}
	if parsed.Code != 0 {
		return "", fmt.Errorf("auth: finger/spi: api code %d", parsed.Code)
	}
	if parsed.Data.B3 == "" {
		return "", fmt.Errorf("auth: finger/spi: empty b_3")
	}
	return parsed.Data.B3, nil
}

// requestQRCode hits .../qrcode/generate and returns (loginURL, qrcodeKey).
func requestQRCode(ctx context.Context, client *http.Client) (string, string, error) {
	endpoint := passportBase + "/x/passport-login/web/qrcode/generate?source=main-fe-header"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", fmt.Errorf("auth: build generate request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("auth: generate qr: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("auth: generate qr: status %s", resp.Status)
	}
	var parsed generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", "", fmt.Errorf("auth: decode generate response: %w", err)
	}
	if parsed.Data.URL == "" || parsed.Data.QRCodeKey == "" {
		return "", "", fmt.Errorf("auth: generate response missing url/qrcode_key")
	}
	return parsed.Data.URL, parsed.Data.QRCodeKey, nil
}

// pollLogin polls the passport service for qrKey until a terminal state is
// reached or ctx is canceled.
func pollLogin(ctx context.Context, client *http.Client, qrKey string) (Cookies, error) {
	pollURL := fmt.Sprintf(
		"%s/x/passport-login/web/qrcode/poll?qrcode_key=%s&source=main-fe-header",
		passportBase, url.QueryEscape(qrKey),
	)

	// Kick off the first poll immediately, then fall into the ticker loop.
	timer := time.NewTimer(0)
	defer timer.Stop()

	confirmedLogged := false
	for {
		select {
		case <-ctx.Done():
			return Cookies{}, ctx.Err()
		case <-timer.C:
		}

		code, successURL, err := pollOnce(ctx, client, pollURL)
		if err != nil {
			return Cookies{}, err
		}

		switch code {
		case 0:
			return cookiesFromSuccessURL(successURL)
		case 86101:
			// waiting for scan; keep polling silently.
		case 86090:
			if !confirmedLogged {
				fmt.Fprintln(logWriter, "QR code scanned, waiting for confirmation...")
				confirmedLogged = true
			}
		case 86038:
			return Cookies{}, ErrQRExpired
		default:
			return Cookies{}, fmt.Errorf("auth: unexpected poll code %d", code)
		}

		timer.Reset(pollInterval)
	}
}

// pollOnce performs a single GET against the poll endpoint.
func pollOnce(ctx context.Context, client *http.Client, pollURL string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
	if err != nil {
		return 0, "", fmt.Errorf("auth: build poll request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("auth: poll qr: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("auth: poll qr: status %s", resp.Status)
	}
	var parsed pollResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, "", fmt.Errorf("auth: decode poll response: %w", err)
	}
	return parsed.Data.Code, parsed.Data.URL, nil
}

// cookiesFromSuccessURL parses the four Bilibili cookies out of the success
// redirect URL returned by the poll endpoint.
//
// Values are read from u.RawQuery WITHOUT percent-decoding. Bilibili's
// SESSDATA is delivered as e.g. "abc%2C123%2Ccef%2A41" — the %2C and %2A
// must be sent back verbatim in the Cookie header; decoding them to ","
// and "*" causes the server to reject or truncate the value (observed
// as preview-only playurl responses for purchased content).
func cookiesFromSuccessURL(raw string) (Cookies, error) {
	if raw == "" {
		return Cookies{}, fmt.Errorf("auth: empty success url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Cookies{}, fmt.Errorf("auth: parse success url: %w", err)
	}
	c := Cookies{
		SESSDATA:        rawQueryParam(u.RawQuery, "SESSDATA"),
		BiliJCT:         rawQueryParam(u.RawQuery, "bili_jct"),
		DedeUserID:      rawQueryParam(u.RawQuery, "DedeUserID"),
		DedeUserIDCkMd5: rawQueryParam(u.RawQuery, "DedeUserID__ckMd5"),
	}
	if c.SESSDATA == "" || c.BiliJCT == "" || c.DedeUserID == "" || c.DedeUserIDCkMd5 == "" {
		return Cookies{}, fmt.Errorf("auth: success url missing one or more cookies")
	}
	return c, nil
}

// rawQueryParam returns the raw (percent-encoded) value of key in a URL
// query string, or "" if the key is absent. Unlike url.Values.Get it does
// not decode %2C, %2A, or any other percent-escapes — the caller gets the
// byte sequence the server sent, suitable for direct re-emission in a
// Cookie header.
func rawQueryParam(rawQuery, key string) string {
	prefix := key + "="
	for _, pair := range strings.Split(rawQuery, "&") {
		if strings.HasPrefix(pair, prefix) {
			return pair[len(prefix):]
		}
	}
	return ""
}
