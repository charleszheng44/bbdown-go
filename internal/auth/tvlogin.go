package auth

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"
)

// TV-login constants. These match the values BBDown uses; Bilibili's
// passport-tv endpoints validate both the sign HMAC and several of
// these fields, so do not change any of them casually.
const (
	tvAppkey   = "4409e2ce8ffd12b8"
	tvAppsec   = "59b43e04ad6965f34319062b478f83dd"
	tvMobiApp  = "android_tv_yst"
	tvPlatform = "android"
	tvBuild    = "102801"
	tvChannel  = "master"
	tvDevice   = "OnePlus"
	tvDeviceNm = "OnePlus7TPro"
	tvPlat     = "Android10OnePlusHD1910"
	tvSysVer   = "29"
	tvNetwork  = "wifi"
	tvIDLen    = 20
	tvBuvidLen = 37
	tvFPSuffix = 45
)

// tvSign returns the lowercase hex md5 of (queryString + appsec), which
// Bilibili validates server-side.
func tvSign(queryString string) string {
	sum := md5.Sum([]byte(queryString + tvAppsec))
	return hex.EncodeToString(sum[:])
}

// encodeOrdered builds "k1=v1&k2=v2..." preserving the caller's slice
// order, URL-escaping values. Bilibili's sign check is order-sensitive;
// callers must pass params in the exact order BBDown uses.
func encodeOrdered(kvs [][2]string) string {
	var b strings.Builder
	for i, kv := range kvs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(kv[0]))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(kv[1]))
	}
	return b.String()
}

// tvLoginParams builds the 20-field parameter list for both the
// auth_code and poll endpoints. Pass "" for authCode on the initial
// auth_code request. nowFn/randStr are injected for deterministic
// testing; production callers pass timeNowUnix and randomAlnum.
func tvLoginParams(authCode string, nowFn func() int64, randStr func(n int) string) [][2]string {
	deviceID := randStr(tvIDLen)
	buvid := randStr(tvBuvidLen)
	ts := fmt.Sprintf("%d", nowFn())
	fingerprint := time.Unix(nowFn(), 0).UTC().Format("20060102150405000") + randStr(tvFPSuffix)

	params := [][2]string{
		{"appkey", tvAppkey},
		{"auth_code", authCode},
		{"bili_local_id", deviceID},
		{"build", tvBuild},
		{"buvid", buvid},
		{"channel", tvChannel},
		{"device", tvDevice},
		{"device_id", deviceID},
		{"device_name", tvDeviceNm},
		{"device_platform", tvPlat},
		{"fingerprint", fingerprint},
		{"guid", buvid},
		{"local_fingerprint", fingerprint},
		{"local_id", buvid},
		{"mobi_app", tvMobiApp},
		{"networkstate", tvNetwork},
		{"platform", tvPlatform},
		{"sys_ver", tvSysVer},
		{"ts", ts},
	}
	sig := tvSign(encodeOrdered(params))
	return append(params, [2]string{"sign", sig})
}

// timeNowUnix / randomAlnum are the production injections for
// tvLoginParams. Exposed so tests stay deterministic.
func timeNowUnix() int64 { return time.Now().Unix() }

var tvRandAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_0123456789"

func randomAlnum(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = tvRandAlphabet[rand.Intn(len(tvRandAlphabet))]
	}
	return string(b)
}

// Endpoints for the TV flow. Package-level so tests can redirect.
var (
	tvAuthCodeBase           = "https://passport.snm0516.aisee.tv"
	tvPollBase               = "https://passport.bilibili.com"
	tvPollInterval           = 1 * time.Second
	tvQRWriter     io.Writer = os.Stdout
	tvLogWriter    io.Writer = os.Stderr
	tvNow                    = func() time.Time { return time.Now() }
)

// tvAuthCodeReply mirrors the shape of /x/passport-tv-login/qrcode/auth_code.
type tvAuthCodeReply struct {
	Code int `json:"code"`
	Data struct {
		URL      string `json:"url"`
		AuthCode string `json:"auth_code"`
	} `json:"data"`
}

// tvPollReply mirrors the shape of /x/passport-tv-login/qrcode/poll.
type tvPollReply struct {
	Code int `json:"code"`
	Data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		MID          int64  `json:"mid"`
	} `json:"data"`
}

// LoginTV runs the TV QR-code login flow and returns the resulting
// TVAuth. The QR content is rendered to tvQRWriter (stdout by default);
// polling happens every tvPollInterval (1 s) until the user confirms,
// the code expires, or ctx is canceled.
func LoginTV(ctx context.Context, client *http.Client) (*TVAuth, error) {
	if client == nil {
		client = http.DefaultClient
	}

	authCode, qrURL, err := tvRequestAuthCode(ctx, client)
	if err != nil {
		return nil, err
	}
	qrterminal.GenerateWithConfig(qrURL, qrterminal.Config{
		Level:     qrterminal.L,
		Writer:    tvQRWriter,
		BlackChar: qrterminal.BLACK,
		WhiteChar: qrterminal.WHITE,
		QuietZone: 1,
	})

	return tvPollLoop(ctx, client, authCode)
}

func tvRequestAuthCode(ctx context.Context, client *http.Client) (authCode, qrURL string, err error) {
	params := tvLoginParams("", func() int64 { return tvNow().Unix() }, randomAlnum)
	body := strings.NewReader(encodeOrdered(params))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		tvAuthCodeBase+"/x/passport-tv-login/qrcode/auth_code", body)
	if err != nil {
		return "", "", fmt.Errorf("auth: build tv auth_code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("auth: tv auth_code: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("auth: tv auth_code: status %s", resp.Status)
	}
	var parsed tvAuthCodeReply
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", "", fmt.Errorf("auth: decode tv auth_code: %w", err)
	}
	if parsed.Code != 0 || parsed.Data.AuthCode == "" || parsed.Data.URL == "" {
		return "", "", fmt.Errorf("auth: tv auth_code: code %d", parsed.Code)
	}
	return parsed.Data.AuthCode, parsed.Data.URL, nil
}

func tvPollLoop(ctx context.Context, client *http.Client, authCode string) (*TVAuth, error) {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}

		reply, err := tvPollOnce(ctx, client, authCode)
		if err != nil {
			return nil, err
		}
		switch reply.Code {
		case 0:
			now := tvNow().Unix()
			return &TVAuth{
				AccessToken:  reply.Data.AccessToken,
				RefreshToken: reply.Data.RefreshToken,
				MID:          reply.Data.MID,
				ExpiresAt:    now + reply.Data.ExpiresIn,
			}, nil
		case 86039:
			// Not scanned yet; keep polling silently.
		case 86038:
			return nil, ErrQRExpired
		default:
			return nil, fmt.Errorf("auth: tv poll unexpected code %d", reply.Code)
		}
		timer.Reset(tvPollInterval)
	}
}

func tvPollOnce(ctx context.Context, client *http.Client, authCode string) (*tvPollReply, error) {
	params := tvLoginParams(authCode, func() int64 { return tvNow().Unix() }, randomAlnum)
	body := strings.NewReader(encodeOrdered(params))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		tvPollBase+"/x/passport-tv-login/qrcode/poll", body)
	if err != nil {
		return nil, fmt.Errorf("auth: build tv poll request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: tv poll: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth: tv poll: status %s", resp.Status)
	}
	var parsed tvPollReply
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("auth: decode tv poll: %w", err)
	}
	return &parsed, nil
}
