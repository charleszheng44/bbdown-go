// Package download provides a generic, Bilibili-agnostic HTTP downloader
// that fetches a URL into a file using parallel HTTP range requests when
// the server supports them. It retries transient failures with exponential
// backoff, reports progress, and honors context cancellation.
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Sentinel errors returned by Fetch.
var (
	// ErrServerError indicates the server returned an unrecoverable status
	// (non-2xx, non-retryable, or still failing after all retry attempts).
	ErrServerError = errors.New("download: server error")

	// ErrCanceled indicates that the context was canceled before the
	// download completed. The partial destination file is removed before
	// this error is returned.
	ErrCanceled = errors.New("download: canceled")

	// ErrPartialDownload indicates that the transfer terminated before
	// the expected number of bytes were received and the error is not
	// one of the other sentinels.
	ErrPartialDownload = errors.New("download: partial download")
)

// Options controls the behavior of Fetch. A zero Options value is valid
// and uses sensible defaults (see field comments).
type Options struct {
	// Threads is the number of parallel range-request workers to use when
	// the server supports byte ranges. Values < 1 are treated as the
	// default (8).
	Threads int

	// Timeout is the per-HTTP-request timeout (applied via a derived
	// context). Values <= 0 are treated as the default (60s).
	Timeout time.Duration

	// UserAgent, when non-empty, is sent as the User-Agent header on every
	// request.
	UserAgent string

	// Headers are extra headers (e.g. Referer, Cookie) copied onto every
	// request. The downloader may override Range and User-Agent.
	Headers http.Header

	// OnProgress, if non-nil, is invoked periodically with the total
	// number of bytes downloaded so far and the total size in bytes
	// (or -1 if unknown). Called from multiple goroutines; it must be
	// safe for concurrent use.
	OnProgress func(downloaded, total int64)
}

// Defaults and tuning knobs.
const (
	defaultThreads      = 8
	defaultTimeout      = 60 * time.Second
	minParallelSize     = 1 << 20 // 1 MiB
	maxRetries          = 3
	retryBackoffBase    = 500 * time.Millisecond
	retryBackoffCap     = 8 * time.Second
	progressReportEvery = 64 * 1024 // bytes per progress callback in streaming copy
)

// Fetch downloads the content at url into dst.
//
// If the server advertises byte-range support (via Accept-Ranges: bytes)
// and the Content-Length exceeds 1 MiB, Fetch issues opts.Threads parallel
// range requests, each writing its slice of the file with File.WriteAt
// into a pre-allocated destination. Otherwise, Fetch falls back to a
// single streaming io.Copy.
//
// On success, dst contains exactly the bytes the server returned. On
// error, dst is removed. If ctx is canceled mid-download, Fetch returns
// ErrCanceled.
func Fetch(ctx context.Context, client *http.Client, url, dst string, opts Options) error {
	if client == nil {
		client = http.DefaultClient
	}
	if opts.Threads < 1 {
		opts.Threads = defaultThreads
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}

	// Probe with HEAD to learn size and range support.
	size, rangesSupported, err := probe(ctx, client, url, opts)
	if err != nil {
		return err
	}

	useParallel := rangesSupported && size > minParallelSize && opts.Threads > 1

	// Create/truncate destination.
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	// Ensure we remove the file on any failure path below.
	success := false
	defer func() {
		_ = f.Close()
		if !success {
			_ = os.Remove(dst)
		}
	}()

	var downloaded atomic.Int64
	reportProgress := func() {
		if opts.OnProgress != nil {
			total := size
			if !rangesSupported && size <= 0 {
				total = -1
			}
			opts.OnProgress(downloaded.Load(), total)
		}
	}

	if useParallel {
		if err := f.Truncate(size); err != nil {
			return err
		}
		if err := parallelDownload(ctx, client, url, f, size, opts, &downloaded, reportProgress); err != nil {
			return mapErr(ctx, err)
		}
	} else {
		if err := singleStream(ctx, client, url, f, opts, &downloaded, reportProgress); err != nil {
			return mapErr(ctx, err)
		}
		// For unknown-size streams, finalize progress against actual bytes.
		if size <= 0 {
			size = downloaded.Load()
		} else if downloaded.Load() != size {
			return ErrPartialDownload
		}
	}

	// Final progress tick.
	reportProgress()
	success = true
	return nil
}

// probe performs a HEAD request and reads Content-Length / Accept-Ranges.
// A probe failure is retried per the standard policy.
func probe(ctx context.Context, client *http.Client, url string, opts Options) (size int64, ranges bool, err error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if werr := waitBackoff(ctx, attempt); werr != nil {
				return 0, false, werr
			}
		}

		reqCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, url, nil)
		if err != nil {
			cancel()
			return 0, false, err
		}
		applyHeaders(req, opts)

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return 0, false, ctx.Err()
			}
			lastErr = err
			continue
		}

		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			_ = resp.Body.Close()
			cancel()
			lastErr = fmt.Errorf("%w: HEAD %s: status %d", ErrServerError, url, resp.StatusCode)
			continue
		}
		if resp.StatusCode >= 400 {
			status := resp.StatusCode
			_ = resp.Body.Close()
			cancel()
			return 0, false, fmt.Errorf("%w: HEAD %s: status %d", ErrServerError, url, status)
		}

		ranges = strings.EqualFold(strings.TrimSpace(resp.Header.Get("Accept-Ranges")), "bytes")
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			if n, perr := strconv.ParseInt(cl, 10, 64); perr == nil {
				size = n
			}
		}
		_ = resp.Body.Close()
		cancel()
		return size, ranges, nil
	}
	if lastErr == nil {
		lastErr = ErrServerError
	}
	return 0, false, fmt.Errorf("%w: HEAD %s: %v", ErrServerError, url, lastErr)
}

