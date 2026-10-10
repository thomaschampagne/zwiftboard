package main

import (
	"os"
	"path/filepath"
	"testing"

	"zwiftboard/internal/config"
)

func TestEnsureConfigCreatesValidDefaultOnce(t *testing.T) {
	legacyConfigPaths = func() []string { return nil } // repo cwd holds a config.yaml
	t.Cleanup(func() { legacyConfigPaths = defaultLegacyConfigPaths })
	path := filepath.Join(t.TempDir(), "config.yml")
	ensureConfig(path)
	if _, err := config.Load(path, config.DefaultProfile); err != nil {
		t.Fatalf("embedded default does not load: %v", err)
	}
	// An existing (user-edited) file must never be overwritten.
	if err := os.WriteFile(path, []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureConfig(path)
	if b, _ := os.ReadFile(path); string(b) != "custom" {
		t.Errorf("existing config overwritten: %q", b)
	}
}

// The default lives under %LocalAppData%\zwiftboard on Windows; tests pin the
// env var so the path is deterministic on every OS.
func TestDefaultConfigPathUsesLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	want := filepath.Join(os.Getenv("LOCALAPPDATA"), "zwiftboard", "config.yml")
	if got := defaultConfigPath(); got != want {
		t.Fatalf("defaultConfigPath() = %q, want %q", got, want)
	}
}

// First run on a fresh machine: the directory does not exist yet, so
// ensureConfig must create it (LOCALAPPDATA\zwiftboard is a new folder).
func TestEnsureConfigCreatesDirectoryAndFile(t *testing.T) {
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "missing-parent"))
	legacyConfigPaths = func() []string { return nil }
	t.Cleanup(func() { legacyConfigPaths = defaultLegacyConfigPaths })
	ensureConfig(defaultConfigPath())
	if _, err := config.Load(defaultConfigPath(), config.DefaultProfile); err != nil {
		t.Fatalf("config after first run does not load: %v", err)
	}
}

// A config from the old layout (next to the exe) must be migrated — copied —
// to the new location on first run, never moved or overwritten.
func TestEnsureConfigMigratesLegacyOnce(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	dir := t.TempDir()
	legacy := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(legacy, []byte("profiles:\n  x: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyConfigPaths = func() []string { return []string{legacy} }
	t.Cleanup(func() { legacyConfigPaths = defaultLegacyConfigPaths })

	ensureConfig(defaultConfigPath())
	migrated, err := os.ReadFile(defaultConfigPath())
	if err != nil {
		t.Fatalf("migrated config missing: %v", err)
	}
	if string(migrated) != "profiles:\n  x: {}\n" {
		t.Fatalf("migrated content = %q", migrated)
	}
	if b, _ := os.ReadFile(legacy); string(b) != "profiles:\n  x: {}\n" {
		t.Fatal("legacy config must be copied, not moved")
	}

	// Second run: the (user-edited) migrated file wins over both the legacy
	// file and the embedded default.
	if err := os.WriteFile(defaultConfigPath(), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureConfig(defaultConfigPath())
	if b, _ := os.ReadFile(defaultConfigPath()); string(b) != "edited" {
		t.Fatalf("second ensureConfig overwrote the migrated file: %q", b)
	}
}
