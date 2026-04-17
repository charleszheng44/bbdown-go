package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/charleszheng44/bbdown-go/internal/api"
	"github.com/charleszheng44/bbdown-go/internal/auth"
	"github.com/charleszheng44/bbdown-go/internal/config"
	"github.com/charleszheng44/bbdown-go/internal/download"
	"github.com/charleszheng44/bbdown-go/internal/mux"
	"github.com/charleszheng44/bbdown-go/internal/parser"
	"github.com/charleszheng44/bbdown-go/internal/planner"
)

// runDownload is the entrypoint for the root command. It handles both the
// single-URL and --batch-file paths.
func runDownload(ctx context.Context, flags *rootFlags, args []string) error {
	urls, err := collectURLs(flags, args)
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		return errors.New("no URL provided; pass a URL or --batch-file")
	}

	// ffmpeg is only needed when we are going to mux. If every selection
	// would skip muxing (video-only / audio-only / sub-only), fail early.
	if !flags.VideoOnly && !flags.AudioOnly && !flags.SubOnly {
		if err := mux.EnsureFFmpeg(ctx); err != nil {
			return err
		}
	}

	cookies, err := loadCookies(flags)
	if err != nil {
		return err
	}

	client := api.NewClient(cookies.AsJar(), "")
	if cookies.TV != nil {
		client.SetAppAuth(cookies.TV)
		appAuthConfigured = true
	}
	httpc := &http.Client{
		Jar:     cookies.AsJar(),
		Timeout: 0, // streamed downloads manage their own timeouts
	}

	if len(urls) == 1 {
		return processURL(ctx, client, httpc, flags, urls[0])
	}

	return runBatch(ctx, client, httpc, flags, urls)
}

// collectURLs merges --batch-file URLs and positional argv URLs into the
// dispatch list. When --batch-file is set, positional arguments are ignored.
func collectURLs(flags *rootFlags, args []string) ([]string, error) {
	if flags.BatchFile != "" {
		return readBatchFile(flags.BatchFile)
	}
	return args, nil
}

// loadCookies resolves the authenticated cookie set from the one-shot
// --cookie flag when present, else the persisted cookie file. Returns
// auth.ErrNotLoggedIn when neither source is available.
func loadCookies(flags *rootFlags) (auth.Cookies, error) {
	if flags.Cookie != "" {
		return auth.ParseCookieString(flags.Cookie)
	}
	path, err := config.CookiesFile()
	if err != nil {
		return auth.Cookies{}, err
	}
	return auth.Load(path)
}

// runBatch dispatches urls through a bounded worker pool sized by
// flags.Concurrency.
func runBatch(ctx context.Context, client *api.Client, httpc *http.Client, flags *rootFlags, urls []string) error {
	workers := flags.Concurrency
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for _, u := range urls {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(url string) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := processURL(ctx, client, httpc, flags, url); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				fmt.Fprintf(os.Stderr, "%s: %s\n", url, formatError(err))
			}
		}(u)
	}
	wg.Wait()
	return firstErr
}

// processURL runs the classify → fetch → plan → download → mux pipeline for
// a single URL, honoring flags.Part for multi-page selection.
func processURL(ctx context.Context, client *api.Client, httpc *http.Client, flags *rootFlags, rawURL string) error {
	target, err := parser.Classify(rawURL)
	if err != nil {
		return err
	}

	// Fetch the first page to learn how many parts exist, then re-resolve
	// per-page stream info for each selected part.
	baseInfo, err := client.FetchPlayInfo(ctx, target, 1)
	if err != nil {
		return err
	}
	total := len(baseInfo.Parts)
	if total == 0 {
		total = 1
	}

	spec := flags.Part
	if spec == "" {
		spec = "1"
	}
	pages, err := parsePartSpec(spec, total)
	if err != nil {
		return err
	}
	if len(pages) == 0 {
		pages = []int{1}
	}
	multi := len(pages) > 1

	template := flags.Name
	if multi {
		template = flags.MultiName
	}

	prefs := planner.Prefs{
		Quality:     flags.Quality,
		CodecOrder:  []string{"hevc", "av1", "avc"},
		VideoOnly:   flags.VideoOnly,
		AudioOnly:   flags.AudioOnly,
		Interactive: flags.Interactive,
	}

	for _, page := range pages {
		if err := ctx.Err(); err != nil {
			return err
		}
		info := baseInfo
		if page != 1 {
			info, err = client.FetchPlayInfo(ctx, target, page)
			if err != nil {
				return err
			}
		}
		if err := processPart(ctx, client, httpc, flags, prefs, template, info, page); err != nil {
			return err
		}
	}
	return nil
}

