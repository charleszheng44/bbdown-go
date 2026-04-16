# `bbdown parts` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `bbdown parts <url>` subcommand that prints an aligned table of the item's page number, duration, and title so users can pick a `-p` / `--part` specifier without guessing.

**Architecture:** Thin Cobra subcommand over existing code. Handler reuses `loadCookies` → `api.NewClient` → `parser.Classify` → `client.FetchPlayInfo` and pipes `info.Parts` through two pure helpers (`formatDuration`, `renderParts`). No changes to `internal/` packages.

**Tech Stack:** Go 1.25, `github.com/spf13/cobra`, `text/tabwriter` (stdlib) for column alignment.

**Spec:** `docs/superpowers/specs/2026-04-16-list-parts-design.md`

**Branch:** `feat/parts-list` (already checked out).

---

## File Structure

- **Create:** `cmd/bbdown/parts_list.go` — `newPartsCmd`, `formatDuration`, `renderParts`.
- **Create:** `cmd/bbdown/parts_list_test.go` — unit tests for the two pure helpers.
- **Modify:** `cmd/bbdown/root.go` — register `newPartsCmd(flags)` next to `newLoginCmd` / `newLogoutCmd`.
- **Modify:** `README.md` — add a row in the Command Reference table.

Why `text/tabwriter`: the problem is textbook tabular alignment and `tabwriter` handles mixed-width runes (CJK titles are common on Bilibili) far more reliably than hand-computed column widths.

> Note on `parts_list.go` naming: the repo already has `cmd/bbdown/parts.go` (the `--part` spec parser). The new file is deliberately named `parts_list.go` so the two live side by side without collision.

---

### Task 1: Pure helper `formatDuration`

**Files:**
- Create: `cmd/bbdown/parts_list.go`
- Test: `cmd/bbdown/parts_list_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/bbdown/parts_list_test.go`:

```go
package main

import "testing"

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{-1, "--:--"},
		{0, "--:--"},
		{1, "00:01"},
		{59, "00:59"},
		{60, "01:00"},
		{3599, "59:59"},
		{3600, "01:00:00"},
		{3661, "01:01:01"},
		{36000, "10:00:00"},
	}
	for _, tc := range cases {
		got := formatDuration(tc.in)
		if got != tc.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/bbdown/ -run TestFormatDuration`
Expected: build failure — `undefined: formatDuration`.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/bbdown/parts_list.go`:

```go
package main

import "fmt"

