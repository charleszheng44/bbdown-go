# Download progress bar — design

Status: approved 2026-05-03. Author: Claude (under direction).

## Goals

- While `bbdown <url>` is running, show stacked per-stream progress bars
  (video + audio) with bytes downloaded, percent, speed, and ETA.
- Degrade cleanly when stdout is not a TTY (pipes, log files, CI).
- No new behavior in the actual download path — only render what
  `internal/download.Fetch`'s existing `OnProgress` callback already emits.

## Non-goals

- Subtitle progress. BCC JSON is KB-scale and finishes before a bar would
  draw a meaningful frame.
- ffmpeg / mux progress. Separate problem with a separate output stream.
- Cross-URL aggregation in batch mode. Batch processes URLs sequentially;
  each URL gets its own bars.
- JSON / machine-readable progress output. Can be added under
  `--progress=json` later if anyone asks.

## Architecture

One new package, one wiring change in `cmd/bbdown`, one new flag.

```
cmd/bbdown/
  root.go      ← +flag: --progress=auto|always|never|plain (default "auto")
  download.go  ← per-part: build progress.Manager, Track("video"/"audio"),
                 wire dlOpts.OnProgress = tracker.Update, defer mgr.Wait()
internal/
  progress/
    progress.go       ← Manager, Tracker interfaces; mpb-backed,
                        plain-text, and no-op implementations
    progress_test.go
go.mod  ← + github.com/vbauerster/mpb/v8 (and 2 transitive deps)
```

Output goes to **stderr**, not stdout, so `bbdown <url> > out.log` keeps
progress on the screen but the redirected log clean. Existing
`Saved <path>` lines stay on stdout. This matches curl / wget / yt-dlp.

## Public interface (`internal/progress`)

```go
package progress

type Mode int
const (
    ModeAuto   Mode = iota // animated if TTY, plain otherwise
    ModeAlways              // animated regardless
    ModeNever                // no output
    ModePlain                // periodic plain lines regardless
)

// ParseMode parses the user-facing flag value.
func ParseMode(s string) (Mode, error)

// Manager owns the rendering surface for one URL. One per call to
// processPart.
type Manager interface {
    // Track registers a new bar. label is "video", "audio", etc.
    // total may be -1 if unknown at registration time; the tracker
    // accepts a corrected total in its first Update call.
    Track(label string, total int64) Tracker

    // Println writes a line above the bars (e.g. "Part 1/3: title").
    // Safe to call even when no bars are active.
    Println(format string, args ...any)

    // Wait flushes all bars to a final state and releases the
    // rendering surface. Idempotent.
    Wait()
}

// Tracker matches the shape internal/download.Options.OnProgress expects.
type Tracker interface {
    Update(downloaded, total int64) // OnProgress signature
    Abort()                          // mark this stream as failed
}

// New picks the implementation based on mode and whether out is a TTY.
func New(out io.Writer, mode Mode) Manager
```

Three implementations behind the interface:

- `mpbManager` — wraps `mpb.Progress`. Used when `mode == ModeAlways`, or
  `mode == ModeAuto && isTerminal(out)`. Bar layout:
  `<label>  [#####     ]  62%  120/195 MiB  3.2 MiB/s  eta 24s`.
- `plainManager` — emits one line per stream every 5 seconds (and on
  completion / abort), e.g. `video: 45% (88/195 MiB) eta 1m12s`. Used when
  `mode == ModePlain`, or `mode == ModeAuto && !isTerminal(out)`.
- `noopManager` — discards everything. Used when `mode == ModeNever`.

`isTerminal` uses `golang.org/x/term.IsTerminal(int(f.Fd()))` (already a
transitive dep via qrterminal). `out` is asserted to `*os.File`; if it
isn't, fall back to plain mode.

## Wiring in `cmd/bbdown/download.go`

Inside `processPart` (around the existing `dlOpts` build at line 234):

```go
mgr := progress.New(os.Stderr, flags.ProgressMode)
defer mgr.Wait()

if multiPart {
    mgr.Println("Part %d/%d: %s", page, total, title)
}

videoTr := mgr.Track("video", videoSize) // size if known, -1 otherwise
audioTr := mgr.Track("audio", audioSize)

videoOpts := dlOpts; videoOpts.OnProgress = videoTr.Update
audioOpts := dlOpts; audioOpts.OnProgress = audioTr.Update
```

Concurrency: `internal/download.Options.OnProgress` already promises
"safe for concurrent use" — `mpb.Bar.SetCurrent` is goroutine-safe.

Failure path: a stream goroutine that returns an error calls
`tracker.Abort()` before pushing to `errCh`. The deferred `mgr.Wait()`
finalizes the surface so cobra's error print doesn't tangle with a
half-rendered bar.

Subtitle goroutine remains unchanged — no tracker, no bar.

## Flag

`root.go` adds one persistent flag:

```
--progress string   Progress display: auto|always|never|plain (default "auto")
```

Validated at flag-parse time via `progress.ParseMode`. Invalid values
produce a normal cobra usage error.

## Testing

- `progress.ParseMode` — table test, valid + invalid inputs.
- `noopManager` — `Track` returns a tracker whose `Update` and `Abort`
  don't panic; `Println` writes nothing.
- `plainManager` — feed a fake clock + buffered writer, advance time
  across the 5-second tick boundary, assert rendered lines match.
  Covers the format and the throttle.
- `mpbManager` — not unit-tested for rendered output; mpb is the SUT we
  trust. One smoke test in `cmd/bbdown` that runs a download against an
  httptest server with `--progress=never` and asserts the file lands
  (regression for the `dlOpts.OnProgress` wiring).

## Dependencies added

`github.com/vbauerster/mpb/v8` plus its two transitive deps
(`VividCortex/ewma` for ETA smoothing, `acarl005/stripansi` for width
math). All MIT, all small. Net add: `go.mod` require + `go.sum` churn.

## YAGNI / explicitly out of scope

- Multi-URL aggregate bar.
- Subtitle bar.
- Mux / ffmpeg progress.
- Persistent bar across parts. Each part's bars finalize before the
  next part starts.
- `--progress=json` machine-readable mode.
