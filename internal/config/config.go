// Package config provides application paths and configuration defaults.
package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// AppName is the user-facing application name.
const AppName = "Meshed Server Tool"

// AppAuthor is the author/organization key used for platform-specific data dirs.
const AppAuthor = "Skomesh"

// DefaultDataDir returns the platform-appropriate user data directory.
//
//   - Linux:   $XDG_DATA_HOME/meshed-server-tool or ~/.local/share/meshed-server-tool
//   - macOS:   ~/Library/Application Support/Meshed Server Tool
//   - Windows: %LocalAppData%\Skomesh\Meshed Server Tool
func DefaultDataDir() string {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LocalAppData")
		if base == "" {
			base = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		return filepath.Join(base, AppAuthor, AppName)
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", AppName)
	default: // linux, freebsd, etc.
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "meshed-server-tool")
		}
		return filepath.Join(os.Getenv("HOME"), ".local", "share", "meshed-server-tool")
	}
}

// DefaultListenAddr is the default HTTP listen address.
const DefaultListenAddr = ":5000"
