// Package config resolves XDG-style configuration and cache directories for
// bbdown and renders output filename templates. It depends only on the Go
// standard library.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// appDirName is the per-application subdirectory used under every base
// directory returned by this package.
const appDirName = "bbdown"

// ConfigDir returns the absolute path to bbdown's configuration directory.
//
// Resolution order:
//   - $XDG_CONFIG_HOME/bbdown, if XDG_CONFIG_HOME is set to a non-empty value.
//   - Per-OS fallback:
//   - darwin:  $HOME/Library/Application Support/bbdown
//   - windows: %AppData%/bbdown
//   - other:   $HOME/.config/bbdown
//
// The directory is not created; callers are expected to MkdirAll as needed.
func ConfigDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, appDirName), nil
	}
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", appDirName), nil
	case "windows":
		if v := os.Getenv("AppData"); v != "" {
			return filepath.Join(v, appDirName), nil
		}
		return "", errors.New("config: %AppData% is not set")
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".config", appDirName), nil
	}
}

// CacheDir returns the absolute path to bbdown's cache directory.
//
// Resolution order:
//   - $XDG_CACHE_HOME/bbdown, if XDG_CACHE_HOME is set to a non-empty value.
//   - Per-OS fallback:
//   - darwin:  $HOME/Library/Caches/bbdown
//   - windows: %LocalAppData%/bbdown
//   - other:   $HOME/.cache/bbdown
//
// The directory is not created; callers are expected to MkdirAll as needed.
func CacheDir() (string, error) {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(v, appDirName), nil
	}
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Caches", appDirName), nil
	case "windows":
		if v := os.Getenv("LocalAppData"); v != "" {
			return filepath.Join(v, appDirName), nil
		}
		return "", errors.New("config: %LocalAppData% is not set")
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".cache", appDirName), nil
	}
}

// CookiesFile returns the absolute path to the persisted cookie jar.
// It is always ConfigDir()/cookies.json.
func CookiesFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cookies.json"), nil
}
