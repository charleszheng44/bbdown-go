# bbdown-go — Design Spec

**Date:** 2026-04-13
**Status:** Approved pending final user review
**Authors:** Claude (Opus 4.6), under direction of the repository owner

## 1. Overview

A Go port of [BBDown](https://github.com/nilaoda/BBDown) focused on a minimal, reliable command-line downloader for Bilibili. Scope is deliberately narrow: the core download path plus authenticated access (including purchased courses). No Dolby Vision handling, no international API, no danmaku, no cover art, no AI-subtitle support in v1.

## 2. Goals and non-goals

### Goals
- Single static binary on Linux, macOS, and Windows (amd64 + arm64).
- Download regular videos (`BV`/`av`), bangumi (`ep`/`ss`), and purchased courses (`cheese`) — video, audio, and subtitles — muxed into MP4.
- QR-code login with persistent cookie storage; cookie-paste fallback.
- Quality selection (flag + interactive picker), multi-part selection, batch download from URL list.
- Clear, actionable error messages. No opaque failure modes.

### Non-goals (v1)
- Danmaku, cover images, AI-generated subtitles.
- International / Southeast-Asia API variant.
- Dolby Vision, HDR, 8K-specific handling beyond whatever falls out of generic DASH stream selection.
- Username/password login (fragile; requires geetest CAPTCHA + RSA).
- GUI, API server, or library-as-service use.

## 3. Language choice

**Go.** Rationale:
- Workload is I/O-bound (HTTP + disk). Rust's zero-cost abstractions and memory-safety guarantees don't meaningfully improve an HTTP downloader.
- Go's stdlib covers HTTP, cookies, JSON, TLS, and structured concurrency (goroutines + channels) natively — ideal for parallel segment downloads and batch jobs.
- Single static binary with trivial cross-compilation.
- Lower implementation and review cost for a Claude-written codebase.

## 4. Architecture

### 4.1 Package layout
```
cmd/bbdown/          entry point; cobra command tree; error formatting
internal/auth/       QR login flow, cookie store (XDG config dir)
internal/api/        Bilibili HTTP client: typed request/response, WBI signing
internal/parser/     URL/ID classification -> normalized target descriptor
internal/planner/    pick video+audio streams given user preferences
internal/download/   ranged/parallel HTTP downloader with retries and progress
internal/mux/        ffmpeg wrapper: video+audio(+subs) -> MP4
internal/config/     XDG paths, user preferences, filename templates
```

### 4.2 Boundary rules
- `api` returns typed Go structs; callers never touch raw JSON.
- `download` is Bilibili-agnostic; it fetches URLs into files.
- `auth` owns the cookie jar; `api` receives an already-authenticated `*http.Client`.
- `cmd/bbdown` is the only package that talks to the user or os.Stdout for user-facing messages. Library packages return errors and structured data.

### 4.3 Dependencies
- `github.com/spf13/cobra` — CLI sub-commands and help generation.
- `github.com/mdp/qrterminal/v3` — render QR code in the terminal during login.
- Standard library for everything else (HTTP, JSON, cookies, concurrency).

External runtime dependency: `ffmpeg` on `PATH`. Detected at startup; absent triggers a typed error with install instructions.

## 5. Data flow

```
URL (argv)
  -> parser.Classify()            -> Target{Kind, IDs}
  -> api.FetchPlayInfo(target)    -> PlayInfo{Parts[], Streams[], Subtitles[]}
  -> planner.Pick(playInfo, prefs)-> Selection{VideoURL, AudioURL, Codec, Quality}
  -> download.Fetch(selection, outDir) -> temp files (.m4v, .m4a, .srt)
  -> mux.Combine(temp files)      -> final <template>.mp4
```

Batch and multi-part flows iterate this pipeline per item, reusing the authenticated client and a bounded worker pool.

## 6. Supported URL kinds

| Kind    | Example                                                          | Metadata endpoint                  | Stream endpoint                    |
|---------|------------------------------------------------------------------|-------------------------------------|------------------------------------|
| Regular | `BV1xx411c7mD`, `av170001`, `bilibili.com/video/BV...`           | `x/web-interface/view`              | `x/player/wbi/playurl`             |
| Bangumi | `ep12345`, `ss1234`                                              | `pgc/view/web/season`               | `pgc/player/web/playurl`           |
| Course  | `bilibili.com/cheese/play/ep12345`                               | `pugv/view/web/season`              | `pugv/player/web/playurl`          |

All endpoints consumed on `api.bilibili.com` / `api.bilibili.com/pgc` / `api.bilibili.com/pugv`, authenticated via the `SESSDATA` cookie.

## 7. Authentication

### 7.1 Primary: QR-code login
Mirrors BBDown's WEB login:
1. `GET passport.bilibili.com/x/passport-login/web/qrcode/generate` -> `{url, qrcode_key}`.
2. Render the `url` as a QR code in the terminal via `qrterminal`.
3. Poll `passport.bilibili.com/x/passport-login/web/qrcode/poll?qrcode_key=...` at 1 Hz.
4. On success, the response contains a redirect URL whose query string carries `SESSDATA`, `bili_jct`, `DedeUserID`, and `DedeUserID__ckMd5`. Parse and persist all four as cookies.
5. Cookies stored at `$XDG_CONFIG_HOME/bbdown/cookies.json` (falls back to `~/.config/bbdown/cookies.json`), mode `0600`.

Poll codes handled: `86101` (waiting for scan), `86090` (waiting for confirm), `86038` (expired -> abort with friendly message), `0` (success).

### 7.2 Fallback: manual cookie
`--cookie "SESSDATA=...; bili_jct=..."` bypasses the persisted jar for a single invocation. Useful for CI or scripted use.

### 7.3 Purchased-course access
No separate entitlement call. The authenticated `SESSDATA` session that granted the purchase is what unlocks the `pugv` endpoints. If the session does not own the course, the API returns a locked-content error which is surfaced as `api.ErrContentLocked`.

### 7.4 Logout
`bbdown logout` deletes the cookie file. No server-side revocation call (not required).

## 8. CLI surface

```
bbdown login                        # QR-code login, persist cookies
bbdown logout                       # delete cookies
bbdown <url> [flags]                # download one URL
bbdown --batch-file urls.txt        # one URL per line

Selection and quality:
  -p, --part <spec>                 1,3-5 | ALL | LAST
  -q, --quality <name>              e.g. "1080P 60" ; falls back to nearest
  -i, --interactive                 interactive picker for quality
      --video-only
      --audio-only
      --sub-only

Output:
  -o, --output-dir <dir>            default: cwd
      --name <template>             default: <title>
      --multi-name <template>       default: <title>/P<page>-<pageTitle>

Auth:
      --cookie <string>             one-shot SESSDATA=...; bili_jct=...

Misc:
      --threads <n>                 per-file download workers (default 8)
      --concurrency <n>             concurrent items in batch mode (default 2)
      --debug                       verbose logs + raw API dumps
      --version
```

### 8.1 Template variables (v1 subset)
`<title>`, `<page>`, `<pageTitle>`, `<bvid>`, `<aid>`, `<quality>`, `<codec>`.

## 9. Error handling

Typed errors exported from each package. `cmd/bbdown` translates them to user-facing messages in one place.

| Error                      | User-facing message                                          |
|----------------------------|--------------------------------------------------------------|
| `auth.ErrNotLoggedIn`      | "Not logged in. Run `bbdown login` first."                   |
| `auth.ErrQRExpired`        | "QR code expired. Run `bbdown login` again."                 |
| `api.ErrContentLocked`     | "This content requires a purchase or is region-locked."       |
| `api.ErrRateLimited`       | "Rate-limited by Bilibili. Retry after a short wait."         |
| `api.ErrUnknownResponse`   | Includes raw payload when `--debug` is set.                   |
| `mux.ErrFFmpegMissing`     | "ffmpeg not found on PATH. Install from https://ffmpeg.org." |
| `download.ErrDiskFull`     | "Out of disk space at <path>."                               |

HTTP client retry policy: up to 3 retries on 429 / 5xx / network errors with exponential backoff (base 500 ms, cap 8 s).

## 10. Concurrency model

- Per-file download uses N range-request workers (default 8, `--threads` flag override). Each worker owns a byte range and writes to a pre-allocated file.
- Batch download uses a bounded worker pool (default 2 items concurrent) to avoid triggering rate limits.
- Graceful cancellation via `context.Context` rooted in `cmd/bbdown`; SIGINT cancels in-flight downloads and cleans temp files.

## 11. Testing

- **Unit** (default `go test ./...`, no network):
  - `parser` URL-to-kind table with representative inputs.
  - `planner` quality selection over fixture stream lists (edge cases: no preferred codec present, only audio, only video).
  - `config` XDG path resolution and template rendering.
  - `auth` cookie serialization round-trip.
- **Integration** (`//go:build integration`, opt-in):
  - Real `api.bilibili.com` calls using cookie from `BBDOWN_TEST_COOKIE`.
  - One regular video, one bangumi episode, one cheese episode.
  - Skipped in CI by default.
- **Tooling**: `gofmt`, `go vet`, `staticcheck` run in CI.

## 12. Configuration and on-disk layout

```
$XDG_CONFIG_HOME/bbdown/
  cookies.json        # 0600, JSON array of http.Cookie-shaped entries
  config.toml         # optional: default quality, default output dir, thread count
$XDG_CACHE_HOME/bbdown/
  tmp/<job-id>/       # in-flight segments, cleared on success or next run
```

## 13. README and attribution

The README opens with:
1. A legal disclaimer (English + Chinese) mirroring BBDown's stance: personal / research / non-commercial use; user is responsible for legal compliance; only download content the user is entitled to access.
2. An attribution block stating the project is a Go port of BBDown by nilaoda, links to the upstream repository, and notes that the implementation in this repo was written entirely by Claude (Anthropic) under human direction.
3. Quick start, command reference, supported URL types, troubleshooting, license.

`LICENSE` is MIT and preserves upstream's MIT copyright notice alongside ours.

## 14. Open items before implementation

- Go module path is `github.com/charleszheng44/bbdown-go`.
- Exact default thread count and batch concurrency (placeholders: 8 / 2).
- Whether to ship a `Dockerfile` in v1 (BBDown has one; leaning no for minimal scope).
