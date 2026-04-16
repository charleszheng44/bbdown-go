package main

import (
	"github.com/spf13/cobra"
)

// version is set via -ldflags "-X main.version=..." at release time.
var version = "dev"

// rootFlags holds the persistent and download-related flags parsed by cobra.
// Subcommands close over this struct; main.go wires the single instance.
type rootFlags struct {
	// Selection / quality.
	Part        string
	Quality     string
	Interactive bool
	VideoOnly   bool
	AudioOnly   bool
	SubOnly     bool

	// Output.
	OutputDir string
	Name      string
	MultiName string

	// Auth.
	Cookie string

	// Misc.
	Threads     int
	Concurrency int
	Debug       bool
	BatchFile   string
}

// debugMode mirrors rootFlags.Debug and is read by formatError. Exposed as a
// package-level variable so formatError can stay a pure function of err.
var debugMode bool

// newRootCmd constructs the top-level cobra command and its children.
func newRootCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "bbdown [flags] <url>",
		Short:         "Download Bilibili videos, bangumi episodes, and courses",
		Long:          "bbdown is a Go port of BBDown focused on a minimal, reliable CLI.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			debugMode = flags.Debug
			return runDownload(cmd.Context(), flags, args)
		},
	}

	// Selection and quality.
	cmd.PersistentFlags().StringVarP(&flags.Part, "part", "p", "",
		"page spec: 1,3-5 | ALL | LAST")
	cmd.PersistentFlags().StringVarP(&flags.Quality, "quality", "q", "",
		"preferred quality label, e.g. \"1080P 60\"")
	cmd.PersistentFlags().BoolVarP(&flags.Interactive, "interactive", "i", false,
		"interactive quality picker")
	cmd.PersistentFlags().BoolVar(&flags.VideoOnly, "video-only", false,
		"download only the video track (no audio, no mux)")
	cmd.PersistentFlags().BoolVar(&flags.AudioOnly, "audio-only", false,
		"download only the audio track")
	cmd.PersistentFlags().BoolVar(&flags.SubOnly, "sub-only", false,
		"download only subtitle files")

	// Output.
	cmd.PersistentFlags().StringVarP(&flags.OutputDir, "output-dir", "o", "",
		"output directory (default: current working directory)")
	cmd.PersistentFlags().StringVar(&flags.Name, "name", "<title>",
		"filename template for single-page items")
	cmd.PersistentFlags().StringVar(&flags.MultiName, "multi-name", "<title>/P<page>-<pageTitle>",
		"filename template for multi-page items")

	// Auth.
	cmd.PersistentFlags().StringVar(&flags.Cookie, "cookie", "",
		"one-shot cookie string, e.g. \"SESSDATA=...; bili_jct=...\"")

	// Misc.
	cmd.PersistentFlags().IntVar(&flags.Threads, "threads", 8,
		"per-file parallel download workers")
	cmd.PersistentFlags().IntVar(&flags.Concurrency, "concurrency", 2,
		"concurrent items when using --batch-file")
	cmd.PersistentFlags().BoolVar(&flags.Debug, "debug", false,
		"verbose logs on stderr, raw API dumps on errors")
	cmd.PersistentFlags().StringVar(&flags.BatchFile, "batch-file", "",
		"path to a file listing URLs to download (one per line)")

	cmd.AddCommand(newLoginCmd(flags))
	cmd.AddCommand(newLogoutCmd())
	cmd.AddCommand(newPartsCmd(flags))

	return cmd
}
