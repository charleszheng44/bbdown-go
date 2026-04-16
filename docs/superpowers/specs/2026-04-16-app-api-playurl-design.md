# App-API playurl fallback — Design Spec

**Date:** 2026-04-16
**Status:** Awaiting final user review
**Authors:** Claude (Opus 4.7), under direction of the repository owner

## 1. Overview

When bangumi (pgc) or cheese (pugv) web playurl returns a preview-only response (`is_preview=1`, mapped to `api.ErrContentLocked`), transparently retry via Bilibili's **app-API** `PlayView` endpoint using a TV-login `access_token`. The app API authenticates through a mobile/TV code path that is not subject to the web front-end's anti-bot / entitlement checks that currently downgrade purchased content to preview on the web path.

UX goal: users who want purchased content run `bbdown login --tv` once; after that, every subsequent `bbdown <url>` that hits paid bangumi/cheese "just works," with no new flags on the download command.

## 2. Motivation

The sibling branch `feat/parts-list` (PR #32) exposed the preview-mode downgrade and gave it a clean error surface (`ErrContentLocked`), but it cannot *resolve* the downgrade — even with `buvid3`, `Extras` pass-through, and SESSDATA percent-encoding preserved, the web endpoint still returns `is_preview:1` for the user's purchased cheese course. The upstream BBDown project solves this via its `-app` flag which uses the TV-login access token against the gRPC `PlayView` endpoint. This spec ports that mechanism with the minimum surface area to make the failing case work, and wires it as an automatic fallback rather than a flag so it stays out of the way.

## 3. Scope

### In scope
- `bbdown login --tv` subcommand: opt-in TV QR login, persists access token alongside web cookies.
- Auto-fallback from `decodePgcPlayurl` preview detection to the app-API `PlayView` call for `parser.KindBangumi` and `parser.KindCourse` when TV auth is available.
- New `internal/api/app.go` that assembles `PlayViewReq`, frames it for gRPC, POSTs HTTP/2, and decodes the `PlayViewReply` back into the existing `api.PlayInfo` shape.
- Committed `.pb.go` files so `protoc` is not required at build time.
- Hint in `formatError` when `ErrContentLocked` fires *without* TV auth configured.
- `bbdown logout` wipes TV auth alongside web cookies.

### Out of scope (v1)
- Regular video (`KindRegular`) app-API path. Regular playurl works on the web API today.
- Refresh-token rotation — when the TV token expires, the user re-runs `bbdown login --tv`.
- Advanced app-reply fields: Dolby Vision, FLAC, multi-role dubbing, clip (intro/outro) metadata. We take `dash_video`/`dash_audio` only.
- IPv6-only hosts / `force_host=1` HTTP fallback.
- TV login via username/password, SMS, or web-token exchange.
- International (`biliintl`) / Southeast-Asia endpoints.

## 4. UX

### Setup (one-time, only for paid content)
```sh
bbdown login            # existing web QR flow — unchanged
bbdown login --tv       # new: second QR scan for app-API access token
```

Both save into the same `cookies.json` file; the TV section is optional.

### Download (unchanged)
```sh
bbdown -p 14 <paid-cheese-url>
```

The root command decides the auth mode automatically. No new flags on `<url>` invocations.

### Error hints
- `ErrContentLocked` with TV auth configured → same message as today, rare (means the account truly doesn't own the content).
- `ErrContentLocked` without TV auth configured → message ends with: *"Run `bbdown login --tv` to enable the app-API fallback for purchased bangumi/cheese."*
- TV token expired (code 86208 / `-101`) → *"TV token expired. Re-run `bbdown login --tv`."*

## 5. Architecture

### 5.1 Package layout

```
cmd/bbdown/
  login.go            # extended: --tv flag invokes LoginTV instead of LoginQR
internal/auth/
  cookies.go          # Cookies gains TV *TVAuth field
  tvlogin.go          # new: TV QR flow (auth_code + poll + sign)
  tvlogin_test.go     # new
internal/api/
  app.go              # new: PlayViewReq builder, gRPC framing, HTTP/2 POST, reply mapping
  app_test.go         # new: protobuf round-trip + fetchViaApp against httptest
  app_framing.go      # new: packFrame / unpackFrame (gzip + 5-byte length prefix)
  app_framing_test.go # new
  playinfo.go         # fetchBangumi / fetchCourse fallback integration
  client.go           # Client gains optional appAuth *auth.TVAuth setter
  appproto/
    playurl.proto     # committed source of truth for regeneration
    playurl.pb.go     # committed generated file
    headers.proto     # Device / Metadata / Locale / Network / FawkesReq
    headers.pb.go
```

### 5.2 Boundary rules

- `internal/auth` owns TV login endpoints, signing, and token persistence. It has no dependency on `internal/api`.
- `internal/api/app.go` depends on `internal/auth` only for the `TVAuth` type. It does not talk to the user; the CLI layer is the only place that reads `TVAuth` out of the `cookies.json` file and passes it to `api.NewClient`.
- `internal/api/appproto/` contains only generated code plus `.proto` sources — no hand-written logic.
- `fetchBangumi` and `fetchCourse` are the only callers that trigger fallback. Regular (BV/av) is never fed through `app.go`.

### 5.3 Dependencies

- `google.golang.org/protobuf` (Marshal/Unmarshal; ~2 MB transitive).
- Standard library `compress/gzip`, `encoding/binary`, `net/http` (HTTP/2 is native in Go's default transport since 1.6).
- No `google.golang.org/grpc`.

Build-time: contributors who modify `.proto` files run `protoc --go_out=…` manually, commit the regenerated `.pb.go`. Users and CI never invoke `protoc`.

### 5.4 Data flow (fallback path)

```
user -> cmd/bbdown -> runDownload
                    -> processURL
                       -> client.FetchPlayInfo(target, page=1)
                          -> fetchCourse (or fetchBangumi)
                             -> /pugv/view/web/season (or pgc)
                             -> /pugv/player/web/playurl (or pgc v2)
                             -> decodePgcPlayurl
                                └── is_preview:1 → return ErrContentLocked
                             -> IF client.appAuth != nil:
                                  fetchViaApp(target, info.EPID, info.CID, codecPref)
                                    -> buildPlayViewReq(...)           (proto)
                                    -> packFrame(gzip + prefix)
                                    -> POST HTTP/2 app.bilibili.com
                                       x-bili-*-bin headers,
                                       authorization: identify_v1 <token>
                                    -> unpackFrame + gunzip
                                    -> proto.Unmarshal into PlayViewReply
                                    -> map StreamList[0].DashVideo + DashAudio[0]
                                       into info.Videos / info.Audios
                                    -> info.Subtitles is left as whatever the
                                       pgc/pugv season fetch populated (empty
                                       today — bangumi/cheese subtitles are
                                       unchanged by this spec)
                                -> return info, nil
                                ELSE: return the original ErrContentLocked
```

### 5.5 Request construction

**Endpoint** (bangumi and cheese both):
```
POST https://app.bilibili.com/bilibili.pgc.gateway.player.v2.PlayURL/PlayView
```
(BBDown uses a separate regular endpoint at `grpc.biliapi.net`; we do not need it.)

**Headers** (all `*-bin` values are base64(protobuf.Marshal(message))):
```
Host: app.bilibili.com
User-Agent: Dalvik/2.1.0 (Linux; U; Android 11; M2012K11AC Build/RKQ1.200826.002) 7.32.0 os/android model/M2012K11AC mobi_app/android build/7320200 channel/xiaomi_cn_tv.danmaku.bili_zm20200902 innerVer/7320200 osVer/11 network/2 grpc-java-cronet/1.36.1
content-type: application/grpc
grpc-encoding: gzip
grpc-accept-encoding: identity,gzip
grpc-timeout: 17996161u
authorization: identify_v1 <access_token>
x-bili-fawkes-req-bin: <b64 FawkesReq {appkey, env=prod, session_id}>
x-bili-metadata-bin:   <b64 Metadata {access_key, mobi_app, build, channel, buvid="", platform}>
x-bili-device-bin:     <b64 Device   {app_id=1, build, buvid="", mobi_app, platform, channel, brand, model, osver}>
x-bili-network-bin:    <b64 Network  {type=Wifi, oid="46007"}>
x-bili-locale-bin:     <b64 Locale   {c_locale: {language="zh", region="CN"}}>
x-bili-restriction-bin: ""
x-bili-exps-bin: ""
te: trailers
```

Constants mirror BBDown (`appkey=android64`, `build=7320200`, `mobi_app=android`, `platform=android`, `channel=xiaomi_cn_tv.danmaku.bili_zm20200902`, `brand=M2012K11AC`, `model=Build/RKQ1.200826.002`, `osver=11`, `session_id=dedf8669`, `buvid=""`, `env=prod`).

**Body** (gRPC frame of `PlayViewReq`):
```
PlayViewReq {
  ep_id:              = info.EPID as int64
  cid:                = info.CID  as int64
  qn:                 = 127        // max; server returns available
  fnval:              = 4048       // DASH + HDR + 4K + Dolby + 8K superset
  fourk:              = true
  spmid:              = "main.ugc-video-detail.0.0"
  from_spmid:         = "main.my-history.0.0"
  prefer_codec_type:  = Code265    // hardcoded — planner.Prefs.CodecOrder is not
                                   // plumbed to fetch-time in v1; HEVC-first matches
                                   // the default planner order. Revisit if users need
                                   // per-invocation codec control on the fallback path.
  download:           = 0
  force_host:         = 2          // https
}
```

Frame layout (5 bytes + payload):
```
offset 0   : 0x01            // 1 = gzip, 0 = identity
offset 1..4: uint32 big-endian length of payload
offset 5..  : payload (gzip(proto.Marshal(req)) or proto.Marshal(req))
```

### 5.6 Response handling

`proto.Unmarshal` into `PlayViewReply`. Map:

```
reply.video_info.timelength              -> (ignored — we already have it)
reply.video_info.stream_list[i]          -> api.Stream
    .stream_info.quality                 -> Stream.ID
    .dash_video.base_url                 -> Stream.BaseURL
    .dash_video.backup_url[]             -> Stream.BackupURLs
    .dash_video.bandwidth                -> Stream.Bandwidth
    .dash_video.codecid                  -> Stream.Codecs  ("avc1" / "hev1" per planner mapping)
reply.video_info.dash_audio[i]           -> api.Stream (audio)
    .base_url / backup_url / bandwidth / id
```

Streams without a populated `dash_video` (e.g. Dolby-only entries) are skipped — we only download what the standard DASH path understands.

If `stream_list` is empty → return `ErrUnknownResponse` wrapping "app playurl returned no DASH streams" (parallel to the web-side empty-streams guard).

### 5.7 TV login wire details

**auth_code request:**
```
POST https://passport.snm0516.aisee.tv/x/passport-tv-login/qrcode/auth_code
Content-Type: application/x-www-form-urlencoded

appkey=4409e2ce8ffd12b8
local_id=0
ts=<unix-sec>
sign=<hex md5(sorted_querystring + "59b43e04ad6965f34319062b478f83dd")>
```
Response: `{"code":0,"data":{"url":"bilibili://...","auth_code":"<code>"}}`. Render `url` as QR.

**Poll loop** (1 Hz):
```
POST https://passport.bilibili.com/x/passport-tv-login/qrcode/poll
(same appkey, ts, sign; plus auth_code=<code>)
```
Codes: `0` success, `86039` not scanned, `86038` expired.

**Success payload:**
```
data: {
  access_token: "<token>",
  refresh_token: "<token>",
  expires_in: <seconds>,
  mid: <int64>
}
```

Persisted as `TVAuth{AccessToken, RefreshToken, MID, ExpiresAt}` under `cookies.Cookies.TV`. `ExpiresAt` is the absolute unix-seconds timestamp computed from `now + expires_in` at login time.

### 5.8 Client construction

`api.NewClient(jar, ua)` keeps its existing signature. A new setter `(*api.Client).SetAppAuth(*auth.TVAuth)` attaches the token; when nil (the default), no fallback fires. `cmd/bbdown/download.go`'s `loadCookies` helper becomes `loadAuth` and populates both the jar and the app auth; `runParts` does the same.

## 6. Error handling

| Condition | Error | User-facing message |
|---|---|---|
| pgc/pugv preview, no TV auth | `api.ErrContentLocked` (existing) | Existing message + hint: *"Run `bbdown login --tv` to enable the app-API fallback for purchased bangumi/cheese."* |
| pgc/pugv preview, TV auth attempt also fails | Original `api.ErrContentLocked` | Existing message (the account truly isn't entitled) |
| App API returns code 87008 / -404 / 62002 / 6002003 | `api.ErrContentLocked` (existing, wrapped msg) | Existing message |
| App API returns code 412 | `api.ErrRateLimited` (existing) | Existing message |
| App API returns code 86208 or HTTP 401 | `auth.ErrTVTokenExpired` (new) | *"TV token expired. Re-run `bbdown login --tv`."* |
| App API empty `stream_list` | `api.ErrUnknownResponse` | *"Unexpected response from Bilibili. Re-run with --debug to see the raw payload."* |
| Transport / gzip / protobuf decode | `api.ErrUnknownResponse` with stage context | Same as above, but `--debug` shows the specific layer |

One new sentinel error: `auth.ErrTVTokenExpired`. No other new error types; everything else reuses existing sentinels.

## 7. Testing

No CI traffic to real Bilibili. All tests use `httptest` + pre-baked protobuf bytes.

### `internal/auth/tvlogin_test.go`
- Happy path: mocked `auth_code` → mocked `poll` returns 86039 twice then 0 with a token.
- 86038 → `ErrQRExpired`.
- Signature verification: the test server checks `sign` against the md5 it computes, rejects with 86000 on mismatch.
- Context cancel mid-poll returns `context.Canceled`.

### `internal/api/app_framing_test.go`
- Round-trip `packFrame(plain=false, payload)` then `unpackFrame` returns the original payload.
- Fixture assertions for byte layout at offsets 0..4.
- `unpackFrame` rejects truncated frames with a typed error.

### `internal/api/app_test.go`
- `fetchViaApp` against an `httptest` server that returns a pre-baked `PlayViewReply` (stored under `testdata/playviewreply.bin`):
  - Success → `PlayInfo.Videos` and `PlayInfo.Audios` contain the expected first entries.
  - gzip'd and non-gzip'd response frames both decode.
  - Code 87008 → `ErrContentLocked`.
  - Code 86208 → `auth.ErrTVTokenExpired`.
  - Empty `stream_list` → `ErrUnknownResponse`.
- Header shape: the test server asserts `authorization: identify_v1 <token>` and that `x-bili-*-bin` headers decode as valid protobuf.

### `internal/api/api_test.go` (integration)
- New case `TestFetchPlayInfo_CoursePreviewFallsBackToApp`: two handlers — pugv web returns `is_preview:1`; app endpoint returns valid DASH. `Client.SetAppAuth(fakeToken)` then `FetchPlayInfo` returns the app-side streams.

## 8. Branching and delivery

- Branch: `feat/app-api-playurl` off `main` (already created for this spec).
- This branch depends on PR #32 (`feat/parts-list`) for the `ErrContentLocked` preview detection it keys off. If PR #32 merges first, this branch rebases onto main and the dependency becomes implicit. If not, we rebase onto `feat/parts-list` and wait.
- Single PR with logical commits grouped by subsystem (TV login → protobuf generated files → framing → app client → fallback integration → README).

## 9. Future work (explicitly deferred)

- **Refresh-token rotation.** Detect expiry a few minutes early, call the refresh endpoint transparently. Not needed in v1 — re-running `login --tv` is acceptable.
- **Strict mode flag** `--app-required` that fails fast when web returns preview and TV auth is absent. Probably unnecessary; error hint suffices.
- **`bbdown logout --tv`** to wipe only the TV half. Rare need; `bbdown logout` already clears both.
- **Dolby / FLAC stream extraction.** The `PlayViewReply` carries these fields; we currently ignore them. Adding them is additive and non-breaking.
- **International endpoints / non-CN access.** Use the `biliintl` path from BBDown. Different auth entirely.

## 10. Risks

- **Bilibili may change the app-API signing keys or endpoint URLs.** We match BBDown's constants at time of writing; upstream maintains them.
- **The committed `.pb.go` files drift from the `.proto` source.** Mitigation: CI runs `protoc --go_out=. internal/api/appproto/*.proto` + `git diff --exit-code` to fail if the committed files are stale. (Added to the Makefile's `make check` target.)
- **HTTP/2 requirement.** Go's default transport negotiates HTTP/2 for HTTPS automatically; corporate proxies that intercept TLS may break this. Documented in README troubleshooting.
- **Account region / purchase-window edge cases still return preview even via app API.** The error hint is honest: if app-side also fails, the account isn't entitled.