// formatDuration renders a duration in seconds as mm:ss when under one hour
// and hh:mm:ss otherwise. Zero or negative input renders as "--:--" (used
// when the upstream API omitted the duration).
func formatDuration(seconds int) string {
	if seconds <= 0 {
		return "--:--"
	}
	if seconds < 3600 {
		return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, (seconds%3600)/60, seconds%60)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/bbdown/ -run TestFormatDuration`
Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
gofmt -w .
git add cmd/bbdown/parts_list.go cmd/bbdown/parts_list_test.go
git commit -m "feat(cmd): add formatDuration helper for parts listing"
```

---

### Task 2: Pure helper `renderParts`

**Files:**
- Modify: `cmd/bbdown/parts_list.go`
- Test: `cmd/bbdown/parts_list_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `cmd/bbdown/parts_list_test.go`:

```go
import (
	"bytes"
	"strings"

	"github.com/charleszheng44/bbdown-go/internal/api"
)

func TestRenderPartsSinglePart(t *testing.T) {
	var buf bytes.Buffer
	parts := []api.Part{
		{Page: 1, CID: "1", Title: "Only page", Duration: 125},
	}
	if err := renderParts(&buf, "My Title", parts); err != nil {
		t.Fatalf("renderParts: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "My Title\n") {
		t.Errorf("missing title header; got:\n%s", out)
	}
	if !strings.Contains(out, "P#") || !strings.Contains(out, "DURATION") || !strings.Contains(out, "TITLE") {
		t.Errorf("missing column header; got:\n%s", out)
	}
	if !strings.Contains(out, "02:05") || !strings.Contains(out, "Only page") {
		t.Errorf("missing row content; got:\n%s", out)
	}
}

func TestRenderPartsMultipleParts(t *testing.T) {
	var buf bytes.Buffer
	parts := []api.Part{
		{Page: 1, Title: "Opening", Duration: 201},
		{Page: 2, Title: "Chapter 1", Duration: 765},
		{Page: 3, Title: "Chapter 2", Duration: 1082},
	}
	if err := renderParts(&buf, "Series", parts); err != nil {
		t.Fatalf("renderParts: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Series", "03:21", "12:45", "18:02", "Opening", "Chapter 1", "Chapter 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// title + header + 3 rows
	if len(lines) != 5 {
		t.Errorf("want 5 lines, got %d:\n%s", len(lines), out)
	}
}

func TestRenderPartsOverOneHour(t *testing.T) {
	var buf bytes.Buffer
	parts := []api.Part{
		{Page: 1, Title: "Short", Duration: 120},
		{Page: 2, Title: "Long", Duration: 3725}, // 01:02:05
	}
	if err := renderParts(&buf, "Mixed", parts); err != nil {
		t.Fatalf("renderParts: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "01:02:05") {
		t.Errorf("expected hh:mm:ss row; got:\n%s", out)
	}
	// Both rows must use hh:mm:ss once any row crosses an hour.
	if !strings.Contains(out, "00:02:00") {
		t.Errorf("expected sub-hour row promoted to hh:mm:ss; got:\n%s", out)
	}
}

func TestRenderPartsEmptyFallback(t *testing.T) {
	var buf bytes.Buffer
	if err := renderParts(&buf, "Solo", nil); err != nil {
		t.Fatalf("renderParts: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Solo") {
		t.Errorf("missing title; got:\n%s", out)
	}
	if !strings.Contains(out, "--:--") {
		t.Errorf("empty Parts should render a synthetic row with --:--; got:\n%s", out)
	}
}
```

> The test file's `import` block above shows the full set after this edit. When appending, merge the new imports into the existing `import` statement rather than adding a second block.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/bbdown/ -run TestRenderParts`
Expected: build failure — `undefined: renderParts`.

- [ ] **Step 3: Write minimal implementation**

Append to `cmd/bbdown/parts_list.go`:

```go
import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/charleszheng44/bbdown-go/internal/api"
)

// renderParts writes title on the first line, then an aligned table of
// Page #, duration, and title — one row per Part. If parts is empty, a
// single synthetic row is emitted so callers always see a uniform shape.
//
// Duration column width is uniform per invocation: if any part is >= 1h,
// every row renders as hh:mm:ss; otherwise mm:ss.
func renderParts(w io.Writer, title string, parts []api.Part) error {
	if _, err := fmt.Fprintln(w, title); err != nil {
		return err
	}

	if len(parts) == 0 {
		parts = []api.Part{{Page: 1, Duration: 0, Title: title}}
	}

	useHours := false
	for _, p := range parts {
		if p.Duration >= 3600 {
			useHours = true
			break
		}
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "P#\tDURATION\tTITLE"); err != nil {
		return err
	}
	for _, p := range parts {
		dur := formatDuration(p.Duration)
		if useHours && p.Duration > 0 && p.Duration < 3600 {
			dur = fmt.Sprintf("00:%s", dur) // promote mm:ss -> 00:mm:ss
		}
		if _, err := fmt.Fprintf(tw, "%d\t%s\t%s\n", p.Page, dur, p.Title); err != nil {
			return err
		}
	}
	return tw.Flush()
}
```

Merge the new imports with the existing `import "fmt"` in the file so there's one import block.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/bbdown/ -run TestRenderParts -v`
Expected: all four `TestRenderParts*` cases PASS.

- [ ] **Step 5: Run the full package tests**

Run: `go test ./cmd/bbdown/`
Expected: PASS (no regressions to existing tests).

- [ ] **Step 6: Commit**

```bash
gofmt -w .
git add cmd/bbdown/parts_list.go cmd/bbdown/parts_list_test.go
git commit -m "feat(cmd): add renderParts tabular formatter"
```

---

### Task 3: `newPartsCmd` Cobra handler

**Files:**
- Modify: `cmd/bbdown/parts_list.go`

- [ ] **Step 1: Add the handler**

Append to `cmd/bbdown/parts_list.go`. Merge the new imports (`context`, `os`, `github.com/charleszheng44/bbdown-go/internal/parser`, `github.com/spf13/cobra`) into the existing import block so there's still exactly one `import (...)` group in the file:

```go
// newPartsCmd returns the `bbdown parts <url>` subcommand. It prints an
// aligned page / duration / title table for the given URL so the user can
// build a --part specifier. Reuses the same cookie + client setup as the
// download path.
func newPartsCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:           "parts <url>",
		Short:         "List the pages of a Bilibili item",
		Long:          "Fetch metadata for the given URL or ID and print page number, duration, and title for each page.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			debugMode = flags.Debug
			return runParts(cmd.Context(), flags, args[0])
		},
	}
}

func runParts(ctx context.Context, flags *rootFlags, rawURL string) error {
	cookies, err := loadCookies(flags)
	if err != nil {
		return err
	}
	client := api.NewClient(cookies.AsJar(), "")
	target, err := parser.Classify(rawURL)
	if err != nil {
		return err
	}
	info, err := client.FetchPlayInfo(ctx, target, 1)
	if err != nil {
		return err
	}
	return renderParts(os.Stdout, info.Title, info.Parts)
}
```

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 3: Run the full package tests**

Run: `go test ./cmd/bbdown/`
Expected: PASS — the previous helper tests still pass; no new tests yet for the Cobra glue.

- [ ] **Step 4: Commit**

```bash
gofmt -w .
git add cmd/bbdown/parts_list.go
git commit -m "feat(cmd): add parts subcommand handler"
```

---

### Task 4: Wire `parts` into the root command

**Files:**
- Modify: `cmd/bbdown/root.go:92-94`

- [ ] **Step 1: Register the subcommand**

In `cmd/bbdown/root.go`, find:

```go
	cmd.AddCommand(newLoginCmd(flags))
	cmd.AddCommand(newLogoutCmd())

	return cmd
```

Replace with:

```go
	cmd.AddCommand(newLoginCmd(flags))
	cmd.AddCommand(newLogoutCmd())
	cmd.AddCommand(newPartsCmd(flags))

	return cmd
```

- [ ] **Step 2: Verify the subcommand is visible**

Run: `go run ./cmd/bbdown --help`
Expected output contains:

```
Available Commands:
  ...
  parts       List the pages of a Bilibili item
  ...
```

- [ ] **Step 3: Verify arg validation**

Run: `go run ./cmd/bbdown parts`
Expected: exits non-zero with a Cobra message about requiring exactly 1 argument.

Run: `go run ./cmd/bbdown parts a b`
Expected: same — "accepts 1 arg(s), received 2".

- [ ] **Step 4: Run the full test suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w .
git add cmd/bbdown/root.go
git commit -m "feat(cmd): register parts subcommand on root"
```

---

### Task 5: Update README Command Reference

**Files:**
- Modify: `README.md:55-59`

- [ ] **Step 1: Add the row**

In `README.md`, find the Command Reference table:

```
| Command            | Purpose                                                |
|--------------------|--------------------------------------------------------|
| `bbdown login`     | Start QR-code login and persist cookies to disk.       |
| `bbdown logout`    | Delete the stored cookie file.                         |
| `bbdown <url>`     | Download the given Bilibili URL (or ID).               |
```

Replace with:

```
| Command            | Purpose                                                |
|--------------------|--------------------------------------------------------|
| `bbdown login`     | Start QR-code login and persist cookies to disk.       |
| `bbdown logout`    | Delete the stored cookie file.                         |
| `bbdown parts <url>` | List page numbers, durations, and titles for a multi-part item. |
| `bbdown <url>`     | Download the given Bilibili URL (or ID).               |
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs(readme): document bbdown parts subcommand"
```

---

### Task 6: End-to-end smoke verification

**Files:** none (manual verification).

This task produces no code. It exists to verify the feature works end-to-end against the real Bilibili API before the branch is handed off.

- [ ] **Step 1: Build the binary**

Run: `go build -o /tmp/bbdown ./cmd/bbdown`
Expected: binary at `/tmp/bbdown`.

- [ ] **Step 2: Run against a known multi-part video**

Run: `/tmp/bbdown parts BV1xx411c7mD`

Expected output shape (exact titles/durations will differ):

```
<title>
P#  DURATION  TITLE
1   03:21     <part title>
2   12:45     <part title>
...
```

- [ ] **Step 3: Run against a single-part video**

Pick any single-page `BV`. Expected: the header plus exactly one row.

- [ ] **Step 4: Confirm exit code is 0**

Run: `echo $?` immediately after step 2.
Expected: `0`.

- [ ] **Step 5: No commit**

Step 6 is a verification task; nothing to commit.

> If steps 2-4 fail for a reason unrelated to network/auth (e.g. a real bug in the handler), stop and report the failure — do not paper over it with a retry.

---

## Self-Review

**Spec coverage:**

| Spec section | Covered by |
|---|---|
| §4 CLI shape, ExactArgs(1), no new flags | Task 3 (`cobra.ExactArgs(1)`, no flag registration), Task 4 (registration) |
| §5.1 Files (parts_list.go, parts_list_test.go, root.go edit) | Tasks 1-4 |
| §5.2 Reuse (loadCookies, api.NewClient, parser.Classify, FetchPlayInfo) | Task 3 |
| §5.3 Pure helpers formatDuration / renderParts | Tasks 1 and 2 |
| §5.4 Handler flow | Task 3 |
| §6 Error handling + empty-Parts fallback | Task 2 (fallback in renderParts), Task 3 (propagation) |
| §7 Unit tests | Tasks 1 (formatDuration) and 2 (renderParts × 4 cases incl. single, multi, hh:mm:ss, empty) |
| §8 README update | Task 5 |
| §9 Branching | Branch already on `feat/parts-list` |

**Placeholder scan:** No TBDs, TODOs, "handle edge cases", or "similar to above" references. Every code block is complete.

**Type consistency:**
- `formatDuration(seconds int) string` — same signature Task 1 → Task 2 usage.
- `renderParts(w io.Writer, title string, parts []api.Part) error` — same signature Task 2 → Task 3.
- `newPartsCmd(flags *rootFlags) *cobra.Command` — defined Task 3, used Task 4.
- `runParts(ctx, flags, rawURL)` — defined and used only in Task 3.
- `authCookies = auth.Cookies` — defined and used within Task 3 only.
- `api.Part{Page, CID, Title, Duration}` — fields match `internal/api/types.go:35-44`.

All signatures line up.
