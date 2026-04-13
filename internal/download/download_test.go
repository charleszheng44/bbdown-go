package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// deterministicPayload returns a reproducible byte slice of length n.
func deterministicPayload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 31)
	}
	return b
}

func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hashFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	return hashBytes(data)
}

// newRangeServer serves a fixed payload with HEAD support, Accept-Ranges,
// and Range-request handling.
func newRangeServer(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if rng := r.Header.Get("Range"); rng != "" {
			// "bytes=START-END"
			spec := strings.TrimPrefix(rng, "bytes=")
			parts := strings.SplitN(spec, "-", 2)
			if len(parts) != 2 {
				http.Error(w, "bad range", http.StatusBadRequest)
				return
			}
			start, err1 := strconv.ParseInt(parts[0], 10, 64)
			end, err2 := strconv.ParseInt(parts[1], 10, 64)
			if err1 != nil || err2 != nil || start < 0 || end >= int64(len(payload)) || start > end {
				http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[start : end+1])
			return
		}
		// Full body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
}

// newNoRangeServer serves payload but advertises no range support.
func newNoRangeServer(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately NO Accept-Ranges.
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
}

func TestFetch_RangeHappyPath(t *testing.T) {
	payload := deterministicPayload(4*1024*1024 + 123) // 4 MiB + tail
	srv := newRangeServer(t, payload)
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "file.bin")

	var last atomic.Int64
	var total atomic.Int64
	opts := Options{
		Threads: 4,
		Timeout: 5 * time.Second,
		OnProgress: func(got, tot int64) {
			last.Store(got)
			total.Store(tot)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Fetch(ctx, srv.Client(), srv.URL, dst, opts); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if got := hashFile(t, dst); got != hashBytes(payload) {
		t.Fatalf("hash mismatch")
	}
	if last.Load() != int64(len(payload)) {
		t.Fatalf("final progress = %d, want %d", last.Load(), len(payload))
	}
	if total.Load() != int64(len(payload)) {
		t.Fatalf("progress total = %d, want %d", total.Load(), len(payload))
	}
}

func TestFetch_NoRangeFallback(t *testing.T) {
	payload := deterministicPayload(2 * 1024 * 1024)
	srv := newNoRangeServer(t, payload)
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "file.bin")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Fetch(ctx, srv.Client(), srv.URL, dst, Options{Threads: 4}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if got := hashFile(t, dst); got != hashBytes(payload) {
		t.Fatalf("hash mismatch")
	}
}

func TestFetch_RetryOn503ThenSucceed(t *testing.T) {
	payload := deterministicPayload(4096) // small -> single-stream path
	var getCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			return
		}
		n := getCalls.Add(1)
		if n == 1 {
			http.Error(w, "try later", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "file.bin")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := Fetch(ctx, srv.Client(), srv.URL, dst, Options{Threads: 1}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if hashFile(t, dst) != hashBytes(payload) {
		t.Fatalf("hash mismatch")
	}
	if getCalls.Load() < 2 {
		t.Fatalf("expected retry, got %d GETs", getCalls.Load())
	}
}

func TestFetch_CancelMidDownload(t *testing.T) {
	payload := deterministicPayload(8 * 1024 * 1024) // 8 MiB, triggers parallel path

	// Server that supports ranges but streams slowly so we can cancel.
	var once sync.Once
	cancelTriggered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			return
		}
		start, end := int64(0), int64(len(payload))-1
		if rng := r.Header.Get("Range"); rng != "" {
			spec := strings.TrimPrefix(rng, "bytes=")
			parts := strings.SplitN(spec, "-", 2)
			start, _ = strconv.ParseInt(parts[0], 10, 64)
			end, _ = strconv.ParseInt(parts[1], 10, 64)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
		}

		flusher, _ := w.(http.Flusher)
		chunk := int64(32 * 1024)
		for off := start; off <= end; off += chunk {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			hi := off + chunk - 1
			if hi > end {
				hi = end
			}
			if _, err := w.Write(payload[off : hi+1]); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			// Signal that data has started flowing, then throttle.
			once.Do(func() { close(cancelTriggered) })
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "file.bin")

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel shortly after the first byte flows.
	go func() {
		select {
		case <-cancelTriggered:
		case <-time.After(2 * time.Second):
		}
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := Fetch(ctx, srv.Client(), srv.URL, dst, Options{Threads: 4, Timeout: 10 * time.Second})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("expected ErrCanceled, got %v", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatalf("partial file should be removed, stat err = %v", statErr)
	}
}

func TestFetch_4xxIsNotRetried(t *testing.T) {
	var headCalls, getCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			headCalls.Add(1)
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", "10")
			w.WriteHeader(http.StatusOK)
			return
		}
		getCalls.Add(1)
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "file.bin")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := Fetch(ctx, srv.Client(), srv.URL, dst, Options{Threads: 1})
	if !errors.Is(err, ErrServerError) {
		t.Fatalf("expected ErrServerError, got %v", err)
	}
	if getCalls.Load() != 1 {
		t.Fatalf("expected single GET, got %d", getCalls.Load())
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatalf("failed download should not leave file, stat err = %v", statErr)
	}
}

// Sanity: ensure we read the body to keep the server happy in unusual paths.
var _ = io.Discard
