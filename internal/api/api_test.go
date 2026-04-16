package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/charleszheng44/bbdown-go/internal/parser"
)

// Identifiers for the three base-URL package vars so tests can rebind a
// single base without copy-pasting boilerplate.
type baseField int

const (
	apiBaseField baseField = iota
	pgcBaseField
	pugvBaseField
)

// withBase temporarily rebinds one of the endpoint base URLs for the
// duration of fn. A matching defer restores the original on exit.
func withBase(t *testing.T, which baseField, url string, fn func()) {
	t.Helper()
	switch which {
	case apiBaseField:
		prev := apiBase
		apiBase = url
		defer func() { apiBase = prev }()
	case pgcBaseField:
		prev := pgcBase
		pgcBase = url
		defer func() { pgcBase = prev }()
	case pugvBaseField:
		prev := pugvBase
		pugvBase = url
		defer func() { pugvBase = prev }()
	default:
		t.Fatalf("unknown baseField %v", which)
	}
	fn()
}

// withAllBases points api/pgc/pugv at the same test server. Handy when one
// httptest.Server multiplexes by path for a single test.
func withAllBases(t *testing.T, url string, fn func()) {
	t.Helper()
	withBase(t, apiBaseField, url, func() {
		withBase(t, pgcBaseField, url, func() {
			withBase(t, pugvBaseField, url, func() {
				fn()
			})
		})
	})
}

// envelopeOK wraps payload in the Bilibili {code:0, data:payload} envelope.
func envelopeOK(payload any) []byte {
	b, _ := json.Marshal(map[string]any{"code": 0, "message": "0", "data": payload})
	return b
}

// envelopeErr produces a Bilibili error envelope.
func envelopeErr(code int, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"code": code, "message": msg, "data": nil})
	return b
}

// ─── regular flow ─────────────────────────────────────────────────────────

