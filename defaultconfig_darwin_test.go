//go:build darwin

package main

import (
	"path/filepath"
	"testing"
	"zwiftboard/internal/config"
)

func TestMacFirstRunUsesForegroundKeyboardProfile(t *testing.T) {
	old := legacyConfigPaths
	legacyConfigPaths = func() []string { return nil }
	t.Cleanup(func() { legacyConfigPaths = old })
	path := filepath.Join(t.TempDir(), "config.yml")
	ensureConfig(path)
	cfg, err := config.Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != "keyboard" || cfg.StartProgram != "" || cfg.FocusProgramNamePrefixOnClick != "" || len(cfg.Bindings) != 5 {
		t.Fatalf("first-run config is not usable on macOS: %+v", cfg)
	}
}
