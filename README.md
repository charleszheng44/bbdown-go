# bbdown-go

A Go port of [BBDown](https://github.com/nilaoda/BBDown) — a minimal command-line downloader for Bilibili.

This is a deliberately smaller rewrite: it keeps only the features I actually use day-to-day (video/audio/subtitle download, quality selection, multi-part and batch download, authenticated access including purchased courses) and omits the rest. For the full feature set, please use the upstream project.

## Legal Disclaimer

**English.** This tool is provided for personal, research, and non-commercial use only. It is intended to help users retrieve content that they are themselves entitled to access under Bilibili's Terms of Service and the applicable law of their jurisdiction — for example, videos on their own account, content they have purchased, or content the copyright holder has explicitly authorized them to download. You are solely responsible for how you use this software and for complying with all relevant laws, platform terms, and copyright. Do not use this tool to download, redistribute, or commercially exploit content you do not have the right to access. The authors and contributors provide this software "as is", without warranty of any kind, and disclaim all liability for any direct or indirect consequences arising from its use or misuse.

**简体中文。** 本工具仅供个人学习、研究和非商业用途。用户应仅使用本工具获取其依据哔哩哔哩用户协议及所在司法管辖区适用法律有权访问的内容，例如自己账号下的视频、已购买的内容或版权方明确授权下载的内容。使用者需自行承担使用本工具的全部责任，并遵守所有相关法律法规、平台条款及著作权规定。严禁使用本工具下载、传播或商业利用任何无权访问的内容。本软件按"现状"提供，不作任何形式的担保；作者与贡献者对因使用或滥用本软件所产生的任何直接或间接后果概不负责。

## Attribution

I ported this project from [BBDown](https://github.com/nilaoda/BBDown) by [nilaoda](https://github.com/nilaoda). The API request flows, URL parsing rules, and login logic used here are derived from that project; all credit for the original reverse-engineering work belongs to the upstream authors.

This port is intentionally simpler than the upstream. It implements only the subset of features I personally need — basic video/audio/subtitle download, quality selection, multi-part and batch download, and authenticated access to private content and purchased courses. Features from upstream that are not included here (Dolby Vision handling, the international/Southeast-Asia API, danmaku, cover art, AI-subtitle downloads, aria2c integration, and so on) are out of scope; use the upstream project if you need them.

The Go implementation in this repository was written entirely by Claude (Anthropic) under my direction. Any bugs, omissions, or stylistic choices in the Go code are the responsibility of this port, not the upstream project.

## Status

Under active development. Track progress and open issues at <https://github.com/charleszheng44/bbdown-go/issues>. Expect breaking changes until the first tagged release.

## Prerequisites

- Go 1.25 or newer (to build or `go install`).
- [ffmpeg](https://ffmpeg.org/) available on your `PATH` (used for muxing video, audio, and subtitles into MP4).

## Install

```sh
go install github.com/charleszheng44/bbdown-go/cmd/bbdown@latest
```

The resulting `bbdown` binary will be placed in `$(go env GOBIN)` (or `$(go env GOPATH)/bin` if `GOBIN` is unset). Ensure that directory is on your `PATH`.

## Quick Start

```sh
bbdown login                 # scan the QR code with the Bilibili mobile app
bbdown <url>                 # download a video, bangumi episode, or course episode
```

Example:

```sh
bbdown https://www.bilibili.com/video/BV1xx411c7mD
```

## Command Reference

`bbdown` exposes four top-level forms. Run `bbdown --help` for the authoritative, up-to-date flag list.

| Command            | Purpose                                                |
|--------------------|--------------------------------------------------------|
| `bbdown login`     | Start QR-code login and persist cookies to disk.       |
| `bbdown logout`    | Delete the stored cookie file.                         |
| `bbdown parts <url>` | List page numbers, durations, and titles for a multi-part item. |
| `bbdown <url>`     | Download the given Bilibili URL (or ID).               |

Key flags for `bbdown <url>`:

| Flag               | Description                                                      |
|--------------------|------------------------------------------------------------------|
| `-p, --part`       | Part selector for multi-part videos: `1,3-5`, `ALL`, or `LAST`.  |
| `-q, --quality`    | Preferred quality name, e.g. `1080P 60`. Falls back to nearest.  |
| `-i, --interactive`| Interactive picker for quality.                                  |
| `-o, --output-dir` | Output directory (default: current directory).                   |
| `--video-only`     | Download only the video stream.                                  |
| `--audio-only`     | Download only the audio stream.                                  |
| `--sub-only`       | Download only subtitles.                                         |
| `--cookie`         | One-shot cookie string (`SESSDATA=...; bili_jct=...`).           |
| `--batch-file`     | Read one URL per line from the given file.                       |
| `--threads`        | Per-file download workers (default 8).                           |
| `--debug`          | Verbose logs and raw API response dumps.                         |

See `bbdown --help` and `bbdown <subcommand> --help` for the full list.

## Supported URL Types

| Kind    | Examples                                                         | Notes                                    |
|---------|------------------------------------------------------------------|------------------------------------------|
| Regular | `BV1xx411c7mD`, `av170001`, `https://www.bilibili.com/video/BV...` | Standard user-uploaded videos.           |
| Bangumi | `ep12345`, `ss1234`, `https://www.bilibili.com/bangumi/play/...` | Anime and licensed series.               |
| Course  | `https://www.bilibili.com/cheese/play/ep12345`                   | Paid courses; requires a purchased account. |

## Login and Cookies

After `bbdown login` succeeds, authentication cookies (`SESSDATA`, `bili_jct`, `DedeUserID`, `DedeUserID__ckMd5`) are written to:

```
$XDG_CONFIG_HOME/bbdown/cookies.json
```

On Linux this typically resolves to `~/.config/bbdown/cookies.json`; on macOS and Windows the equivalent per-user config directory is used. The file is created with mode `0600` (readable only by the owning user). Treat it as a secret — anyone with this file can access your Bilibili account.

To remove the stored cookies:

```sh
bbdown logout
```

For one-shot or CI use, pass `--cookie "SESSDATA=...; bili_jct=..."` instead of logging in; this bypasses the persisted cookie jar for that invocation only.

## Troubleshooting

- **`ffmpeg not found on PATH`.** Install ffmpeg from <https://ffmpeg.org/> (or your system package manager) and ensure the binary is reachable via `PATH`. `bbdown` detects ffmpeg at startup.
- **Rate limiting.** If Bilibili returns HTTP 412 or similar and `bbdown` reports a rate-limit error, wait a minute before retrying and reduce `--threads` or batch `--concurrency`.
- **Locked content.** `"This content requires a purchase or is region-locked"` means the authenticated account does not own the course or the content is not available in your region. Log in with an account that has access, or use a VPN/proxy from a supported region.
- **HTTPS proxy.** `bbdown` honours the standard `HTTPS_PROXY` (and `HTTP_PROXY`, `NO_PROXY`) environment variables. Example:

  ```sh
  HTTPS_PROXY=http://127.0.0.1:7890 bbdown <url>
  ```

- **Verbose diagnostics.** Re-run with `--debug` to print the raw API responses; include the output when filing a bug report.

## License

Released under the [MIT License](LICENSE). The upstream BBDown project is also MIT-licensed; its original copyright notice is preserved alongside ours inside `LICENSE`.