func TestFetchPlayInfo_Regular_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/x/web-interface/nav", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeOK(map[string]any{
			"wbi_img": map[string]any{
				"img_url": "https://x/7cd084941338484aae1ad9425b84077c.png",
				"sub_url": "https://x/4932caff0ff746eab6f01bf08b70ac45.png",
			},
		}))
	})
	mux.HandleFunc("/x/web-interface/view", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("bvid") != "BV1aBc" {
			t.Errorf("unexpected bvid: %q", r.URL.Query().Get("bvid"))
		}
		w.Write(envelopeOK(map[string]any{
			"bvid":  "BV1aBc",
			"aid":   170001,
			"cid":   555,
			"title": "Demo Title",
			"pages": []map[string]any{
				{"page": 1, "cid": 555, "part": "P1", "duration": 60},
				{"page": 2, "cid": 556, "part": "P2", "duration": 90},
			},
			"subtitle": map[string]any{
				"list": []map[string]any{
					{"lan": "zh-CN", "subtitle_url": "//i0.hdslb.com/s/zh.json"},
				},
			},
		}))
	})
	mux.HandleFunc("/x/player/wbi/playurl", func(w http.ResponseWriter, r *http.Request) {
		// The request must carry wts and w_rid, proving the WBI path ran.
		q := r.URL.Query()
		if q.Get("wts") == "" || q.Get("w_rid") == "" {
			t.Errorf("playurl missing wbi params: %s", r.URL.RawQuery)
		}
		if q.Get("cid") != "555" {
			t.Errorf("playurl cid = %q, want 555", q.Get("cid"))
		}
		w.Write(envelopeOK(map[string]any{
			"accept_quality":     []int{80, 32, 16},
			"accept_description": []string{"1080P", "480P", "360P"},
			"dash": map[string]any{
				"video": []map[string]any{
					{
						"id":        80,
						"baseUrl":   "https://cdn/v80.m4s",
						"backupUrl": []string{"https://backup/v80.m4s"},
						"bandwidth": 3000000,
						"codecs":    "avc1.640032",
						"mimeType":  "video/mp4",
					},
				},
				"audio": []map[string]any{
					{
						"id":        30280,
						"baseUrl":   "https://cdn/a.m4s",
						"bandwidth": 128000,
						"codecs":    "mp4a.40.2",
						"mimeType":  "audio/mp4",
					},
				},
			},
		}))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	withAllBases(t, srv.URL, func() {
		c := NewClient(nil, "")
		c.now = func() time.Time { return time.Unix(1700000000, 0) }
		info, err := c.FetchPlayInfo(context.Background(), parser.Target{
			Kind: parser.KindRegular,
			BVID: "BV1aBc",
		}, 1)
		if err != nil {
			t.Fatalf("FetchPlayInfo: %v", err)
		}
		if info.Title != "Demo Title" || info.BVID != "BV1aBc" || info.AID != "170001" || info.CID != "555" {
			t.Errorf("metadata mismatch: %+v", info)
		}
		if len(info.Parts) != 2 || info.Parts[1].CID != "556" {
			t.Errorf("parts mismatch: %+v", info.Parts)
		}
		if len(info.Videos) != 1 || info.Videos[0].BaseURL != "https://cdn/v80.m4s" || info.Videos[0].Quality != "1080P" {
			t.Errorf("video stream mismatch: %+v", info.Videos)
		}
		if len(info.Videos[0].BackupURLs) != 1 {
			t.Errorf("backup urls mismatch: %+v", info.Videos[0].BackupURLs)
		}
		if len(info.Audios) != 1 || info.Audios[0].ID != 30280 {
			t.Errorf("audio stream mismatch: %+v", info.Audios)
		}
		if len(info.Subtitles) != 1 || info.Subtitles[0].URL != "https://i0.hdslb.com/s/zh.json" {
			t.Errorf("subtitle mismatch: %+v", info.Subtitles)
		}
	})
}

// ─── bangumi flow ─────────────────────────────────────────────────────────

func TestFetchPlayInfo_Bangumi_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/pgc/view/web/season", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ep_id") != "12345" {
			t.Errorf("unexpected ep_id: %q", r.URL.Query().Get("ep_id"))
		}
		w.Write(envelopeOK(map[string]any{
			"season_id": 42,
			"title":     "Bangumi Title",
			"episodes": []map[string]any{
				{"ep_id": 12345, "cid": 999, "aid": 111, "title": "E1", "long_title": "Episode One"},
				{"ep_id": 12346, "cid": 1000, "aid": 111, "title": "E2", "long_title": "Episode Two"},
			},
		}))
	})
	mux.HandleFunc("/pgc/player/web/playurl", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ep_id") != "12345" || r.URL.Query().Get("cid") != "999" {
			t.Errorf("playurl params mismatch: %s", r.URL.RawQuery)
		}
		w.Write(envelopeOK(map[string]any{
			"accept_quality":     []int{80},
			"accept_description": []string{"1080P"},
			"dash": map[string]any{
				"video": []map[string]any{{"id": 80, "baseUrl": "https://cdn/bv.m4s", "bandwidth": 2000000, "codecs": "avc1", "mimeType": "video/mp4"}},
				"audio": []map[string]any{{"id": 30280, "baseUrl": "https://cdn/ba.m4s", "bandwidth": 128000, "codecs": "mp4a.40.2", "mimeType": "audio/mp4"}},
			},
		}))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	withAllBases(t, srv.URL, func() {
		c := NewClient(nil, "")
		info, err := c.FetchPlayInfo(context.Background(), parser.Target{
			Kind: parser.KindBangumi,
			EPID: "12345",
		}, 1)
		if err != nil {
			t.Fatalf("FetchPlayInfo: %v", err)
		}
		if info.Title != "Bangumi Title" || info.EPID != "12345" || info.CID != "999" {
			t.Errorf("metadata: %+v", info)
		}
		if len(info.Videos) != 1 || info.Videos[0].Quality != "1080P" {
			t.Errorf("streams: %+v", info.Videos)
		}
	})
}

// TestFetchPlayInfo_Bangumi_NestedPlayurl covers the pgc variant where the
// DASH payload is wrapped under "video_info".
func TestFetchPlayInfo_Bangumi_NestedPlayurl(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/pgc/view/web/season", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeOK(map[string]any{
			"title":    "X",
			"episodes": []map[string]any{{"ep_id": 1, "cid": 10, "aid": 20, "title": "E"}},
		}))
	})
	mux.HandleFunc("/pgc/player/web/playurl", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeOK(map[string]any{
			"video_info": map[string]any{
				"accept_quality":     []int{80},
				"accept_description": []string{"1080P"},
				"dash": map[string]any{
					"video": []map[string]any{{"id": 80, "baseUrl": "https://v"}},
					"audio": []map[string]any{{"id": 30280, "baseUrl": "https://a"}},
				},
			},
		}))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	withAllBases(t, srv.URL, func() {
		c := NewClient(nil, "")
		info, err := c.FetchPlayInfo(context.Background(), parser.Target{Kind: parser.KindBangumi, EPID: "1"}, 1)
		if err != nil {
			t.Fatalf("FetchPlayInfo: %v", err)
		}
		if len(info.Videos) != 1 || info.Videos[0].BaseURL != "https://v" {
			t.Errorf("nested video_info not decoded: %+v", info.Videos)
		}
	})
}

