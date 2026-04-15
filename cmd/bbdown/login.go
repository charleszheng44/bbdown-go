package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/charleszheng44/bbdown-go/internal/auth"
	"github.com/charleszheng44/bbdown-go/internal/config"
)

// newLoginCmd builds `bbdown login`, which runs the QR-code login flow and
// persists the resulting cookies to config.CookiesFile().
func newLoginCmd(_ *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:           "login",
		Short:         "Log in to Bilibili via QR code",
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

			cookies, err := auth.LoginQR(ctx, nil)
			if err != nil {
				return err
			}
			if err := auth.Store(path, cookies); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Login successful. Cookies saved to %s\n", path)
			return nil
		},
	}
}
