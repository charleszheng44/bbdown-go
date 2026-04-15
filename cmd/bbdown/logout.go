package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/charleszheng44/bbdown-go/internal/config"
)

// newLogoutCmd builds `bbdown logout`, which deletes the persisted cookie
// file. Absence of the file is treated as success.
func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "logout",
		Short:         "Delete the persisted cookie file",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := config.CookiesFile()
			if err != nil {
				return fmt.Errorf("resolve cookies path: %w", err)
			}
			if err := os.Remove(path); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					fmt.Fprintln(cmd.OutOrStdout(), "Already logged out.")
					return nil
				}
				return fmt.Errorf("delete cookies: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Logged out.")
			return nil
		},
	}
}
