package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Endpoint bases. Exposed as package-level variables (not constants) so tests
// can point the client at an httptest.Server. Production code never mutates
// these after init.
var (
	apiBase  = "https://api.bilibili.com"
	pgcBase  = "https://api.bilibili.com"
	pugvBase = "https://api.bilibili.com"
)

// DefaultUserAgent is used when the caller passes an empty UA to NewClient.
// Bilibili's web endpoints reject empty or obviously automated User-Agent
// strings, so we mimic a recent desktop Chrome.
const DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

// Sentinel errors surfaced to callers. Use errors.Is for comparisons.
var (
	// ErrContentLocked indicates the item requires a purchase or is region-
	// or age-locked for the current session (Bilibili codes -404, 62002,
	// 87008 and similar).
	ErrContentLocked = errors.New("api: content locked or not purchased")
	// ErrRateLimited indicates Bilibili returned HTTP 412 or code 412,
	// which in practice means anti-scraping has engaged.
	ErrRateLimited = errors.New("api: rate limited by bilibili")
	// ErrUnknownResponse is returned for every other non-zero code. The
	// wrapped error carries the message Bilibili supplied, if any.
	ErrUnknownResponse = errors.New("api: unexpected response")
)

// Client is a Bilibili web client. A zero value is not usable; always call
// NewClient. Client is safe for concurrent use by multiple goroutines.
type Client struct {
	httpc *http.Client
	ua    string

	mixinMu     sync.Mutex
	mixinKey    string
	mixinExpiry time.Time
	// mixinTTL controls how long a fetched mixin key is reused. Exposed as
	// a field (not a constant) so tests can drive cache-expiry behaviour.
	mixinTTL time.Duration
	// now is overridable in tests. Production always uses time.Now.
	now func() time.Time
}

// NewClient returns a Client backed by net/http. jar may be nil, in which
// case unauthenticated requests are made (Bilibili will still answer for
// public content, but pgc / pugv endpoints will likely refuse). ua may be
// empty to use DefaultUserAgent.
func NewClient(jar http.CookieJar, ua string) *Client {
	if ua == "" {
		ua = DefaultUserAgent
	}
	return &Client{
		httpc: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		ua:       ua,
		mixinTTL: 10 * time.Minute,
		now:      time.Now,
	}
}

// envelope models the common Bilibili response shape. Every endpoint we
// consume wraps its payload in {code, message, data}.
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	// Some pgc endpoints return the payload under "result" instead of
	// "data" when code is zero.
	Result json.RawMessage `json:"result"`
}

// payload returns whichever of Data/Result is populated.
func (e *envelope) payload() json.RawMessage {
	if len(e.Data) > 0 && string(e.Data) != "null" {
		return e.Data
	}
	return e.Result
}

// doJSON issues a GET, decodes the Bilibili envelope, and returns the raw
// payload on code == 0. Non-zero codes are translated to the sentinel
// errors. HTTP 412 — which Bilibili uses for anti-bot throttling before the
// body is even a JSON envelope — short-circuits to ErrRateLimited.
func (c *Client) doJSON(ctx context.Context, url string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("api: build request: %w", err)
	}
	req.Header.Set("User-Agent", c.ua)
	// Bilibili's WBI endpoint occasionally denies requests lacking a
	// referer; setting this generically is harmless for unauthenticated
	// endpoints.
	req.Header.Set("Referer", "https://www.bilibili.com")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api: http do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusPreconditionFailed {
		return nil, ErrRateLimited
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a bounded amount for diagnostic context.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("%w: http %d: %s", ErrUnknownResponse, resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("api: read body: %w", err)
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("%w: not JSON: %v", ErrUnknownResponse, err)
	}
	if env.Code == 0 {
		return env.payload(), nil
	}
	return nil, classifyCode(env.Code, env.Message)
}

// doRaw GETs url and returns the raw response body. Used for subtitle JSON
// which is not wrapped in the Bilibili envelope.
func (c *Client) doRaw(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("api: build request: %w", err)
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Referer", "https://www.bilibili.com")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api: http do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusPreconditionFailed {
		return nil, ErrRateLimited
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: http %d", ErrUnknownResponse, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// classifyCode maps a Bilibili API code onto the package sentinel errors.
// Codes observed in the wild:
//
//	-404  -> item absent or region-locked ("啥都木有")
//	62002 -> 稿件不可见 (content hidden / taken down)
//	87008 -> cheese episode not purchased
//	6002003 -> bangumi without entitlement
//	412   -> throttled (also sometimes surfaced in the JSON body)
func classifyCode(code int, msg string) error {
	switch code {
	case -404, 62002, 87008, 6002003:
		if msg == "" {
			return ErrContentLocked
		}
		return fmt.Errorf("%w: %s", ErrContentLocked, msg)
	case 412:
		return ErrRateLimited
	default:
		if msg == "" {
			return fmt.Errorf("%w: code %d", ErrUnknownResponse, code)
		}
		return fmt.Errorf("%w: code %d: %s", ErrUnknownResponse, code, msg)
	}
}
