package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakePassport spins up an httptest server that serves /generate and /poll,
// redirects passportBase to it, and tightens pollInterval for fast tests.
// It returns a cleanup function that restores globals.
type fakePassport struct {
	srv          *httptest.Server
	pollCalls    atomic.Int32
	pollResponse func(call int) string // returns JSON body for the Nth call (1-indexed)
	generateBody string
	logBuf       *bytes.Buffer
	qrBuf        *bytes.Buffer
}

func newFakePassport(t *testing.T, generateBody string, pollResp func(call int) string) *fakePassport {
	t.Helper()

	f := &fakePassport{
		generateBody: generateBody,
		pollResponse: pollResp,
		logBuf:       &bytes.Buffer{},
		qrBuf:        &bytes.Buffer{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/x/passport-login/web/qrcode/generate", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("source"); got != "main-fe-header" {
			t.Errorf("generate: want source=main-fe-header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, f.generateBody)
	})
	mux.HandleFunc("/x/passport-login/web/qrcode/poll", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("qrcode_key"); got != "key-xyz" {
			t.Errorf("poll: want qrcode_key=key-xyz, got %q", got)
		}
		if got := r.URL.Query().Get("source"); got != "main-fe-header" {
			t.Errorf("poll: want source=main-fe-header, got %q", got)
		}
		n := int(f.pollCalls.Add(1))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, f.pollResponse(n))
	})
	// Default finger/spi handler returns a deterministic buvid3 so
	// LoginQR tests can assert the field round-trips. Individual tests
	// may override behavior by redefining passportBase/apiBase after
	// calling newFakePassport.
	mux.HandleFunc("/x/frontend/finger/spi", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{"b_3":"bv3-test","b_4":"bv4-test"}}`)
	})

	f.srv = httptest.NewServer(mux)

	// Swap globals.
	origPassport := passportBase
	origAPI := apiBase
	origInterval := pollInterval
	origQR := qrWriter
	origLog := logWriter
	passportBase = f.srv.URL
	apiBase = f.srv.URL
	pollInterval = 1 * time.Millisecond
	qrWriter = f.qrBuf
	logWriter = f.logBuf

	t.Cleanup(func() {
		f.srv.Close()
		passportBase = origPassport
		apiBase = origAPI
		pollInterval = origInterval
		qrWriter = origQR
		logWriter = origLog
	})

	return f
}

const okGenerate = `{"code":0,"data":{"url":"https://passport.bilibili.com/qr?qrcode_key=key-xyz","qrcode_key":"key-xyz"}}`

func TestLoginQRSuccess(t *testing.T) {
	successURL := "https://passport.bilibili.com/?SESSDATA=s-val&bili_jct=j-val&DedeUserID=42&DedeUserID__ckMd5=m-val&Expires=0"

	poll := func(call int) string {
		switch call {
		case 1:
			return `{"data":{"code":86101}}` // waiting for scan
		case 2:
			return `{"data":{"code":86090}}` // scanned, waiting confirm
		case 3:
			return `{"data":{"code":86090}}` // still waiting confirm (ensure logged once)
		default:
			return fmt.Sprintf(`{"data":{"code":0,"url":%q}}`, successURL)
		}
	}
	f := newFakePassport(t, okGenerate, poll)

	got, err := LoginQR(context.Background(), f.srv.Client())
	if err != nil {
		t.Fatalf("LoginQR: %v", err)
	}
	want := Cookies{SESSDATA: "s-val", BiliJCT: "j-val", DedeUserID: "42", DedeUserIDCkMd5: "m-val", Buvid3: "bv3-test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want %+v, got %+v", want, got)
	}
	if f.qrBuf.Len() == 0 {
		t.Fatalf("expected QR code to have been written to qrWriter")
	}
	logOut := f.logBuf.String()
	if !strings.Contains(logOut, "QR code scanned") {
		t.Fatalf("expected 86090 log message, got %q", logOut)
	}
	// Ensure we only logged the confirm message once even though 86090 was
	// returned twice.
	if strings.Count(logOut, "QR code scanned") != 1 {
		t.Fatalf("expected 86090 message exactly once, got %q", logOut)
	}
}

func TestLoginQRExpired(t *testing.T) {
	poll := func(call int) string {
		switch call {
		case 1:
			return `{"data":{"code":86101}}`
		default:
			return `{"data":{"code":86038}}`
		}
	}
	f := newFakePassport(t, okGenerate, poll)

	_, err := LoginQR(context.Background(), f.srv.Client())
	if !errors.Is(err, ErrQRExpired) {
		t.Fatalf("want ErrQRExpired, got %v", err)
	}
}

func TestLoginQRContextCanceled(t *testing.T) {
	// Always return "waiting for scan" so the loop spins until ctx is canceled.
	poll := func(call int) string { return `{"data":{"code":86101}}` }
	f := newFakePassport(t, okGenerate, poll)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := LoginQR(ctx, f.srv.Client())
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("want context error, got %v", err)
	}
}

func TestLoginQRUnexpectedCode(t *testing.T) {
	poll := func(call int) string { return `{"data":{"code":99999}}` }
	f := newFakePassport(t, okGenerate, poll)

	_, err := LoginQR(context.Background(), f.srv.Client())
	if err == nil {
		t.Fatalf("expected error for unknown code")
	}
	if !strings.Contains(err.Error(), "99999") {
		t.Fatalf("error should mention code 99999, got %v", err)
	}
}

func TestLoginQRGenerateHTTPError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/x/passport-login/web/qrcode/generate", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origBase := passportBase
	passportBase = srv.URL
	t.Cleanup(func() { passportBase = origBase })

	_, err := LoginQR(context.Background(), srv.Client())
	if err == nil {
		t.Fatalf("expected error from failed generate call")
	}
}

// TestLoginQRPreservesSESSDATAEncoding guards against a regression where
// the passport-delivered SESSDATA (which contains %2C and %2A) gets
// percent-decoded during extraction. Sending the decoded form back to
// Bilibili causes cheese/bangumi playurl to downgrade to preview mode.
func TestLoginQRPreservesSESSDATAEncoding(t *testing.T) {
	successURL := "https://passport.bilibili.com/?SESSDATA=abc%2C123%2Cdef%2A41&bili_jct=j&DedeUserID=1&DedeUserID__ckMd5=m&Expires=0"
	poll := func(call int) string {
		return fmt.Sprintf(`{"data":{"code":0,"url":%q}}`, successURL)
	}
	f := newFakePassport(t, okGenerate, poll)

	got, err := LoginQR(context.Background(), f.srv.Client())
	if err != nil {
		t.Fatalf("LoginQR: %v", err)
	}
	if got.SESSDATA != "abc%2C123%2Cdef%2A41" {
		t.Errorf("SESSDATA was decoded; got %q, want %q", got.SESSDATA, "abc%2C123%2Cdef%2A41")
	}
	if got.Buvid3 != "bv3-test" {
		t.Errorf("Buvid3 = %q, want %q", got.Buvid3, "bv3-test")
	}
}

func TestLoginQRSuccessMissingCookie(t *testing.T) {
	// Success URL is missing bili_jct.
	successURL := "https://passport.bilibili.com/?SESSDATA=s&DedeUserID=1&DedeUserID__ckMd5=m"
	poll := func(call int) string {
		return fmt.Sprintf(`{"data":{"code":0,"url":%q}}`, successURL)
	}
	f := newFakePassport(t, okGenerate, poll)

	_, err := LoginQR(context.Background(), f.srv.Client())
	if err == nil {
		t.Fatalf("expected error for incomplete success url")
	}
}
