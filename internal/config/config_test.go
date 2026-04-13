package config

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfigDirRespectsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")

	got, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	want := filepath.Join("/tmp/xdg-config", "bbdown")
	if got != want {
		t.Fatalf("ConfigDir = %q, want %q", got, want)
	}
}

func TestConfigDirFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	if runtime.GOOS == "windows" {
		t.Setenv("AppData", `C:\Users\test\AppData\Roaming`)
	} else {
		t.Setenv("HOME", "/home/test")
	}

	got, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}

	var want string
	switch runtime.GOOS {
	case "darwin":
		want = filepath.Join("/home/test", "Library", "Application Support", "bbdown")
	case "windows":
		want = filepath.Join(`C:\Users\test\AppData\Roaming`, "bbdown")
	default:
		want = filepath.Join("/home/test", ".config", "bbdown")
	}
	if got != want {
		t.Fatalf("ConfigDir = %q, want %q", got, want)
	}
}

func TestConfigDirEmptyXDGUsesFallback(t *testing.T) {
	// An empty (but set) XDG_CONFIG_HOME should be treated the same as
	// unset. The XDG Base Directory spec explicitly requires this.
	t.Setenv("XDG_CONFIG_HOME", "")
	if runtime.GOOS != "windows" {
		t.Setenv("HOME", "/home/test")
	} else {
		t.Setenv("AppData", `C:\Users\test\AppData\Roaming`)
	}

	got, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	if strings.Contains(got, "XDG_CONFIG_HOME") {
		t.Fatalf("ConfigDir = %q should not leak env name", got)
	}
}

func TestCacheDirRespectsXDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-cache")

	got, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	want := filepath.Join("/tmp/xdg-cache", "bbdown")
	if got != want {
		t.Fatalf("CacheDir = %q, want %q", got, want)
	}
}

func TestCacheDirFallback(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", `C:\Users\test\AppData\Local`)
	} else {
		t.Setenv("HOME", "/home/test")
	}

	got, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}

	var want string
	switch runtime.GOOS {
	case "darwin":
		want = filepath.Join("/home/test", "Library", "Caches", "bbdown")
	case "windows":
		want = filepath.Join(`C:\Users\test\AppData\Local`, "bbdown")
	default:
		want = filepath.Join("/home/test", ".cache", "bbdown")
	}
	if got != want {
		t.Fatalf("CacheDir = %q, want %q", got, want)
	}
}

func TestCookiesFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")

	got, err := CookiesFile()
	if err != nil {
		t.Fatalf("CookiesFile: %v", err)
	}
	want := filepath.Join("/tmp/xdg-config", "bbdown", "cookies.json")
	if got != want {
		t.Fatalf("CookiesFile = %q, want %q", got, want)
	}
}

func TestCookiesFileUsesConfigDir(t *testing.T) {
	// Whatever ConfigDir returns, CookiesFile must be its child.
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")

	cfg, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	ck, err := CookiesFile()
	if err != nil {
		t.Fatalf("CookiesFile: %v", err)
	}
	if filepath.Dir(ck) != cfg {
		t.Fatalf("CookiesFile parent = %q, want %q", filepath.Dir(ck), cfg)
	}
	if filepath.Base(ck) != "cookies.json" {
		t.Fatalf("CookiesFile base = %q, want %q", filepath.Base(ck), "cookies.json")
	}
}
