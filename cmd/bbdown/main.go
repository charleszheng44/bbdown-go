// Command bbdown is the cobra-based command-line entry point for bbdown-go,
// a Go port of BBDown focused on a minimal, reliable Bilibili downloader.
//
// See docs/superpowers/specs/2026-04-13-bbdown-go-port-design.md §4, §8, §9
// for the architecture and flag reference this package implements.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// exit codes.
const (
	exitOK       = 0
	exitErr      = 1
	exitCanceled = 130
)

func main() {
	os.Exit(run())
}

// run is main() split out so tests (or deferred cleanups) can exercise the
// top-level wiring without calling os.Exit.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	flags := &rootFlags{}
	cmd := newRootCmd(flags)
	cmd.SetContext(ctx)

	err := cmd.ExecuteContext(ctx)
	if err == nil {
		return exitOK
	}

	// SIGINT / SIGTERM -> 130, per POSIX convention.
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		fmt.Fprintln(os.Stderr, "canceled")
		return exitCanceled
	}

	msg := formatError(err)
	if msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	return exitErr
}
