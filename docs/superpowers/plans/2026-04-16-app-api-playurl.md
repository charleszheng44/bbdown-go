# App-API playurl fallback — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an opt-in TV-login flow plus an app-API (gRPC-over-HTTP/2) PlayView client, and auto-fallback from the pgc/pugv web preview response to the app path — so purchased cheese/bangumi downloads just work once a user runs `bbdown login --tv` once.

**Architecture:** Reuse the existing `auth.Cookies` file as the single storage surface by adding a `TV *TVAuth` field. `internal/auth/tvlogin.go` provides `LoginTV(ctx, client) (*TVAuth, error)`. `internal/api/app.go` builds a `PlayViewReq`, frames it for gRPC (5-byte length prefix + gzip), POSTs HTTP/2 to `app.bilibili.com`, and maps the decoded `PlayViewReply` into the existing `api.PlayInfo`. `fetchBangumi` / `fetchCourse` catch `ErrContentLocked` from `decodePgcPlayurl` and retry via `fetchViaApp` when `Client.appAuth` is set.

**Tech Stack:** Go 1.25, `google.golang.org/protobuf` (new runtime dep, ~2 MB transitive), `compress/gzip`, `net/http` (HTTP/2 native), stdlib `crypto/md5` for TV sign. Committed `.pb.go` files so `protoc` is not needed at build time.

**Spec:** `docs/superpowers/specs/2026-04-16-app-api-playurl-design.md`

**Branch:** `feat/app-api-playurl` (already checked out).

---

## File Structure

- **Create:** `internal/auth/tvlogin.go` — `LoginTV`, `tvSignParams`, `tvLoginParams` helpers, `ErrTVTokenExpired`.
- **Create:** `internal/auth/tvlogin_test.go`.
- **Modify:** `internal/auth/cookies.go` — add `TVAuth` struct and `TV *TVAuth` field on `Cookies`.
- **Modify:** `internal/auth/cookies_test.go` — round-trip a `TVAuth`.
- **Modify:** `cmd/bbdown/login.go` — add `--tv` flag that calls `LoginTV` and merges the token into the existing cookies file.
- **Modify:** `cmd/bbdown/errors.go` — hint text when `ErrContentLocked` fires without TV auth.
- **Modify:** `cmd/bbdown/download.go` — pass `cookies.TV` through to `api.Client.SetAppAuth` in both the download path and `bbdown parts`.
- **Modify:** `cmd/bbdown/parts_list.go` — same SetAppAuth wiring.
- **Create:** `internal/api/appproto/playviewreq.proto` + `playviewreq.pb.go`.
- **Create:** `internal/api/appproto/playviewreply.proto` + `playviewreply.pb.go`.
- **Create:** `internal/api/appproto/headers.proto` + `headers.pb.go` (Device, Metadata, Locale, Network, FawkesReq in one file).
- **Create:** `internal/api/app_framing.go` — `packFrame`, `unpackFrame`.
- **Create:** `internal/api/app_framing_test.go`.
- **Create:** `internal/api/app.go` — `fetchViaApp`, header-bin builders, HTTP/2 POST.
- **Create:** `internal/api/app_test.go` + `testdata/playviewreply_ok.bin`, `testdata/playviewreply_locked.bin` (generated during Task 7, committed).
- **Modify:** `internal/api/client.go` — `Client.appAuth` field + `SetAppAuth`.
- **Modify:** `internal/api/playinfo.go` — fallback calls in `fetchBangumi` and `fetchCourse`.
- **Modify:** `internal/api/api_test.go` — integration test covering the fallback.
- **Modify:** `go.mod` / `go.sum` — new dep `google.golang.org/protobuf`.
- **Modify:** `Makefile` — new `proto` target that runs `protoc` and a `proto-check` target for CI.
- **Modify:** `README.md` — document `bbdown login --tv` in the Command Reference.

---

### Task 1: `TVAuth` type and persistence

**Files:**
- Modify: `internal/auth/cookies.go`
- Test: `internal/auth/cookies_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/auth/cookies_test.go`:

```go
func TestStoreLoadRoundTripWithTVAuth(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	want := Cookies{
		SESSDATA:        "s",
		BiliJCT:         "j",
		DedeUserID:      "1",
		DedeUserIDCkMd5: "m",
		TV: &TVAuth{
			AccessToken:  "tv-access",
			RefreshToken: "tv-refresh",
			MID:          42,
			ExpiresAt:    1800000000,
		},
	}
	if err := Store(path, want); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.TV == nil {
		t.Fatalf("TV auth missing after round-trip: %+v", got)
	}
	if got.TV.AccessToken != "tv-access" || got.TV.MID != 42 {
		t.Fatalf("TV auth mismatch: %+v", got.TV)
	}
}

func TestStoreLoadRoundTripWithoutTVAuth(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	want := Cookies{SESSDATA: "s", BiliJCT: "j", DedeUserID: "1", DedeUserIDCkMd5: "m"}
	if err := Store(path, want); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.TV != nil {
		t.Fatalf("TV auth should be nil when omitted, got %+v", got.TV)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/auth/ -run TestStoreLoadRoundTripWithTVAuth -v`
Expected: build failure — `undefined: TVAuth`.

- [ ] **Step 3: Add the TVAuth type and field**

In `internal/auth/cookies.go`, add below the `Cookies` struct declaration:

```go
// TVAuth holds the bearer credentials produced by the TV QR-login flow.
// The app-API playurl endpoint authenticates with AccessToken (sent as
// "authorization: identify_v1 <token>"); the rest of the fields are
// persisted so an expiry hint can be shown without a round-trip.
type TVAuth struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	MID          int64  `json:"mid"`
	// ExpiresAt is the absolute unix-seconds timestamp when this token
	// stops being accepted (= login_time + expires_in).
	ExpiresAt int64 `json:"expires_at"`
}
```

Modify the `Cookies` struct to add the `TV` field (place it after `Extras`):

```go
type Cookies struct {
	SESSDATA        string            `json:"sessdata"`
	BiliJCT         string            `json:"bili_jct"`
	DedeUserID      string            `json:"dede_user_id"`
	DedeUserIDCkMd5 string            `json:"dede_user_id_ck_md5"`
	Buvid3          string            `json:"buvid3,omitempty"`
	Extras          map[string]string `json:"extras,omitempty"`
	TV              *TVAuth           `json:"tv,omitempty"`
}
```

