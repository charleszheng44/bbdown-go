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
//
// NOTE: Bilibili's app endpoint can return non-zero result codes
// (e.g. 87008 "course not purchased", 86208 "token expired") embedded
// in a response body or gRPC trailer rather than via HTTP status. The
// wire format for those has not been observed yet; until it is, only
// HTTP-level failures (401, 412, other non-2xx) are classified. If a
// --debug session surfaces preview-like empty replies from the app
// endpoint, file an issue with the dumped frame so this mapping can
// be extended.
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

// encodeBinHeader returns base64(Marshal(m)).
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
