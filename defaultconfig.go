package main

import (
	_ "embed"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// defaultConfig is the shipped config.yaml, compiled into the binary so a
// lone zwiftboard.exe is self-sufficient: on first run it is written to
// %LocalAppData%\zwiftboard\config.yml for the user to edit.
//
//go:embed config.yaml
var defaultConfig []byte

// configDir is the per-user config directory: %LOCALAPPDATA%\zwiftboard on
// Windows (the exe folder is often read-only for a normal user — Program
// Files, Downloads), os.UserConfigDir()/zwiftboard elsewhere so dev machines
// and CI get a stable location regardless of where the binary runs. Overridable
// in tests.
var configDir = func() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "zwiftboard")
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "zwiftboard" // last resort: relative to cwd
	}
	return filepath.Join(base, "zwiftboard")
}

// defaultConfigPath is where the config lives by default; -config overrides it.
func defaultConfigPath() string {
	return filepath.Join(configDir(), "config.yml")
}

// defaultLegacyConfigPaths are the pre-AppData locations: config.yaml next to
// the executable, plus the cwd-relative one only for `go run` builds (whose
// executable lives in the temp dir, where a config beside it would be lost).
// A release exe must never adopt a config.yaml from whatever folder it was
// started in: an unrelated file copied into %LocalAppData% that fails to load
// would exit(1) on every start. Overridable in tests.
var defaultLegacyConfigPaths = func() []string {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	dir := filepath.Dir(exe)
	out := []string{filepath.Join(dir, "config.yaml")}
	if strings.HasPrefix(dir, filepath.Clean(os.TempDir())) {
		out = append(out, "config.yaml")
	}
	return out
}
var legacyConfigPaths = defaultLegacyConfigPaths

// ensureConfig migrates a legacy config once, then writes the embedded default
// to path when the file is missing. Never overwrites; failure is recoverable
// (the app runs in log-only mode).
func ensureConfig(path string) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			slog.Warn("cannot create config directory", "dir", dir, "err", err)
			return
		}
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		for _, legacy := range legacyConfigPaths() {
			if legacy == path {
				continue
			}
			b, rerr := os.ReadFile(legacy)
			if rerr != nil {
				continue
			}
			if werr := os.WriteFile(path, b, 0o644); werr != nil {
				slog.Warn("cannot migrate legacy config", "from", legacy, "to", path, "err", werr)
				return
			}
			slog.Info("migrated config to the new location", "from", legacy, "to", path)
			return
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if !os.IsExist(err) {
			slog.Warn("cannot create default config", "file", path, "err", err)
		}
		return
	}
	defer f.Close()
	if _, err := f.Write(defaultConfig); err != nil {
		slog.Warn("cannot write default config", "file", path, "err", err)
		return
	}
	slog.Info("created default config", "file", path)
}