> Note: the `Buvid3` and `Extras` fields are added by the sibling `feat/parts-list` branch (PR #32). If PR #32 has not merged yet, rebase this branch onto it before starting. If it has merged to main, no action needed — a fresh `main` already has both fields.

- [ ] **Step 4: Add the new sentinel error**

In `internal/auth/cookies.go`, add to the existing `var (...)` error block:

```go
// ErrTVTokenExpired is returned by the app-API client when Bilibili
// rejects the access token (code 86208 or HTTP 401). Callers should
// prompt the user to re-run `bbdown login --tv`.
ErrTVTokenExpired = errors.New("tv access token expired")
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/auth/`
Expected: all tests PASS, including the two new round-trip cases.

- [ ] **Step 6: Commit**

```bash
gofmt -w .
git add internal/auth/cookies.go internal/auth/cookies_test.go
git commit -m "feat(auth): TVAuth type on Cookies + ErrTVTokenExpired sentinel"
```

---

### Task 2: TV login sign + param builder (pure helpers)

**Files:**
- Create: `internal/auth/tvlogin.go`
- Create: `internal/auth/tvlogin_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/auth/tvlogin_test.go`:

```go
package auth

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/auth/ -run 'TestTV|TestEncodeOrdered'`
Expected: build failure — `undefined: tvSign`, `tvLoginParams`, `encodeOrdered`, `tvAppsec`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/auth/tvlogin.go`:

```go
package auth

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"time"
)