// ─── course (pugv) flow ───────────────────────────────────────────────────

func TestFetchPlayInfo_Course_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/pugv/view/web/season", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ep_id") != "77" {
			t.Errorf("unexpected ep_id: %q", r.URL.Query().Get("ep_id"))
		}
		w.Write(envelopeOK(map[string]any{
			"title": "Course",
			"episodes": []map[string]any{
				{"id": 77, "cid": 700, "aid": 8000, "title": "Lesson 1"},
			},
		}))
	})
	mux.HandleFunc("/pugv/player/web/playurl", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeOK(map[string]any{
			"accept_quality":     []int{80},
			"accept_description": []string{"1080P"},
			"dash": map[string]any{
				"video": []map[string]any{{"id": 80, "baseUrl": "https://c/v"}},
				"audio": []map[string]any{{"id": 30280, "baseUrl": "https://c/a"}},
			},
		}))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	withAllBases(t, srv.URL, func() {
		c := NewClient(nil, "")
		info, err := c.FetchPlayInfo(context.Background(), parser.Target{Kind: parser.KindCourse, EPID: "77"}, 1)
		if err != nil {
			t.Fatalf("FetchPlayInfo: %v", err)
		}
		if info.Title != "Course" || info.EPID != "77" || info.CID != "700" {
			t.Errorf("metadata: %+v", info)
		}
	})
}

// TestFetchPlayInfo_CourseLocked asserts that a pugv "not purchased" code
// surfaces as ErrContentLocked.
func TestFetchPlayInfo_CourseLocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeErr(87008, "课程未购买"))
	}))
	defer srv.Close()
	withAllBases(t, srv.URL, func() {
		c := NewClient(nil, "")
		_, err := c.FetchPlayInfo(context.Background(), parser.Target{Kind: parser.KindCourse, EPID: "77"}, 1)
		if !errors.Is(err, ErrContentLocked) {
			t.Errorf("got %v, want ErrContentLocked", err)
		}
	})
}

// TestFetchPlayInfo_RegularLocked exercises the -404 hidden-content path.
func TestFetchPlayInfo_RegularLocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeErr(-404, "啥都木有"))
	}))
	defer srv.Close()
	withAllBases(t, srv.URL, func() {
		c := NewClient(nil, "")
		_, err := c.FetchPlayInfo(context.Background(), parser.Target{Kind: parser.KindRegular, BVID: "BVxxx"}, 1)
		if !errors.Is(err, ErrContentLocked) {
			t.Errorf("got %v, want ErrContentLocked", err)
		}
	})
}

// TestRateLimited412 exercises the HTTP 412 short-circuit.
func TestRateLimited412(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer srv.Close()
	withAllBases(t, srv.URL, func() {
		c := NewClient(nil, "")
		_, err := c.FetchPlayInfo(context.Background(), parser.Target{Kind: parser.KindRegular, BVID: "BV"}, 1)
		if !errors.Is(err, ErrRateLimited) {
			t.Errorf("got %v, want ErrRateLimited", err)
		}
	})
}

// TestRateLimitedCode412 exercises the code==412 JSON-body path.
func TestRateLimitedCode412(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeErr(412, "请求被拦截"))
	}))
	defer srv.Close()
	withAllBases(t, srv.URL, func() {
		c := NewClient(nil, "")
		_, err := c.FetchPlayInfo(context.Background(), parser.Target{Kind: parser.KindBangumi, EPID: "1"}, 1)
		if !errors.Is(err, ErrRateLimited) {
			t.Errorf("got %v, want ErrRateLimited", err)
		}
	})
}

// TestUnknownResponseCode verifies that unclassified codes become
// ErrUnknownResponse and carry the message.
func TestUnknownResponseCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeErr(99999, "something broke"))
	}))
	defer srv.Close()
	withAllBases(t, srv.URL, func() {
		c := NewClient(nil, "")
		_, err := c.FetchPlayInfo(context.Background(), parser.Target{Kind: parser.KindBangumi, EPID: "1"}, 1)
		if !errors.Is(err, ErrUnknownResponse) {
			t.Fatalf("got %v, want ErrUnknownResponse", err)
		}
		if !strings.Contains(err.Error(), "something broke") {
			t.Errorf("expected message in error, got %v", err)
		}
	})
}

