package main

import (
	_ "embed"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// defaultConfig is the shipped config.yaml, compiled into the binary so a
// lone zwiftboard.exe is self-sufficient: on first run it is written next to
// the program for the user to edit.
//
//go:embed config.yaml
var defaultConfig []byte

// defaultConfigPath is config.yaml next to the executable (a double-clicked
// exe has an unrelated cwd). `go run` builds into the temp dir, where a config
// would be lost, so fall back to the cwd there.
func defaultConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "config.yaml"
	}
	dir := filepath.Dir(exe)
	if strings.HasPrefix(dir, filepath.Clean(os.TempDir())) {
		return "config.yaml"
	}
	return filepath.Join(dir, "config.yaml")
}

// ensureConfig writes the embedded default to path when the file is missing.
// Never overwrites; failure is recoverable (the app runs in log-only mode).
func ensureConfig(path string) {
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