// TV-login constants. These match the values BBDown uses; Bilibili's
// passport-tv endpoints validate both the sign HMAC and several of
// these fields, so do not change any of them casually.
const (
	tvAppkey    = "4409e2ce8ffd12b8"
	tvAppsec    = "59b43e04ad6965f34319062b478f83dd"
	tvMobiApp   = "android_tv_yst"
	tvPlatform  = "android"
	tvBuild     = "102801"
	tvChannel   = "master"
	tvDevice    = "OnePlus"
	tvDeviceNm  = "OnePlus7TPro"
	tvPlat      = "Android10OnePlusHD1910"
	tvSysVer    = "29"
	tvNetwork   = "wifi"
	tvIDLen     = 20
	tvBuvidLen  = 37
	tvFPSuffix  = 45
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
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/auth/ -run 'TestTV|TestEncodeOrdered' -v`
Expected: all three cases PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w .
git add internal/auth/tvlogin.go internal/auth/tvlogin_test.go
git commit -m "feat(auth): TV login sign and parameter helpers"
```

---

### Task 3: `LoginTV` flow (auth_code + poll)

**Files:**
- Modify: `internal/auth/tvlogin.go`
- Modify: `internal/auth/tvlogin_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/auth/tvlogin_test.go` (merge imports):

```go
import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"
)

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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/auth/ -run 'TestLoginTV'`
Expected: build failure — `undefined: LoginTV`, `tvAuthCodeBase`, etc.

- [ ] **Step 3: Add the `LoginTV` implementation**

Append to `internal/auth/tvlogin.go` (merge new imports: `"context"`, `"encoding/json"`, `"io"`, `"net/http"`, `"os"`):

```go
import (
	"github.com/mdp/qrterminal/v3"
)

// Endpoints for the TV flow. Package-level so tests can redirect.
var (
	tvAuthCodeBase  = "https://passport.snm0516.aisee.tv"
	tvPollBase      = "https://passport.bilibili.com"
	tvPollInterval  = 1 * time.Second
	tvQRWriter      io.Writer = os.Stdout
	tvLogWriter     io.Writer = os.Stderr
	tvNow                     = func() time.Time { return time.Now() }
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
```

Merge the new imports into the existing `import (...)` block so there is still exactly one group in the file.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/auth/ -run TestLoginTV -v`
Expected: all three `TestLoginTV*` cases PASS.

- [ ] **Step 5: Run the full package**

Run: `go test ./internal/auth/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w .
git add internal/auth/tvlogin.go internal/auth/tvlogin_test.go
git commit -m "feat(auth): LoginTV QR flow (auth_code + poll)"
```

---

### Task 4: `bbdown login --tv` wiring

**Files:**
- Modify: `cmd/bbdown/login.go`

- [ ] **Step 1: Add the flag and dispatch**

The current `newLoginCmd` already accepts `--cookie / --cookie-file / --cookie-stdin`. Extend it by adding a `--tv` flag and a branch that, when set, runs `LoginTV` and merges the returned `TVAuth` into any already-stored cookies (or errors if no web cookies exist yet — web login must come first).

Replace the body of `newLoginCmd` with:

```go
func newLoginCmd(flags *rootFlags) *cobra.Command {
	var (
		cookieFile  string
		cookieStdin bool
		tv          bool
	)
	cmd := &cobra.Command{
		Use:           "login",
		Short:         "Log in to Bilibili (QR code, cookie import, or TV QR with --tv)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			path, err := config.CookiesFile()
			if err != nil {
				return fmt.Errorf("resolve cookies path: %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return fmt.Errorf("create config dir: %w", err)
			}

			if tv {
				return runTVLogin(ctx, cmd.OutOrStdout(), path)
			}

			raw, err := resolveImportedCookie(cmd.InOrStdin(), cmd.OutOrStdout(), flags.Cookie, cookieFile, cookieStdin)
			if err != nil {
				return err
			}

			var cookies auth.Cookies
			if raw != "" {
				cookies, err = auth.ParseCookieString(raw)
				if err != nil {
					return err
				}
				if cookies.SESSDATA == "" || cookies.BiliJCT == "" || cookies.DedeUserID == "" || cookies.DedeUserIDCkMd5 == "" {
					return fmt.Errorf("imported cookie must include SESSDATA, bili_jct, DedeUserID, and DedeUserID__ckMd5")
				}
			} else {
				cookies, err = auth.LoginQR(ctx, nil)
				if err != nil {
					return err
				}
			}
			if err := auth.Store(path, cookies); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Login successful. Cookies saved to %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&cookieFile, "cookie-file", "",
		"import cookies from a file whose contents are the DevTools cookie header value")
	cmd.Flags().BoolVar(&cookieStdin, "cookie-stdin", false,
		"read the cookie header value from stdin (terminated by EOF / Ctrl-D)")
	cmd.Flags().BoolVar(&tv, "tv", false,
		"run the TV QR flow to capture the app-API access token (one-time setup for purchased bangumi/cheese)")
	return cmd
}

// runTVLogin overlays a fresh TVAuth onto the existing cookies file. It
// refuses to run when no web cookies are persisted — TV login extends,
// not replaces, the web session.
func runTVLogin(ctx context.Context, w io.Writer, path string) error {
	existing, err := auth.Load(path)
	if err != nil {
		return fmt.Errorf("tv login requires an existing web login — run `bbdown login` first: %w", err)
	}
	tvAuth, err := auth.LoginTV(ctx, nil)
	if err != nil {
		return err
	}
	existing.TV = tvAuth
	if err := auth.Store(path, existing); err != nil {
		return err
	}
	fmt.Fprintf(w, "TV login successful. App-API access token saved to %s\n", path)
	return nil
}
```

Merge the new imports (`"context"`) into the existing block.

- [ ] **Step 2: Build and smoke-test**

Run: `go build ./... && go run ./cmd/bbdown login --help`
Expected: clean build; help output shows the `--tv` flag.

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`
Expected: PASS. No new tests in this task — the subcommand wiring is thin glue covered by the helper tests in Tasks 1–3 and the smoke build.

- [ ] **Step 4: Commit**

```bash
gofmt -w .
git add cmd/bbdown/login.go
git commit -m "feat(cmd): login --tv flag for app-API access-token setup"
```

---

### Task 5: Committed protobuf sources + generated `.pb.go`

**Files:**
- Create: `internal/api/appproto/playviewreq.proto`
- Create: `internal/api/appproto/playviewreply.proto`
- Create: `internal/api/appproto/headers.proto`
- Create: `internal/api/appproto/playviewreq.pb.go` (generated)
- Create: `internal/api/appproto/playviewreply.pb.go` (generated)
- Create: `internal/api/appproto/headers.pb.go` (generated)
- Modify: `go.mod`, `go.sum`
- Modify: `Makefile`

- [ ] **Step 1: Add the runtime protobuf dependency**

```bash
go get google.golang.org/protobuf@latest
```

Expected: `go.mod` and `go.sum` updated; `go build ./...` still clean (nothing imports the package yet).

- [ ] **Step 2: Write the .proto sources**

Create `internal/api/appproto/playviewreq.proto`:

```protobuf
syntax = "proto3";
package appproto;
option go_package = "github.com/charleszheng44/bbdown-go/internal/api/appproto";

message PlayViewReq {
  int64 ep_id = 1;
  int64 cid = 2;
  int64 qn = 3;
  int32 fnver = 4;
  int32 fnval = 5;
  uint32 download = 6;
  int32 force_host = 7;
  bool fourk = 8;
  string spmid = 9;
  string from_spmid = 10;
  int32 teenagers_mode = 11;
  enum CodeType {
    NOCODE = 0;
    CODE264 = 1;
    CODE265 = 2;
    CODEAV1 = 3;
  }
  CodeType prefer_codec_type = 12;
  bool is_preview = 13;
  int64 room_id = 14;
}
```

Create `internal/api/appproto/playviewreply.proto` (trimmed to fields we decode):

```protobuf
syntax = "proto3";
package appproto;
option go_package = "github.com/charleszheng44/bbdown-go/internal/api/appproto";

message PlayViewReply {
  VideoInfo video_info = 1;
}

message VideoInfo {
  uint32 quality = 1;
  string format = 2;
  uint64 timelength = 3;
  uint32 video_codecid = 4;
  repeated StreamItem stream_list = 5;
  repeated DashItem dash_audio = 6;
}

message StreamItem {
  StreamInfo stream_info = 1;
  DashVideo dash_video = 2;
}

message StreamInfo {
  uint32 quality = 1;
  string format = 2;
  string description = 3;
  uint32 err_code = 4;
}

message DashVideo {
  string base_url = 1;
  repeated string backup_url = 2;
  uint32 bandwidth = 3;
  uint32 codecid = 4;
  string md5 = 5;
  uint64 size = 6;
}

message DashItem {
  uint32 id = 1;
  string base_url = 2;
  repeated string backup_url = 3;
  uint32 bandwidth = 4;
  uint32 codecid = 5;
  string md5 = 6;
  uint64 size = 7;
}
```

> Note on `proto3` vs BBDown's `proto2`: `optional` in proto2 is the default in proto3, and wire format is identical for scalar fields we use. Switching to proto3 avoids `optional` scaffolding in the generated Go code. Unknown fields the server sends but we did not declare are preserved on the wire and silently dropped by `proto.Unmarshal`, which is exactly the behavior we want.

Create `internal/api/appproto/headers.proto`:

```protobuf
syntax = "proto3";
package appproto;
option go_package = "github.com/charleszheng44/bbdown-go/internal/api/appproto";

message Device {
  int32 app_id = 1;
  int32 build = 2;
  string buvid = 3;
  string mobi_app = 4;
  string platform = 5;
  string device = 6;
  string channel = 7;
  string brand = 8;
  string model = 9;
  string osver = 10;
}

message Metadata {
  string access_key = 1;
  string mobi_app = 2;
  string device = 3;
  int32 build = 4;
  string channel = 5;
  string buvid = 6;
  string platform = 7;
}

message Network {
  enum TYPE {
    NT_UNKNOWN = 0;
    WIFI = 1;
    CELLULAR = 2;
    OFFLINE = 3;
    OTHERNET = 4;
    ETHERNET = 5;
  }
  TYPE type = 1;
  string oid = 3;
}

message Locale {
  message LocaleIds {
    string language = 1;
    string region = 3;
  }
  LocaleIds c_locale = 1;
}

message FawkesReq {
  string appkey = 1;
  string env = 2;
  string session_id = 3;
}
```

- [ ] **Step 3: Generate the .pb.go files**

Run:

```bash
protoc --go_out=. --go_opt=paths=source_relative \
  internal/api/appproto/playviewreq.proto \
  internal/api/appproto/playviewreply.proto \
  internal/api/appproto/headers.proto
```

If `protoc-gen-go` is not installed, first run:
```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
```

Expected: three `*.pb.go` files created next to the `.proto` sources.

- [ ] **Step 4: Add Makefile targets**

Modify `Makefile` — add these targets (placement: after the existing `fmt` / `test` targets, before `.PHONY` list if any):

```makefile
.PHONY: proto proto-check

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
	  internal/api/appproto/playviewreq.proto \
	  internal/api/appproto/playviewreply.proto \
	  internal/api/appproto/headers.proto

# proto-check is used in CI: regenerates and fails if the committed
# .pb.go files drift from the .proto sources.
proto-check:
	@$(MAKE) proto
	@git diff --exit-code internal/api/appproto || { \
	  echo "Regenerate protobuf files: 'make proto' and commit."; \
	  exit 1; \
	}
```

Add `proto-check` to the list of targets run by whatever rollup target CI calls (`make check` or equivalent) — inspect the Makefile for the existing aggregator and append `proto-check` to its prerequisites.

- [ ] **Step 5: Verify the build and tests**

Run: `go build ./... && go test ./...`
Expected: clean build and all tests PASS — the new types exist but nothing references them yet.

- [ ] **Step 6: Commit**

```bash
gofmt -w .
git add go.mod go.sum Makefile internal/api/appproto/
git commit -m "feat(api): app-API protobuf schema and generated bindings"
```

---

### Task 6: gRPC framing helpers

**Files:**
- Create: `internal/api/app_framing.go`
- Create: `internal/api/app_framing_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/app_framing_test.go`:

```go
package api

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"testing"
)

func TestPackFrameGzipped(t *testing.T) {
	payload := []byte("hello-gzipped-payload")
	frame := packFrame(payload, true)
	if frame[0] != 0x01 {
		t.Fatalf("first byte should be 0x01 (gzip), got %#x", frame[0])
	}
	declaredLen := binary.BigEndian.Uint32(frame[1:5])
	body := frame[5:]
	if int(declaredLen) != len(body) {
		t.Errorf("declared len %d != body len %d", declaredLen, len(body))
	}
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("gunzip mismatch: got %q want %q", got, payload)
	}
}

func TestPackFramePlain(t *testing.T) {
	payload := []byte("hello-plain")
	frame := packFrame(payload, false)
	if frame[0] != 0x00 {
		t.Fatalf("first byte should be 0x00 (identity), got %#x", frame[0])
	}
	if int(binary.BigEndian.Uint32(frame[1:5])) != len(payload) {
		t.Fatalf("declared len mismatch")
	}
	if !bytes.Equal(frame[5:], payload) {
		t.Fatalf("body mismatch")
	}
}

func TestUnpackFrameRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
		gzip    bool
	}{
		{"gzipped", []byte("round-trip-me"), true},
		{"plain", []byte("round-trip-me"), false},
		{"empty_gzipped", []byte{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := packFrame(tc.payload, tc.gzip)
			got, err := unpackFrame(frame)
			if err != nil {
				t.Fatalf("unpackFrame: %v", err)
			}
			if !bytes.Equal(got, tc.payload) {
				t.Errorf("got %q want %q", got, tc.payload)
			}
		})
	}
}

func TestUnpackFrameRejectsTruncated(t *testing.T) {
	if _, err := unpackFrame([]byte{0x01, 0x00}); err == nil {
		t.Fatal("expected error on truncated frame")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -run 'TestPackFrame|TestUnpackFrame'`
Expected: build failure — `undefined: packFrame`, `unpackFrame`.

- [ ] **Step 3: Implement**

Create `internal/api/app_framing.go`:

```go
package api

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

// packFrame wraps a protobuf payload in gRPC's length-prefixed framing.
// Layout: 1-byte compression flag (1=gzip, 0=identity), 4-byte uint32
// big-endian length, then the payload itself. When compress=true the
// payload is gzipped first and the length describes the compressed
// body.
func packFrame(payload []byte, compress bool) []byte {
	body := payload
	var flag byte
	if compress {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		_, _ = gw.Write(payload)
		_ = gw.Close()
		body = buf.Bytes()
		flag = 0x01
	}
	out := make([]byte, 5+len(body))
	out[0] = flag
	binary.BigEndian.PutUint32(out[1:5], uint32(len(body)))
	copy(out[5:], body)
	return out
}

// unpackFrame parses a gRPC-framed message and returns the inner
// (decompressed, if needed) payload. It rejects short or inconsistent
// frames with a typed error so callers can surface ErrUnknownResponse.
func unpackFrame(frame []byte) ([]byte, error) {
	if len(frame) < 5 {
		return nil, fmt.Errorf("frame truncated: %d bytes", len(frame))
	}
	flag := frame[0]
	declared := binary.BigEndian.Uint32(frame[1:5])
	body := frame[5:]
	if uint32(len(body)) < declared {
		return nil, fmt.Errorf("frame body %d bytes, header declares %d", len(body), declared)
	}
	body = body[:declared]
	switch flag {
	case 0x00:
		return body, nil
	case 0x01:
		r, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		defer r.Close()
		return io.ReadAll(r)
	default:
		return nil, fmt.Errorf("unknown frame compression flag %#x", flag)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/api/ -run 'TestPackFrame|TestUnpackFrame' -v`
Expected: all cases PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w .
git add internal/api/app_framing.go internal/api/app_framing_test.go
git commit -m "feat(api): gRPC length-prefix framing (packFrame, unpackFrame)"
```

---

### Task 7: App-API playurl client (`fetchViaApp`)

**Files:**
- Create: `internal/api/app.go`
- Create: `internal/api/app_test.go`
- Create: `internal/api/testdata/playviewreply_ok.bin`
- Modify: `internal/api/client.go`

- [ ] **Step 1: Extend `api.Client` with optional app auth**

In `internal/api/client.go`, add an import `"github.com/charleszheng44/bbdown-go/internal/auth"` and, to the `Client` struct, add:

```go
	// appAuth carries a TV access token used by fetchViaApp. nil when
	// the caller has not configured app-API fallback; fetchViaApp is
	// skipped in that case.
	appAuth *auth.TVAuth
```

Add method:

```go
// SetAppAuth attaches a TV access token to the client. When set,
// fetchBangumi/fetchCourse will fall back to the app-API PlayView
// endpoint on ErrContentLocked from the web decoder. Passing nil
// disables fallback.
func (c *Client) SetAppAuth(a *auth.TVAuth) { c.appAuth = a }
```

This import introduces a new `internal/api -> internal/auth` dependency. Verify no import cycle: `internal/auth` does not import `internal/api` (grep confirms).

- [ ] **Step 2: Write the failing test**

Create `internal/api/app_test.go`:

```go
package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/charleszheng44/bbdown-go/internal/api/appproto"
	"github.com/charleszheng44/bbdown-go/internal/auth"
	"google.golang.org/protobuf/proto"
)

func TestFetchViaAppSuccess(t *testing.T) {
	// Build a minimal valid PlayViewReply as the fixture.
	reply := &appproto.PlayViewReply{
		VideoInfo: &appproto.VideoInfo{
			Timelength: 120_000,
			StreamList: []*appproto.StreamItem{
				{
					StreamInfo: &appproto.StreamInfo{Quality: 80, Description: "1080P"},
					DashVideo: &appproto.DashVideo{
						BaseUrl: "https://cdn/v.m4s", Bandwidth: 2_000_000,
						Codecid: 7, Size: 10_000_000,
					},
				},
			},
			DashAudio: []*appproto.DashItem{
				{Id: 30280, BaseUrl: "https://cdn/a.m4s", Bandwidth: 128_000},
			},
		},
	}
	body, err := proto.Marshal(reply)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "identify_v1 testtoken" {
			t.Errorf("authorization header missing/wrong: %q", r.Header.Get("Authorization"))
		}
		for _, h := range []string{"x-bili-metadata-bin", "x-bili-device-bin", "x-bili-locale-bin", "x-bili-network-bin", "x-bili-fawkes-req-bin"} {
			if v := r.Header.Get(h); v == "" {
				t.Errorf("missing header %s", h)
			} else if _, err := base64.StdEncoding.DecodeString(v); err != nil {
				t.Errorf("header %s not base64: %v", h, err)
			}
		}
		// Optional: verify request body decodes to PlayViewReq.
		raw, _ := io.ReadAll(r.Body)
		payload, err := unpackFrame(raw)
		if err != nil {
			t.Errorf("request frame unpack: %v", err)
		}
		var req appproto.PlayViewReq
		if err := proto.Unmarshal(payload, &req); err != nil {
			t.Errorf("request proto: %v", err)
		}
		if req.EpId != 111 || req.Cid != 222 {
			t.Errorf("req ep_id/cid mismatch: %d/%d", req.EpId, req.Cid)
		}
		w.Header().Set("Content-Type", "application/grpc")
		_, _ = w.Write(packFrame(body, true))
	}))
	defer srv.Close()

	origBase := appBase
	appBase = srv.URL
	defer func() { appBase = origBase }()

	c := NewClient(nil, "")
	c.SetAppAuth(&auth.TVAuth{AccessToken: "testtoken"})
	info, err := c.fetchViaApp(context.Background(), "111", "222")
	if err != nil {
		t.Fatalf("fetchViaApp: %v", err)
	}
	if len(info.Videos) != 1 || info.Videos[0].BaseURL != "https://cdn/v.m4s" {
		t.Fatalf("video stream mismatch: %+v", info.Videos)
	}
	if len(info.Audios) != 1 || info.Audios[0].BaseURL != "https://cdn/a.m4s" {
		t.Fatalf("audio stream mismatch: %+v", info.Audios)
	}
}

