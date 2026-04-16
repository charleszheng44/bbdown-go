package auth

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTVSignIsMD5OfQueryPlusAppsec(t *testing.T) {
	params := [][2]string{
		{"appkey", "4409e2ce8ffd12b8"},
		{"auth_code", ""},
		{"ts", "1700000000"},
	}
	qs := encodeOrdered(params)
	sig := tvSign(qs)
	expected := md5Hex(qs + tvAppsec)
	if sig != expected {
		t.Fatalf("tvSign mismatch: got %s want %s", sig, expected)
	}
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestTVLoginParamsAreOrderedAndComplete(t *testing.T) {
	params := tvLoginParams("my-auth-code", func() int64 { return 1700000000 }, func(n int) string {
		return strings.Repeat("A", n)
	})
	// The signature must be the LAST entry.
	if params[len(params)-1][0] != "sign" {
		t.Errorf("last key should be sign, got %s", params[len(params)-1][0])
	}
	// Required keys must be present.
	must := map[string]bool{
		"appkey": false, "auth_code": false, "bili_local_id": false, "build": false,
		"buvid": false, "channel": false, "device": false, "device_id": false,
		"device_name": false, "device_platform": false, "fingerprint": false,
		"guid": false, "local_fingerprint": false, "local_id": false, "mobi_app": false,
		"networkstate": false, "platform": false, "sys_ver": false, "ts": false, "sign": false,
	}
	for _, kv := range params {
		must[kv[0]] = true
	}
	for k, seen := range must {
		if !seen {
			t.Errorf("missing required param %q", k)
		}
	}
	// ts and auth_code must be what we passed.
	for _, kv := range params {
		if kv[0] == "ts" && kv[1] != "1700000000" {
			t.Errorf("ts wrong: %s", kv[1])
		}
		if kv[0] == "auth_code" && kv[1] != "my-auth-code" {
			t.Errorf("auth_code wrong: %s", kv[1])
		}
	}
	// Signature must verify against the rest of the query.
	var (
		signValue string
		others    [][2]string
	)
	for _, kv := range params {
		if kv[0] == "sign" {
			signValue = kv[1]
			continue
		}
		others = append(others, kv)
	}
	if signValue != md5Hex(encodeOrdered(others)+tvAppsec) {
		t.Errorf("sign does not verify")
	}
}

func TestEncodeOrderedEscapesSpecialChars(t *testing.T) {
	got := encodeOrdered([][2]string{{"k1", "a b"}, {"k2", "+"}})
	want := "k1=" + url.QueryEscape("a b") + "&k2=" + url.QueryEscape("+")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestLoginTVSuccess(t *testing.T) {
	var pollCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/x/passport-tv-login/qrcode/auth_code", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.FormValue("appkey") != tvAppkey {
			t.Errorf("appkey: %q", r.FormValue("appkey"))
		}
		if r.FormValue("sign") == "" {
			t.Errorf("sign missing")
		}
		io.WriteString(w, `{"code":0,"data":{"url":"bilibili://login?auth_code=AC","auth_code":"AC"}}`)
	})
	mux.HandleFunc("/x/passport-tv-login/qrcode/poll", func(w http.ResponseWriter, r *http.Request) {
		n := pollCalls.Add(1)
		if n == 1 {
			io.WriteString(w, `{"code":86039,"data":{}}`)
			return
		}
		io.WriteString(w, `{"code":0,"data":{"access_token":"at","refresh_token":"rt","expires_in":3600,"mid":42}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origAuthCodeBase := tvAuthCodeBase
	origPollBase := tvPollBase
	origPollInt := tvPollInterval
	origQR := tvQRWriter
	origLog := tvLogWriter
	origClock := tvNow
	tvAuthCodeBase = srv.URL
	tvPollBase = srv.URL
	tvPollInterval = 1 * time.Millisecond
	tvQRWriter = io.Discard
	tvLogWriter = io.Discard
	tvNow = func() time.Time { return time.Unix(1700000000, 0) }
	defer func() {
		tvAuthCodeBase = origAuthCodeBase
		tvPollBase = origPollBase
		tvPollInterval = origPollInt
		tvQRWriter = origQR
		tvLogWriter = origLog
		tvNow = origClock
	}()

	got, err := LoginTV(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("LoginTV: %v", err)
	}
	if got.AccessToken != "at" || got.RefreshToken != "rt" || got.MID != 42 {
		t.Errorf("unexpected auth: %+v", got)
	}
	if got.ExpiresAt != 1700000000+3600 {
		t.Errorf("ExpiresAt = %d, want %d", got.ExpiresAt, 1700000000+3600)
	}
}

func TestLoginTVExpired(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/x/passport-tv-login/qrcode/auth_code", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"code":0,"data":{"url":"bilibili://x","auth_code":"AC"}}`)
	})
	mux.HandleFunc("/x/passport-tv-login/qrcode/poll", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"code":86038,"data":{}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origAuthCodeBase := tvAuthCodeBase
	origPollBase := tvPollBase
	origPollInt := tvPollInterval
	origQR := tvQRWriter
	tvAuthCodeBase = srv.URL
	tvPollBase = srv.URL
	tvPollInterval = 1 * time.Millisecond
	tvQRWriter = io.Discard
	defer func() {
		tvAuthCodeBase = origAuthCodeBase
		tvPollBase = origPollBase
		tvPollInterval = origPollInt
		tvQRWriter = origQR
	}()

	_, err := LoginTV(context.Background(), srv.Client())
	if !errors.Is(err, ErrQRExpired) {
		t.Fatalf("want ErrQRExpired, got %v", err)
	}
}

func TestLoginTVContextCanceled(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/x/passport-tv-login/qrcode/auth_code", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"code":0,"data":{"url":"bilibili://x","auth_code":"AC"}}`)
	})
	mux.HandleFunc("/x/passport-tv-login/qrcode/poll", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"code":86039,"data":{}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origAuthCodeBase := tvAuthCodeBase
	origPollBase := tvPollBase
	origPollInt := tvPollInterval
	origQR := tvQRWriter
	tvAuthCodeBase = srv.URL
	tvPollBase = srv.URL
	tvPollInterval = 1 * time.Millisecond
	tvQRWriter = io.Discard
	defer func() {
		tvAuthCodeBase = origAuthCodeBase
		tvPollBase = origPollBase
		tvPollInterval = origPollInt
		tvQRWriter = origQR
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := LoginTV(ctx, srv.Client())
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("want context error, got %v", err)
	}
}
