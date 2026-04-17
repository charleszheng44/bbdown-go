package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/charleszheng44/bbdown-go/internal/auth"
	"github.com/charleszheng44/bbdown-go/internal/config"
)

// newLoginCmd builds `bbdown login`. By default it runs the QR-code
// flow. For purchased cheese/bangumi content the QR-login cookies are
// often insufficient (Bilibili requires bili_ticket and buvid4, which
// the web passport does not hand back) — in that case use one of the
// cookie-import modes:
//
//	bbdown login --cookie '<paste string>'    # inline
//	bbdown login --cookie-file ~/cookie.txt   # from file
//	bbdown login --cookie-stdin               # from stdin (recommended)
//
// See the README for how to copy the cookie header from a logged-in
// browser request in DevTools.
func newLoginCmd(flags *rootFlags) *cobra.Command {
	var (
		cookieFile  string
		cookieStdin bool
	)
	cmd := &cobra.Command{
		Use:           "login",
		Short:         "Log in to Bilibili (QR code or import a browser cookie)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			path, err := config.CookiesFile()
			if err != nil {
				return fmt.Errorf("resolve cookies path: %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return fmt.Errorf("create config dir: %w", err)
			}

			raw, err := resolveImportedCookie(cmd.InOrStdin(), cmd.OutOrStdout(), flags.Cookie, cookieFile, cookieStdin)
			if err != nil {
				return err
			}

			var cookies auth.Cookies
			if raw != "" {
				cookies, err = auth.ParseCookieString(raw)
				if err != nil {
					return err
				}
				if cookies.SESSDATA == "" || cookies.BiliJCT == "" || cookies.DedeUserID == "" || cookies.DedeUserIDCkMd5 == "" {
					return fmt.Errorf("imported cookie must include SESSDATA, bili_jct, DedeUserID, and DedeUserID__ckMd5")
				}
			} else {
				cookies, err = auth.LoginQR(ctx, nil)
				if err != nil {
					return err
				}
			}
			if err := auth.Store(path, cookies); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Login successful. Cookies saved to %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&cookieFile, "cookie-file", "",
		"import cookies from a file whose contents are the DevTools cookie header value")
	cmd.Flags().BoolVar(&cookieStdin, "cookie-stdin", false,
		"read the cookie header value from stdin (terminated by EOF / Ctrl-D)")
	return cmd
}

// resolveImportedCookie returns the cookie string to persist, or "" if
// the caller wants the QR flow. At most one of flagCookie / cookieFile /
// cookieStdin may be set; zero means QR. An empty flagCookie ("" passed
// explicitly, e.g. --cookie=”) is treated as unset and also falls
// through to QR — callers that want to reject an explicit empty value
// should do so upstream.
func resolveImportedCookie(stdin io.Reader, promptOut io.Writer, flagCookie, cookieFile string, cookieStdin bool) (string, error) {
	set := 0
	if flagCookie != "" {
		set++
	}
	if cookieFile != "" {
		set++
	}
	if cookieStdin {
		set++
	}
	if set > 1 {
		return "", fmt.Errorf("at most one of --cookie, --cookie-file, --cookie-stdin may be used")
	}
	if flagCookie != "" {
		return flagCookie, nil
	}
	if cookieFile != "" {
		b, err := os.ReadFile(cookieFile)
		if err != nil {
			return "", fmt.Errorf("read --cookie-file: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	if cookieStdin {
		fmt.Fprintln(promptOut, "Paste the cookie header value from DevTools (Network → any request → Request Headers → cookie), then press Ctrl-D:")
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read cookie from stdin: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return "", nil
}