func TestFetchViaAppEmptyStreamsErrUnknown(t *testing.T) {
	reply := &appproto.PlayViewReply{VideoInfo: &appproto.VideoInfo{}}
	body, _ := proto.Marshal(reply)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(packFrame(body, false))
	}))
	defer srv.Close()
	origBase := appBase
	appBase = srv.URL
	defer func() { appBase = origBase }()

	c := NewClient(nil, "")
	c.SetAppAuth(&auth.TVAuth{AccessToken: "t"})
	_, err := c.fetchViaApp(context.Background(), "1", "2")
	if !errors.Is(err, ErrUnknownResponse) {
		t.Fatalf("got %v, want ErrUnknownResponse", err)
	}
}

func TestFetchViaAppHTTP401TokenExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	origBase := appBase
	appBase = srv.URL
	defer func() { appBase = origBase }()

	c := NewClient(nil, "")
	c.SetAppAuth(&auth.TVAuth{AccessToken: "t"})
	_, err := c.fetchViaApp(context.Background(), "1", "2")
	if !errors.Is(err, auth.ErrTVTokenExpired) {
		t.Fatalf("got %v, want ErrTVTokenExpired", err)
	}
}

// TestFetchViaAppSuccessWithFixture reads a pre-baked .bin file so the
// decoding path is exercised against bytes that were once emitted by
// proto.Marshal, guarding against silent breakage if the reply shape
// drifts from what server production sends.
func TestFetchViaAppSuccessWithFixture(t *testing.T) {
	fixturePath := "testdata/playviewreply_ok.bin"
	body, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture missing (%v); run TestFetchViaAppSuccess once to regenerate if needed", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(packFrame(body, true))
	}))
	defer srv.Close()
	origBase := appBase
	appBase = srv.URL
	defer func() { appBase = origBase }()

	c := NewClient(nil, "")
	c.SetAppAuth(&auth.TVAuth{AccessToken: "t"})
	info, err := c.fetchViaApp(context.Background(), "111", "222")
	if err != nil {
		t.Fatalf("fetchViaApp: %v", err)
	}
	if len(info.Videos) == 0 {
		t.Fatalf("expected at least one video stream")
	}
	_ = bytes.Buffer{} // silence unused import if bytes used elsewhere
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/api/ -run 'TestFetchViaApp'`
Expected: build failure — `undefined: appBase`, `(*Client).fetchViaApp`.

- [ ] **Step 4: Implement `fetchViaApp`**

Create `internal/api/app.go`:

```go
package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"

	"github.com/charleszheng44/bbdown-go/internal/api/appproto"
	"github.com/charleszheng44/bbdown-go/internal/auth"
	"google.golang.org/protobuf/proto"
)

