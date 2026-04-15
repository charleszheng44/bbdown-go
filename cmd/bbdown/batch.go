package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// readBatchFile parses a batch file into a deduplicated slice of URLs.
//
// File format:
//   - one URL per line
//   - lines beginning with '#' (after optional whitespace) are comments
//   - blank lines are ignored
//   - duplicate URLs are collapsed to their first occurrence
func readBatchFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("batch: open %s: %w", path, err)
	}
	defer f.Close()

	var out []string
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(f)
	// Allow long URLs without enforcing the default 64 KiB per-line limit.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip any trailing inline comment introduced by a '#' preceded by
		// whitespace. URLs do not legitimately contain '#' except as a
		// fragment; treat a mid-line "  # ..." as a comment and leave
		// embedded '#' (no surrounding whitespace) untouched.
		if idx := strings.Index(line, " #"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
			if line == "" {
				continue
			}
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("batch: scan %s: %w", path, err)
	}
	return out, nil
}
