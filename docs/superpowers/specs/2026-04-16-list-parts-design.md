# `bbdown parts` — Design Spec

**Date:** 2026-04-16
**Status:** Awaiting final user review
**Authors:** Claude (Opus 4.7), under direction of the repository owner

## 1. Overview

Add a new subcommand, `bbdown parts <url>`, that prints the list of pages/parts for a Bilibili item so the user can pick a `-p` / `--part` specifier without guessing. Output is a plain-text aligned table: page number, duration, title.

## 2. Motivation

The root command accepts `-p 1,3-5 | ALL | LAST`, but there is no way to discover how many parts exist or what each one is called before running a download. Users currently have to open the URL in a browser or start a download with `-p ALL` just to see the list. This subcommand closes that gap cheaply — the metadata is already returned by `api.Client.FetchPlayInfo`.

## 3. Scope

### In scope
- New subcommand `bbdown parts <url>` that works for every URL kind already supported by `parser.Classify` (regular `BV`/`av`, bangumi `ep`/`ss`, cheese course `ep`).
- Aligned table output with a title header and columns `P#  DURATION  TITLE`.
- Auth reuse: cookies from `--cookie` or the persisted cookie file, same as download.

### Out of scope (noted, not doing now)
- `--json` / machine-readable output.
- Listing available qualities or subtitle languages.
- Listing all episodes in a bangumi season or cheese course as a cross-episode index.
- Any change to the existing `--part` parsing behavior.

## 4. CLI shape

```
bbdown parts <url>
```

- Exactly one positional argument: the URL or ID. Zero or multiple positional arguments is an error.
- No subcommand-specific flags. Inherits persistent root flags; only `--cookie` and `--debug` are meaningful here. Flags that don't apply (`-p`, `-q`, `-i`, `--video-only`, `--audio-only`, `--sub-only`, `-o`, `--name`, `--multi-name`, `--threads`, `--concurrency`, `--batch-file`) are silently ignored — Cobra's persistent-flag model already permits this, and warning on each one adds noise without value.

### Example output

```
$ bbdown parts BV1xx411c7mD
Some Video Title
P#   DURATION  TITLE
 1    03:21    Opening
 2    12:45    Chapter 1 — Introduction
 3    18:02    Chapter 2 — Details
```

For a single-part item, one row is still printed so the output shape is uniform.

## 5. Architecture

### 5.1 Files

- **New:** `cmd/bbdown/parts_list.go` — `newPartsCmd(flags *rootFlags) *cobra.Command` plus the pure helpers below.
- **New:** `cmd/bbdown/parts_list_test.go` — unit tests for the pure helpers.
- **Edited:** `cmd/bbdown/root.go` — register `newPartsCmd(flags)` alongside `newLoginCmd` / `newLogoutCmd`.

No changes to `internal/` packages. The feature is a thin Cobra handler over existing code.

### 5.2 Reuse

The handler reuses:

- `loadCookies(flags)` from `download.go` for `--cookie` / persisted-cookie resolution.
- `api.NewClient(jar, "")` with the same cookie jar.
- `parser.Classify(rawURL)` to normalize the input.
- `client.FetchPlayInfo(ctx, target, 1)` — page 1 is enough; `info.Parts` enumerates every page.
- `formatError` / `debugMode` for error presentation (via `SilenceErrors`/`SilenceUsage` inherited from the root command).

### 5.3 Pure helpers (testable in isolation)

```go
// formatDuration returns "mm:ss" when seconds < 3600, else "hh:mm:ss".
// Negative or zero input renders as "--:--".
func formatDuration(seconds int) string

// renderParts formats a PlayInfo's title and Parts list as an aligned,
// printable table. Writes to w. Columns: P#, DURATION, TITLE.
func renderParts(w io.Writer, title string, parts []api.Part) error
```

Column alignment: compute the max width of each numeric/duration column over all rows (duration width is either 5 for `mm:ss` or 8 for `hh:mm:ss`, uniform per-invocation). Title is printed as-is; no truncation.

### 5.4 Handler flow

```
bbdown parts <url>
  → loadCookies(flags)
  → api.NewClient(jar, "")
  → parser.Classify(url)
  → client.FetchPlayInfo(ctx, target, 1)
  → renderParts(os.Stdout, info.Title, info.Parts)
```

Exactly one network round-trip in the happy path (the playinfo call). ffmpeg is NOT required and NOT checked.

## 6. Error handling

- Argument validation: `cobra.ExactArgs(1)`.
- `loadCookies` error → propagated (e.g. `auth.ErrNotLoggedIn` when neither `--cookie` nor a persisted file is available). Matches download behavior.
- `parser.Classify` error → propagated unchanged.
- `FetchPlayInfo` error (rate limit, unauthorized, locked content) → propagated; rendered by the existing `formatError` at the main entry point.
- Empty `info.Parts` despite a successful fetch: fall back to printing a synthetic single row — `Page=1`, `Duration=0` (renders as `--:--`), `Title=info.Title`. This preserves the "uniform shape" contract and only triggers on unusual upstream shapes. Implemented inside `renderParts` so the handler stays a single linear flow.

No new error types are introduced.

## 7. Testing

Unit tests in `parts_list_test.go`:

1. `formatDuration` — table-driven: `0`, `59`, `60`, `3599`, `3600`, `3661`, negative → `--:--`.
2. `renderParts` — three cases:
   - Single-part item: one row, title header present.
   - Multi-part item with sub-hour durations: `mm:ss` column, five chars wide.
   - Multi-part item where at least one duration ≥ 1h: `hh:mm:ss` column, eight chars wide (asserts uniform width per invocation).

No Cobra-wiring or HTTP test is added; the handler is thin glue, and the underlying `api` / `parser` packages are already covered.

## 8. Documentation

- `README.md` "Command Reference" table: add a row for `bbdown parts <url>` — "List page numbers, durations, and titles for a multi-part item."
- No separate doc page.

## 9. Branching and delivery

- Branch: `feat/parts-list` off `main` (already created as part of brainstorming).
- Commits: one for the subcommand + tests + README update, unless review surfaces something worth splitting.
- PR target: `main`.

## 10. Risks and open items

- **None identified.** The feature reuses existing code paths that already ship in the download flow; the only new surface is ~80 lines of CLI glue and a formatter.