// appBase is the base URL of Bilibili's bangumi/cheese app-API gateway.
// Exposed as a variable so tests can redirect it at an httptest server.
var appBase = "https://app.bilibili.com"

// App-API constants. These impersonate the official Bilibili Android
// app build; the server validates some of them (mobi_app, channel,
// build) when deciding what streams to return.
const (
	appMobiApp    = "android"
	appPlatform   = "android"
	appBuild      = 7320200
	appChannel    = "xiaomi_cn_tv.danmaku.bili_zm20200902"
	appBrand      = "M2012K11AC"
	appModel      = "Build/RKQ1.200826.002"
	appOSVer      = "11"
	appVersion    = "7.32.0"
	appDalvikVer  = "2.1.0"
	appCronetVer  = "1.36.1"
	appSessionID  = "dedf8669"
	appEnv        = "prod"
	appKey        = "android64"
	appAppID      = 1
	appLanguage   = "zh"
	appRegion     = "CN"
	appNetworkOid = "46007"
)

// fetchViaApp calls the bangumi/cheese PlayView app endpoint and maps
// the decoded reply into a PlayInfo with Videos/Audios populated. It
// requires c.appAuth to be non-nil; callers (fetchBangumi / fetchCourse)
// gate on that before invoking.
func (c *Client) fetchViaApp(ctx context.Context, epid, cid string) (PlayInfo, error) {
	if c.appAuth == nil {
		return PlayInfo{}, fmt.Errorf("%w: app auth not configured", ErrUnknownResponse)
	}
	token := c.appAuth.AccessToken

	epidInt, cidInt := mustAtoi64(epid), mustAtoi64(cid)
	reqMsg := &appproto.PlayViewReq{
		EpId: epidInt, Cid: cidInt,
		Qn: 127, Fnval: 4048, Fnver: 0, Fourk: true,
		Spmid: "main.ugc-video-detail.0.0", FromSpmid: "main.my-history.0.0",
		PreferCodecType: appproto.PlayViewReq_CODE265,
		ForceHost:       2,
	}
	reqBytes, err := proto.Marshal(reqMsg)
	if err != nil {
		return PlayInfo{}, fmt.Errorf("%w: marshal PlayViewReq: %v", ErrUnknownResponse, err)
	}
	framed := packFrame(reqBytes, true)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		appBase+"/bilibili.pgc.gateway.player.v2.PlayURL/PlayView",
		bytes.NewReader(framed),
	)
	if err != nil {
		return PlayInfo{}, fmt.Errorf("%w: build app request: %v", ErrUnknownResponse, err)
	}
	setAppHeaders(req.Header, token)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return PlayInfo{}, fmt.Errorf("%w: app do: %v", ErrUnknownResponse, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return PlayInfo{}, auth.ErrTVTokenExpired
	}
	if resp.StatusCode == http.StatusPreconditionFailed {
		return PlayInfo{}, ErrRateLimited
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return PlayInfo{}, fmt.Errorf("%w: http %d", ErrUnknownResponse, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return PlayInfo{}, fmt.Errorf("%w: read body: %v", ErrUnknownResponse, err)
	}
	payload, err := unpackFrame(body)
	if err != nil {
		return PlayInfo{}, fmt.Errorf("%w: unpack frame: %v", ErrUnknownResponse, err)
	}

	var reply appproto.PlayViewReply
	if err := proto.Unmarshal(payload, &reply); err != nil {
		return PlayInfo{}, fmt.Errorf("%w: unmarshal PlayViewReply: %v", ErrUnknownResponse, err)
	}
	return mapAppReply(&reply, epid, cid)
}