// singleStream performs a plain GET and copies the body into f.
func singleStream(ctx context.Context, client *http.Client, url string, f *os.File, opts Options, counter *atomic.Int64, report func()) error {
	return withRetry(ctx, func(attempt int) error {
		// Seek back on retry so we overwrite any earlier partial bytes.
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if err := f.Truncate(0); err != nil {
			return err
		}
		counter.Store(0)
		report()

		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		applyHeaders(req, opts)

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			return fmt.Errorf("%w: GET %s: status %d", ErrServerError, url, resp.StatusCode)
		}
		if resp.StatusCode >= 400 {
			return backoffHalt(fmt.Errorf("%w: GET %s: status %d", ErrServerError, url, resp.StatusCode))
		}

		_, err = copyWithProgress(reqCtx, f, resp.Body, counter, report)
		return err
	})
}

// parallelDownload issues Threads range requests concurrently.
func parallelDownload(ctx context.Context, client *http.Client, url string, f *os.File, size int64, opts Options, counter *atomic.Int64, report func()) error {
	workers := int64(opts.Threads)
	if workers > size {
		workers = size
	}
	if workers < 1 {
		workers = 1
	}
	chunk := size / workers
	remainder := size % workers

	wg := sync.WaitGroup{}
	errCh := make(chan error, workers)
	grpCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var offset int64
	for i := int64(0); i < workers; i++ {
		start := offset
		length := chunk
		if i == workers-1 {
			length += remainder
		}
		end := start + length - 1
		offset = end + 1

		wg.Add(1)
		go func(start, end int64) {
			defer wg.Done()
			if err := fetchRange(grpCtx, client, url, f, start, end, opts, counter, report); err != nil {
				errCh <- err
				cancel()
			}
		}(start, end)
	}

	wg.Wait()
	close(errCh)
	if err, ok := <-errCh; ok {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

// fetchRange downloads [start,end] of url into f at offset start with
// the standard retry policy.
func fetchRange(ctx context.Context, client *http.Client, url string, f *os.File, start, end int64, opts Options, counter *atomic.Int64, report func()) error {
	return withRetry(ctx, func(attempt int) error {
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		applyHeaders(req, opts)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			return fmt.Errorf("%w: GET %s: status %d", ErrServerError, url, resp.StatusCode)
		}
		// Accept 206 (Partial Content) and 200 (full-body fallback for tiny files).
		if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
			return backoffHalt(fmt.Errorf("%w: GET %s: status %d", ErrServerError, url, resp.StatusCode))
		}

		written, err := copyRangeWithProgress(reqCtx, f, resp.Body, start, counter, report)
		if err != nil {
			return err
		}
		expect := end - start + 1
		if written != expect {
			return fmt.Errorf("%w: wrote %d of %d bytes for range %d-%d", ErrPartialDownload, written, expect, start, end)
		}
		return nil
	})
}

// copyWithProgress copies src to dst tracking progress in counter and
// calling report() periodically.
func copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, counter *atomic.Int64, report func()) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	sinceReport := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			wn, werr := dst.Write(buf[:n])
			total += int64(wn)
			counter.Add(int64(wn))
			sinceReport += int64(wn)
			if sinceReport >= progressReportEvery {
				report()
				sinceReport = 0
			}
			if werr != nil {
				return total, werr
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}

// copyRangeWithProgress is like copyWithProgress but writes at a fixed
// file offset using WriteAt.
func copyRangeWithProgress(ctx context.Context, f *os.File, src io.Reader, offset int64, counter *atomic.Int64, report func()) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	sinceReport := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			wn, werr := f.WriteAt(buf[:n], offset+total)
			total += int64(wn)
			counter.Add(int64(wn))
			sinceReport += int64(wn)
			if sinceReport >= progressReportEvery {
				report()
				sinceReport = 0
			}
			if werr != nil {
				return total, werr
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}

// haltError wraps an error that should NOT be retried.
type haltError struct{ err error }

func (h *haltError) Error() string { return h.err.Error() }
func (h *haltError) Unwrap() error { return h.err }

func backoffHalt(err error) error { return &haltError{err: err} }

// withRetry runs op up to maxRetries+1 times, retrying on transient errors
// (anything not wrapped with backoffHalt, not a context error, and not
// already a permanent sentinel).
func withRetry(ctx context.Context, op func(attempt int) error) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if err := waitBackoff(ctx, attempt); err != nil {
				return err
			}
		}
		err := op(attempt)
		if err == nil {
			return nil
		}
		var halt *haltError
		if errors.As(err, &halt) {
			return halt.err
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// waitBackoff sleeps for an exponentially-growing duration, capped at
// retryBackoffCap, with a small random jitter. Returns ctx.Err() if the
// context is canceled during the wait.
func waitBackoff(ctx context.Context, attempt int) error {
	d := retryBackoffBase << (attempt - 1)
	if d <= 0 || d > retryBackoffCap {
		d = retryBackoffCap
	}
	// Full jitter in [d/2, d].
	jitter := time.Duration(rand.Int63n(int64(d/2) + 1))
	d = d/2 + jitter
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// mapErr normalizes terminal errors to the exported sentinels.
func mapErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrCanceled
	}
	return err
}

// applyHeaders copies Options headers onto req and sets a User-Agent
// when configured.
func applyHeaders(req *http.Request, opts Options) {
	for k, vs := range opts.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if opts.UserAgent != "" {
		req.Header.Set("User-Agent", opts.UserAgent)
	}
}
