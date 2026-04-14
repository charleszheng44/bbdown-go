// Package api implements the Bilibili web HTTP client used by bbdown-go.
//
// The package exposes a Client that wraps an *http.Client pre-loaded with the
// caller's authenticated cookie jar. Callers supply a parser.Target (from
// internal/parser) and receive a fully populated PlayInfo — metadata plus
// video, audio, and subtitle stream descriptors — or one of the exported
// sentinel errors.
//
// Three endpoint families are covered:
//
//   - Regular videos: x/web-interface/view + x/player/wbi/playurl (WBI signed).
//   - Bangumi:        pgc/view/web/season   + pgc/player/web/playurl.
//   - Cheese courses: pugv/view/web/season  + pugv/player/web/playurl.
//
// WBI signing is implemented in wbi.go. The derived mixin key is cached on
// the Client with a short TTL so that repeated requests within a session do
// not re-hit the nav endpoint.
//
// See docs/superpowers/specs/2026-04-13-bbdown-go-port-design.md §4, §6, §7.
package api