// setAppHeaders installs the constant User-Agent / authorization / *-bin
// headers the server expects. Binary headers are base64(protobuf).
func setAppHeaders(h http.Header, token string) {
	h.Set("User-Agent", fmt.Sprintf(
		"Dalvik/%s (Linux; U; Android %s; %s %s) %s os/android model/%s mobi_app/%s build/%d channel/%s innerVer/%d osVer/%s network/2 grpc-java-cronet/%s",
		appDalvikVer, appOSVer, appBrand, appModel, appVersion, appBrand, appMobiApp, appBuild, appChannel, appBuild, appOSVer, appCronetVer,
	))
	h.Set("Content-Type", "application/grpc")
	h.Set("grpc-encoding", "gzip")
	h.Set("grpc-accept-encoding", "identity,gzip")
	h.Set("grpc-timeout", "17996161u")
	h.Set("te", "trailers")
	h.Set("Authorization", "identify_v1 "+token)
	h.Set("x-bili-fawkes-req-bin", encodeBinHeader(&appproto.FawkesReq{Appkey: appKey, Env: appEnv, SessionId: appSessionID}))
	h.Set("x-bili-metadata-bin", encodeBinHeader(&appproto.Metadata{
		AccessKey: token, MobiApp: appMobiApp, Build: appBuild, Channel: appChannel, Platform: appPlatform,
	}))
	h.Set("x-bili-device-bin", encodeBinHeader(&appproto.Device{
		AppId: appAppID, Build: appBuild, MobiApp: appMobiApp, Platform: appPlatform,
		Channel: appChannel, Brand: appBrand, Model: appModel, Osver: appOSVer,
	}))
	h.Set("x-bili-network-bin", encodeBinHeader(&appproto.Network{
		Type: appproto.Network_WIFI, Oid: appNetworkOid,
	}))
	h.Set("x-bili-locale-bin", encodeBinHeader(&appproto.Locale{
		CLocale: &appproto.Locale_LocaleIds{Language: appLanguage, Region: appRegion},
	}))
	h.Set("x-bili-restriction-bin", "")
	h.Set("x-bili-exps-bin", "")
}

// encodeBinHeader returns base64(Marshal(m)). Marshal errors are
// impossible for the flat messages we use and are silently elided.
func encodeBinHeader(m proto.Message) string {
	b, _ := proto.Marshal(m)
	return base64.StdEncoding.EncodeToString(b)
}

// mapAppReply converts a PlayViewReply into our PlayInfo shape. Only
// DashVideo and DashAudio entries are consumed; Dolby/FLAC/SegmentVideo
// are ignored in v1.
func mapAppReply(reply *appproto.PlayViewReply, epid, cid string) (PlayInfo, error) {
	info := PlayInfo{EPID: epid, CID: cid}
	if reply.VideoInfo == nil {
		return info, fmt.Errorf("%w: app playurl missing video_info", ErrUnknownResponse)
	}
	for _, s := range reply.VideoInfo.StreamList {
		if s == nil || s.DashVideo == nil || s.StreamInfo == nil || s.DashVideo.BaseUrl == "" {
			continue
		}
		info.Videos = append(info.Videos, Stream{
			ID:         int(s.StreamInfo.Quality),
			BaseURL:    s.DashVideo.BaseUrl,
			BackupURLs: s.DashVideo.BackupUrl,
			Bandwidth:  int(s.DashVideo.Bandwidth),
			Codecs:     codecNameFromID(int(s.DashVideo.Codecid)),
			MimeType:   "video/mp4",
			Quality:    s.StreamInfo.Description,
		})
	}
	for _, a := range reply.VideoInfo.DashAudio {
		if a == nil || a.BaseUrl == "" {
			continue
		}
		info.Audios = append(info.Audios, Stream{
			ID:         int(a.Id),
			BaseURL:    a.BaseUrl,
			BackupURLs: a.BackupUrl,
			Bandwidth:  int(a.Bandwidth),
			MimeType:   "audio/mp4",
		})
	}
	if len(info.Videos) == 0 {
		return info, fmt.Errorf("%w: app playurl returned no DASH streams", ErrUnknownResponse)
	}
	return info, nil
}

// codecNameFromID turns Bilibili's codec-id enum into the DASH codec
// string the planner compares against. 7 = AVC, 12 = HEVC, 13 = AV1.
func codecNameFromID(id int) string {
	switch id {
	case 7:
		return "avc"
	case 12:
		return "hevc"
	case 13:
		return "av1"
	default:
		return ""
	}
}