// TestFetchPlayInfo_CoursePreview exercises the case where pugv playurl
// returns code 0 with is_preview=1 and a durl-only MP4 preview clip. The
// session is not entitled to the full DASH streams; callers should see
// ErrContentLocked, not an empty-streams surprise at the planner layer.
func TestFetchPlayInfo_CoursePreview(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/pugv/view/web/season", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeOK(map[string]any{
			"title":    "Course",
			"episodes": []map[string]any{{"id": 77, "cid": 700, "aid": 8000, "title": "Lesson"}},
		}))
	})
	mux.HandleFunc("/pugv/player/web/playurl", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeOK(map[string]any{
			"is_preview": 1,
			"type":       "MP4",
			"fnval":      1,
			"durl": []map[string]any{
				{"size": 100, "length": 500, "url": "https://preview.example/clip.mp4"},
			},
		}))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	withAllBases(t, srv.URL, func() {
		c := NewClient(nil, "")
		_, err := c.FetchPlayInfo(context.Background(), parser.Target{Kind: parser.KindCourse, EPID: "77"}, 1)
		if !errors.Is(err, ErrContentLocked) {
			t.Fatalf("got %v, want ErrContentLocked", err)
		}
	})
}

// TestFetchPlayInfo_BangumiPreview mirrors TestFetchPlayInfo_CoursePreview
// for the pgc path, which also returns is_preview=1 for non-VIP sessions
// attempting to watch VIP-only episodes.
func TestFetchPlayInfo_BangumiPreview(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/pgc/view/web/season", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeOK(map[string]any{
			"title":    "X",
			"episodes": []map[string]any{{"ep_id": 1, "cid": 10, "aid": 20, "title": "E"}},
		}))
	})
	mux.HandleFunc("/pgc/player/web/playurl", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeOK(map[string]any{
			"is_preview": 1,
			"type":       "MP4",
			"durl":       []map[string]any{{"url": "https://preview.example/clip.mp4"}},
		}))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	withAllBases(t, srv.URL, func() {
		c := NewClient(nil, "")
		_, err := c.FetchPlayInfo(context.Background(), parser.Target{Kind: parser.KindBangumi, EPID: "1"}, 1)
		if !errors.Is(err, ErrContentLocked) {
			t.Fatalf("got %v, want ErrContentLocked", err)
		}
	})
}

// TestFetchPlayInfo_CourseNoDashNoPreview exercises the defensive path:
// playurl succeeded (code 0) and is_preview=0, but no recognized shape
// populated any streams. Callers should see ErrUnknownResponse rather than
// leaking an empty-streams state to the planner.
func TestFetchPlayInfo_CourseNoDashNoPreview(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/pugv/view/web/season", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(envelopeOK(map[string]any{
			"title":    "Course",
			"episodes": []map[string]any{{"id": 77, "cid": 700, "aid": 8000, "title": "Lesson"}},
		}))
	})
	mux.HandleFunc("/pugv/player/web/playurl", func(w http.ResponseWriter, _ *http.Request) {
		// Code 0, no is_preview, but zero streams in either shape we know.
		w.Write(envelopeOK(map[string]any{"message": "ok"}))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	withAllBases(t, srv.URL, func() {
		c := NewClient(nil, "")
		_, err := c.FetchPlayInfo(context.Background(), parser.Target{Kind: parser.KindCourse, EPID: "77"}, 1)
		if !errors.Is(err, ErrUnknownResponse) {
			t.Fatalf("got %v, want ErrUnknownResponse", err)
		}
	})
}

// TestFetchSubtitle_EndToEnd drives the FetchSubtitle path through an
// httptest server serving BCC JSON.
func TestFetchSubtitle_EndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"body":[{"from":0,"to":1,"content":"hi"}]}`)
	}))
	defer srv.Close()

	c := NewClient(nil, "")
	got, err := c.FetchSubtitle(context.Background(), srv.URL+"/sub.json")
	if err != nil {
		t.Fatalf("FetchSubtitle: %v", err)
	}
	want := "1\n00:00:00,000 --> 00:00:01,000\nhi\n\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", string(got), want)
	}
}

// TestClassifyErrEdge verifies ErrContentLocked without a message still wraps.
func TestClassifyErrEdge(t *testing.T) {
	err := classifyCode(62002, "")
	if !errors.Is(err, ErrContentLocked) {
		t.Errorf("classifyCode(62002, \"\") = %v", err)
	}
	err = classifyCode(0, "") // zero is not an error code; behavior should be unknown-response.
	if !errors.Is(err, ErrUnknownResponse) {
		t.Errorf("classifyCode(0, \"\") = %v, want wrapped ErrUnknownResponse", err)
	}
}
