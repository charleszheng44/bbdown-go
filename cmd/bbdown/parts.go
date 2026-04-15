package main

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ErrPartSpec is returned when a --part specifier cannot be parsed.
var ErrPartSpec = errors.New("invalid --part specifier")

// parsePartSpec converts a --part specifier into a sorted, deduplicated list
// of 1-based page indices. total is the number of pages available; when 0 or
// negative, specifiers that depend on it ("ALL", "LAST", open ranges) return
// an error.
//
// Accepted forms (case-insensitive):
//
//	"ALL"             -> every page 1..total
//	"LAST"            -> [total]
//	"1,2,3"           -> [1,2,3]
//	"3-5"             -> [3,4,5]
//	"1,3-5,LAST"      -> [1,3,4,5,total]
//
// Whitespace around tokens is ignored. Duplicates are collapsed. Ranges must
// be non-inverted (lo <= hi). Pages outside [1, total] produce an error when
// total > 0.
func parsePartSpec(spec string, total int) ([]int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("%w: empty specifier", ErrPartSpec)
	}
	upper := strings.ToUpper(spec)
	if upper == "ALL" {
		if total <= 0 {
			return nil, fmt.Errorf("%w: ALL requires a known total", ErrPartSpec)
		}
		out := make([]int, total)
		for i := range out {
			out[i] = i + 1
		}
		return out, nil
	}

	seen := map[int]struct{}{}
	add := func(n int) error {
		if n < 1 {
			return fmt.Errorf("%w: page %d out of range", ErrPartSpec, n)
		}
		if total > 0 && n > total {
			return fmt.Errorf("%w: page %d exceeds total %d", ErrPartSpec, n, total)
		}
		seen[n] = struct{}{}
		return nil
	}

	for _, tok := range strings.Split(spec, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		utok := strings.ToUpper(tok)
		switch utok {
		case "LAST":
			if total <= 0 {
				return nil, fmt.Errorf("%w: LAST requires a known total", ErrPartSpec)
			}
			if err := add(total); err != nil {
				return nil, err
			}
			continue
		case "ALL":
			return nil, fmt.Errorf("%w: ALL must appear alone", ErrPartSpec)
		}

		if idx := strings.IndexByte(tok, '-'); idx >= 0 {
			loStr := strings.TrimSpace(tok[:idx])
			hiStr := strings.TrimSpace(tok[idx+1:])
			lo, err := strconv.Atoi(loStr)
			if err != nil {
				return nil, fmt.Errorf("%w: range low %q: %v", ErrPartSpec, loStr, err)
			}
			var hi int
			if strings.EqualFold(hiStr, "LAST") {
				if total <= 0 {
					return nil, fmt.Errorf("%w: LAST requires a known total", ErrPartSpec)
				}
				hi = total
			} else {
				hi, err = strconv.Atoi(hiStr)
				if err != nil {
					return nil, fmt.Errorf("%w: range high %q: %v", ErrPartSpec, hiStr, err)
				}
			}
			if lo > hi {
				return nil, fmt.Errorf("%w: inverted range %d-%d", ErrPartSpec, lo, hi)
			}
			for n := lo; n <= hi; n++ {
				if err := add(n); err != nil {
					return nil, err
				}
			}
			continue
		}

		n, err := strconv.Atoi(tok)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %v", ErrPartSpec, tok, err)
		}
		if err := add(n); err != nil {
			return nil, err
		}
	}

	if len(seen) == 0 {
		return nil, fmt.Errorf("%w: no pages selected", ErrPartSpec)
	}
	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Ints(out)
	return out, nil
}