// mustAtoi64 converts a decimal string to int64, returning 0 when the
// string is empty or unparseable. fetchViaApp is only reached after
// fetchBangumi/fetchCourse populated info.EPID/CID from the season
// response, so in practice these values always parse.
func mustAtoi64(s string) int64 {
	var n int64
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/api/ -run TestFetchViaApp -v`
Expected: `TestFetchViaAppSuccess`, `TestFetchViaAppEmptyStreamsErrUnknown`, and `TestFetchViaAppHTTP401TokenExpired` PASS. `TestFetchViaAppSuccessWithFixture` is `SKIPPED` until Step 6 writes the fixture.

- [ ] **Step 6: Generate the fixture**

Run from the repo root:

```bash
cat > /tmp/gen_fixture.go <<'EOF'
package main

import (
	"os"

	"github.com/charleszheng44/bbdown-go/internal/api/appproto"
	"google.golang.org/protobuf/proto"
)

func main() {
	r := &appproto.PlayViewReply{
		VideoInfo: &appproto.VideoInfo{
			Timelength: 120000,
			StreamList: []*appproto.StreamItem{
				{
					StreamInfo: &appproto.StreamInfo{Quality: 80, Description: "1080P"},
					DashVideo:  &appproto.DashVideo{BaseUrl: "https://cdn/v.m4s", Bandwidth: 2000000, Codecid: 7, Size: 10_000_000},
				},
			},
			DashAudio: []*appproto.DashItem{{Id: 30280, BaseUrl: "https://cdn/a.m4s", Bandwidth: 128000}},
		},
	}
	b, _ := proto.Marshal(r)
	if err := os.WriteFile("internal/api/testdata/playviewreply_ok.bin", b, 0o644); err != nil {
		panic(err)
	}
}
EOF
mkdir -p internal/api/testdata
go run /tmp/gen_fixture.go
rm /tmp/gen_fixture.go
```

Re-run tests: `go test ./internal/api/ -run TestFetchViaApp -v`
Expected: all four cases PASS (`TestFetchViaAppSuccessWithFixture` now runs and passes).

- [ ] **Step 7: Commit**

```bash
gofmt -w .
git add internal/api/app.go internal/api/app_test.go internal/api/client.go internal/api/testdata/playviewreply_ok.bin
git commit -m "feat(api): app-API PlayView client (fetchViaApp)"
```

---

### Task 8: Auto-fallback integration in `fetchBangumi` and `fetchCourse`

**Files:**
- Modify: `internal/api/playinfo.go`
- Modify: `internal/api/api_test.go`
- Modify: `cmd/bbdown/download.go`
- Modify: `cmd/bbdown/parts_list.go`

- [ ] **Step 1: Write the failing integration test**

Append to `internal/api/api_test.go`:

```go
// TestFetchPlayInfo_CoursePreviewFallsBackToApp verifies that a pugv
// preview response (is_preview=1) triggers a PlayView call when
// Client.SetAppAuth has been called, and that the app-side streams are
// returned.
func TestFetchPlayInfo_CoursePreviewFallsBackToApp(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/pugv/view/web/season", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeOK(map[string]any{
			"title":    "Course",
			"episodes": []map[string]any{{"id": 77, "cid": 700, "aid": 8000, "title": "Lesson"}},
		}))
	})
	mux.HandleFunc("/pugv/player/web/playurl", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeOK(map[string]any{
			"is_preview": 1, "type": "MP4",
			"durl": []map[string]any{{"url": "https://preview/clip.mp4"}},
		}))
	})
	mux.HandleFunc("/bilibili.pgc.gateway.player.v2.PlayURL/PlayView", func(w http.ResponseWriter, _ *http.Request) {
		reply := &appproto.PlayViewReply{
			VideoInfo: &appproto.VideoInfo{
				StreamList: []*appproto.StreamItem{{
					StreamInfo: &appproto.StreamInfo{Quality: 80, Description: "1080P"},
					DashVideo:  &appproto.DashVideo{BaseUrl: "https://app-cdn/v.m4s", Bandwidth: 2_000_000, Codecid: 7, Size: 1},
				}},
				DashAudio: []*appproto.DashItem{{Id: 30280, BaseUrl: "https://app-cdn/a.m4s", Bandwidth: 128_000}},
			},
		}
		b, _ := proto.Marshal(reply)
		w.Header().Set("Content-Type", "application/grpc")
		w.Write(packFrame(b, true))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	withAllBases(t, srv.URL, func() {
		origApp := appBase
		appBase = srv.URL
		defer func() { appBase = origApp }()

		c := NewClient(nil, "")
		c.SetAppAuth(&auth.TVAuth{AccessToken: "t"})
		info, err := c.FetchPlayInfo(context.Background(), parser.Target{Kind: parser.KindCourse, EPID: "77"}, 1)
		if err != nil {
			t.Fatalf("FetchPlayInfo: %v", err)
		}
		if len(info.Videos) != 1 || info.Videos[0].BaseURL != "https://app-cdn/v.m4s" {
			t.Fatalf("expected app-side stream, got %+v", info.Videos)
		}
	})
}
```

Add to the test file's imports (if not already present):
```go
import (
	"github.com/charleszheng44/bbdown-go/internal/api/appproto"
	"github.com/charleszheng44/bbdown-go/internal/auth"
	"google.golang.org/protobuf/proto"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestFetchPlayInfo_CoursePreviewFallsBackToApp -v`
Expected: FAIL — `FetchPlayInfo` returns `ErrContentLocked` unchanged (no fallback yet).

- [ ] **Step 3: Wire the fallback into `fetchBangumi`**

In `internal/api/playinfo.go`, locate the `fetchBangumi` function. Replace its tail:

```go
	if err := decodePgcPlayurl(playRaw, &info); err != nil {
		return PlayInfo{}, err
	}
	return info, nil
}
```

with:

```go
	if err := decodePgcPlayurl(playRaw, &info); err != nil {
		if errors.Is(err, ErrContentLocked) && c.appAuth != nil {
			appInfo, appErr := c.fetchViaApp(ctx, info.EPID, info.CID)
			if appErr == nil {
				// Preserve the title/parts/subtitles we already resolved
				// from the season endpoint; only the streams come from
				// the app reply.
				info.Videos = appInfo.Videos
				info.Audios = appInfo.Audios
				return info, nil
			}
			// Fall through: if the app call also failed, propagate the
			// original web error so users see the familiar message.
		}
		return PlayInfo{}, err
	}
	return info, nil
}
```

Add `"errors"` to the import block if it is not already there.

- [ ] **Step 4: Wire the same fallback into `fetchCourse`**

Apply the identical transformation to the `fetchCourse` function's tail — replace:

```go
	if err := decodePgcPlayurl(playRaw, &info); err != nil {
		return PlayInfo{}, err
	}
	return info, nil
}
```

with the same block as Step 3 (copy-paste — keep it literal; the two call sites intentionally share behaviour).

- [ ] **Step 5: Run the integration test**

Run: `go test ./internal/api/ -run TestFetchPlayInfo_CoursePreviewFallsBackToApp -v`
Expected: PASS.

- [ ] **Step 6: Wire `SetAppAuth` from the CLI**

In `cmd/bbdown/download.go`, find the `runDownload` function — specifically the line `client := api.NewClient(cookies.AsJar(), "")`. After that line, add:

```go
	if cookies.TV != nil {
		client.SetAppAuth(cookies.TV)
	}
```

In `cmd/bbdown/parts_list.go`, find `runParts` — specifically the `client := api.NewClient(cookies.AsJar(), "")` line — and add the same two lines directly after it.

- [ ] **Step 7: Full test suite**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 8: Commit**

```bash
gofmt -w .
git add internal/api/playinfo.go internal/api/api_test.go cmd/bbdown/download.go cmd/bbdown/parts_list.go
git commit -m "feat(api): auto-fallback to app-API PlayView on pgc/pugv preview"
```

---

### Task 9: Error-message hint and README

**Files:**
- Modify: `cmd/bbdown/errors.go`
- Modify: `cmd/bbdown/errors_test.go`
- Modify: `README.md`

- [ ] **Step 1: Write the failing test**

In `cmd/bbdown/errors_test.go`, locate the existing `formatError` table-driven test (it iterates over `errors.Is` cases). Append one new case (keep every other existing field the same):

```go
{
	name:     "content_locked_without_tv_auth",
	err:      api.ErrContentLocked,
	tvAuthed: false,
	contains: "bbdown login --tv",
},
{
	name:     "content_locked_with_tv_auth",
	err:      api.ErrContentLocked,
	tvAuthed: true,
	contains: "requires a purchase or is region-locked",
},
```

This new `tvAuthed` column requires a corresponding field on the test-case struct. Add it:

```go
type formatErrorCase struct {
	name     string
	err      error
	inDebug  bool
	tvAuthed bool
	contains string
}
```

Inside the loop, before calling `formatError`, mirror `tvAuthed` into the package-level state:

```go
prevAppAuthed := appAuthConfigured
appAuthConfigured = tt.tvAuthed
defer func() { appAuthConfigured = prevAppAuthed }()
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/bbdown/ -run TestFormatError -v`
Expected: build failure — `undefined: appAuthConfigured` — or test failure if the variable exists but the hint is not yet emitted.

- [ ] **Step 3: Implement the hint**

In `cmd/bbdown/errors.go`, add a package-level `appAuthConfigured bool` near `debugMode`:

```go
// appAuthConfigured mirrors whether a TV access token is currently
// attached to api.Client. Set by newRootCmd's RunE (and the parts
// subcommand's) after SetAppAuth; read by formatError to decide
// whether to append a login --tv hint on ErrContentLocked.
var appAuthConfigured bool
```

Update the `errors.Is(err, api.ErrContentLocked)` branch of `formatError`:

```go
case errors.Is(err, api.ErrContentLocked):
	msg := "This content requires a purchase or is region-locked."
	if !appAuthConfigured {
		msg += " Run `bbdown login --tv` to enable the app-API fallback for purchased bangumi/cheese."
	}
	return msg