// processPart runs the download / mux pipeline for a single resolved page.
func processPart(ctx context.Context, client *api.Client, httpc *http.Client, flags *rootFlags, prefs planner.Prefs, template string, info api.PlayInfo, page int) error {
	pageTitle := ""
	if page-1 >= 0 && page-1 < len(info.Parts) {
		pageTitle = info.Parts[page-1].Title
	}

	sel, err := planner.Pick(info, prefs)
	if err != nil {
		return err
	}

	outDir := flags.OutputDir
	if outDir == "" {
		outDir, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	rendered := config.Render(template, config.TemplateVars{
		Title:     info.Title,
		Page:      page,
		PageTitle: pageTitle,
		BVID:      info.BVID,
		AID:       info.AID,
		Quality:   sel.Quality,
		Codec:     sel.Codec,
	})
	finalPath := filepath.Join(outDir, rendered+".mp4")
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// Sub-only short-circuits everything else.
	if flags.SubOnly {
		return saveSubtitles(ctx, client, info, finalPath)
	}

	tmpDir, err := makeTempDir(info.CID, page)
	if err != nil {
		return err
	}
	keepTmp := flags.Debug
	defer func() {
		if !keepTmp {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	videoPath := filepath.Join(tmpDir, "video.m4v")
	audioPath := filepath.Join(tmpDir, "audio.m4a")
	subPath := ""

	dlOpts := download.Options{
		Threads:   flags.Threads,
		UserAgent: api.DefaultUserAgent,
		Headers:   http.Header{"Referer": []string{"https://www.bilibili.com"}},
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 3)

	if sel.Video != nil && !flags.AudioOnly {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := download.Fetch(ctx, httpc, sel.Video.BaseURL, videoPath, dlOpts); err != nil {
				errCh <- fmt.Errorf("video: %w", err)
			}
		}()
	} else {
		videoPath = ""
	}

	if sel.Audio != nil && !flags.VideoOnly {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := download.Fetch(ctx, httpc, sel.Audio.BaseURL, audioPath, dlOpts); err != nil {
				errCh <- fmt.Errorf("audio: %w", err)
			}
		}()
	} else {
		audioPath = ""
	}

	if len(info.Subtitles) > 0 && !flags.VideoOnly && !flags.AudioOnly {
		subPath = filepath.Join(tmpDir, "subtitle.srt")
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fetchSubtitleFile(ctx, client, info.Subtitles[0].URL, subPath); err != nil {
				// Subtitles are optional; log but do not fail the download.
				if flags.Debug {
					fmt.Fprintf(os.Stderr, "subtitle fetch failed: %v\n", err)
				}
				subPath = ""
			}
		}()
	}

	wg.Wait()
	close(errCh)
	if err, ok := <-errCh; ok {
		keepTmp = flags.Debug
		return err
	}

	// --video-only / --audio-only skip mux; just move the file into place.
	if flags.VideoOnly {
		return moveTo(videoPath, replaceExt(finalPath, ".m4v"))
	}
	if flags.AudioOnly {
		return moveTo(audioPath, replaceExt(finalPath, ".m4a"))
	}

	inputs := mux.Inputs{Video: videoPath, Audio: audioPath, Subtitle: subPath}
	if err := mux.Combine(ctx, inputs, finalPath); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Saved %s\n", finalPath)
	return nil
}

// saveSubtitles writes every available subtitle track next to finalPath.
func saveSubtitles(ctx context.Context, client *api.Client, info api.PlayInfo, finalPath string) error {
	if len(info.Subtitles) == 0 {
		return errors.New("no subtitles available for this item")
	}
	base := stripExt(finalPath)
	for _, s := range info.Subtitles {
		dst := fmt.Sprintf("%s.%s.srt", base, safeLang(s.Lang))
		if err := fetchSubtitleFile(ctx, client, s.URL, dst); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "Saved %s\n", dst)
	}
	return nil
}

// fetchSubtitleFile downloads a BCC subtitle URL and writes the SRT bytes to dst.
func fetchSubtitleFile(ctx context.Context, client *api.Client, url, dst string) error {
	srt, err := client.FetchSubtitle(ctx, url)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, srt, 0o644)
}

// makeTempDir creates a per-part working directory under CacheDir()/tmp.
func makeTempDir(cid string, page int) (string, error) {
	cache, err := config.CacheDir()
	if err != nil {
		return "", err
	}
	jobID := fmt.Sprintf("%s-p%d-%d", cid, page, time.Now().UnixNano())
	dir := filepath.Join(cache, "tmp", jobID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create tmp dir: %w", err)
	}
	return dir, nil
}

// moveTo renames src to dst, falling back to copy+delete across filesystems.
func moveTo(src, dst string) error {
	if src == "" {
		return errors.New("nothing to move")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		fmt.Fprintf(os.Stdout, "Saved %s\n", dst)
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	_ = os.Remove(src)
	fmt.Fprintf(os.Stdout, "Saved %s\n", dst)
	return nil
}

// replaceExt swaps the extension of path with ext (which must start with '.').
func replaceExt(path, ext string) string {
	return stripExt(path) + ext
}

// stripExt returns path without its extension.
func stripExt(path string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return path
	}
	return path[:len(path)-len(ext)]
}

// safeLang reduces a Bilibili language tag to a filesystem-safe suffix.
func safeLang(lang string) string {
	if lang == "" {
		return "sub"
	}
	// Best-effort: strip obvious path and windows-hostile characters.
	out := make([]byte, 0, len(lang))
	for i := 0; i < len(lang); i++ {
		c := lang[i]
		switch c {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', ' ':
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return "sub"
	}
	return string(out)
}
