package api

import (
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

func TestFetchViaAppHTTP412RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer srv.Close()
	origBase := appBase
	appBase = srv.URL
	defer func() { appBase = origBase }()

	c := NewClient(nil, "")
	c.SetAppAuth(&auth.TVAuth{AccessToken: "t"})
	_, err := c.fetchViaApp(context.Background(), "1", "2")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("got %v, want ErrRateLimited", err)
	}
}

// TestFetchViaAppSuccessWithFixture reads a pre-baked .bin file so the
// decoding path is exercised against bytes that were once emitted by
// proto.Marshal, guarding against silent breakage if the reply shape
// drifts.
func TestFetchViaAppSuccessWithFixture(t *testing.T) {
	fixturePath := "testdata/playviewreply_ok.bin"
	body, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture missing (%v)", err)
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
}