```

Also add a new branch above it for the expired-token case:

```go
case errors.Is(err, auth.ErrTVTokenExpired):
	return "TV access token expired. Re-run `bbdown login --tv`."
```

Add `"github.com/charleszheng44/bbdown-go/internal/auth"` to the import block if it's not already there.

- [ ] **Step 4: Set `appAuthConfigured` where we set the client's app auth**

In `cmd/bbdown/download.go`, update the block from Task 8 Step 6 so it also mirrors into `appAuthConfigured`:

```go
	if cookies.TV != nil {
		client.SetAppAuth(cookies.TV)
		appAuthConfigured = true
	}
```

Apply the identical addition in `cmd/bbdown/parts_list.go`.

- [ ] **Step 5: Run the tests**

Run: `go test ./cmd/bbdown/ -run TestFormatError -v`
Expected: both new subcases PASS; existing cases continue to PASS.

- [ ] **Step 6: Update README**

In `README.md`, in the Command Reference table (lines ~55–60), replace the `bbdown login` row with two rows so both variants are documented:

```
| Command                | Purpose                                                          |
|------------------------|------------------------------------------------------------------|
| `bbdown login`         | Start QR-code login and persist cookies to disk.                 |
| `bbdown login --tv`    | One-time TV-QR scan, enables app-API fallback for paid content.  |
| `bbdown logout`        | Delete the stored cookie file (web + TV).                        |
| `bbdown parts <url>`   | List page numbers, durations, and titles for a multi-part item.  |
| `bbdown <url>`         | Download the given Bilibili URL (or ID).                         |
```

Also update the prose above the table from "four top-level forms" to "five top-level forms".

In the "Login and Cookies" section, after the paragraph that describes where cookies are written, add:

```markdown
For purchased bangumi or cheese (course) content whose web playurl only
returns a preview clip, run `bbdown login --tv` once to scan a second QR
code from your phone's Bilibili app. The resulting access token is saved
alongside the web cookies and used automatically whenever the web path
would otherwise return "This content requires a purchase or is
region-locked". It is unrelated to mobile-app push notifications — it is
purely an authentication mechanism for the app-API download endpoint.
```

- [ ] **Step 7: Full test suite and smoke build**

Run: `go test ./... && go vet ./... && go build -o /tmp/bbdown ./cmd/bbdown`
Expected: all green, binary builds.

Run: `/tmp/bbdown login --help`
Expected: help output shows `--tv`.

- [ ] **Step 8: Commit**

```bash
gofmt -w .
git add cmd/bbdown/errors.go cmd/bbdown/errors_test.go cmd/bbdown/download.go cmd/bbdown/parts_list.go README.md
git commit -m "docs+cmd: login --tv error hints and README Command Reference update"
```

---

## Self-Review

**Spec coverage:**

| Spec section | Covered by |
|---|---|
| §3 in-scope `bbdown login --tv` | Task 4 |
| §3 auto-fallback for bangumi + cheese | Task 8 |
| §3 app-API client | Tasks 5, 6, 7 |
| §3 committed .pb.go | Task 5 |
| §3 formatError hint | Task 9 |
| §3 `bbdown logout` wipes TV | Already satisfied by existing `logout` (deletes whole cookies file); no code change |
| §5.1 package layout | Tasks 1–9 align to the spec's file table |
| §5.5 PlayView request shape | Task 7 `fetchViaApp` + `setAppHeaders` |
| §5.5 frame layout | Task 6 `packFrame`/`unpackFrame` |
| §5.6 reply mapping | Task 7 `mapAppReply` |
| §5.7 TV login wire details | Tasks 2 and 3 |
| §6 error handling (ErrTVTokenExpired) | Task 1 (sentinel), Task 7 (HTTP 401 branch), Task 9 (formatError branch) |
| §7 testing | Each task ends with unit tests; Task 8 adds the integration test |
| §9 future work (refresh rotation) | Explicitly out of scope; no task |
| §10 risks (proto-check CI) | Task 5 adds the `proto-check` Make target |

**Placeholder scan:** Every code block is self-contained. No TBDs, TODOs, or "handle edge cases" hand-waves. The one "inspect the Makefile for the existing aggregator" line in Task 5 Step 4 is explicit about what the agent should do, not hand-waving.

**Type consistency:**
- `auth.TVAuth{AccessToken, RefreshToken, MID, ExpiresAt}` defined in Task 1, referenced in Tasks 3/4/7/8/9 — same field names throughout.
- `auth.ErrTVTokenExpired` defined in Task 1, checked in Task 7 (HTTP 401 branch), surfaced in Task 9 (formatError branch).
- `api.Client.SetAppAuth(*auth.TVAuth)` defined in Task 7, called in Tasks 7/8/9.
- `api.Client.appAuth` (unexported) defined in Task 7, read in Tasks 7/8.
- `packFrame(payload, compress bool) []byte` + `unpackFrame(frame) ([]byte, error)` defined in Task 6, used in Task 7 (tests and `fetchViaApp`) and Task 8 (integration test).
- `appproto.PlayViewReq` / `PlayViewReply` / `VideoInfo` / `StreamItem` / `StreamInfo` / `DashVideo` / `DashItem` / `Device` / `Metadata` / `Locale` / `Locale_LocaleIds` / `Network` / `Network_WIFI` / `FawkesReq` defined by `protoc` output in Task 5, referenced in Tasks 7 and 8 under those exact names.
- `fetchViaApp(ctx, epid, cid string) (PlayInfo, error)` defined in Task 7, called in Task 8 with identical signature.
- `appBase` package var defined in Task 7, swapped by tests in Tasks 7 and 8.
- `appAuthConfigured` package var defined in Task 9, written by the blocks added in Task 8 Step 6 and Task 9 Step 4 — the Step 4 update is what makes the variable set; Task 8's Step 6 is rewritten in Task 9's Step 4 to mirror it. Both references use the same name.

All signatures line up.
