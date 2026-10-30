package main

import (
	"os"
	"path/filepath"
	"testing"

	"zwiftboard/internal/config"
)

// An explicit -config that does not exist is fatal: it must NOT be
// auto-created from the embedded default (only the default path is).
func TestResolveConfigFileExplicitMissingIsFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.yml")
	if _, err := resolveConfigFile(path, true); err == nil {
		t.Fatal("an explicit -config that does not exist must be an error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("an explicit -config must not be auto-created")
	}
}

func TestResolveConfigFileExplicitExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("loglevel: info\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveConfigFile(path, true)
	if err != nil || got != path {
		t.Fatalf("got %q err %v, want %q", got, err, path)
	}
}

func TestResolveConfigFileDefaultCreates(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	legacyConfigPaths = func() []string { return nil }
	t.Cleanup(func() { legacyConfigPaths = defaultLegacyConfigPaths })
	got, err := resolveConfigFile(defaultConfigPath(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("default path was not created: %v", err)
	}
}

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

// writeFile fails the test if the file cannot be written.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBackupConfigFirstBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	writeFile(t, path, "broken")
	bak, ok := backupConfig(path)
	if !ok || bak != path+".bak" {
		t.Fatalf("bak=%q ok=%v", bak, ok)
	}
	if got := readFile(t, bak); got != "broken" {
		t.Fatalf("backup = %q, want the current file", got)
	}
	if _, err := os.Stat(path + ".bak.1"); !os.IsNotExist(err) {
		t.Fatal("a first backup must not rotate anything")
	}
}

// An existing .bak may be a file the user is editing: recovery shifts the chain
// (.bak.2 dropped, .bak.1 -> .bak.2, .bak -> .bak.1) instead of clobbering it.
func TestBackupConfigRotatesTheChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	writeFile(t, path, "broken")
	writeFile(t, path+".bak", "backup")
	writeFile(t, path+".bak.1", "older")
	writeFile(t, path+".bak.2", "oldest")

	bak, ok := backupConfig(path)
	if !ok {
		t.Fatal("backupConfig failed")
	}
	if got := readFile(t, bak); got != "broken" {
		t.Errorf(".bak = %q, want the current file", got)
	}
	if got := readFile(t, bak+".1"); got != "backup" {
		t.Errorf(".bak.1 = %q, want the previous backup", got)
	}
	if got := readFile(t, bak+".2"); got != "older" {
		t.Errorf(".bak.2 = %q, want the previous .bak.1", got)
	}
}

// Recovery keeps the broken content in .bak, rebuilds the shipped default and
// leaves a file that loads.
func TestRecoverConfigRestoresTheShippedDefault(t *testing.T) {
	legacyConfigPaths = func() []string { return nil } // the repo cwd holds a config.yaml
	t.Cleanup(func() { legacyConfigPaths = defaultLegacyConfigPaths })
	path := filepath.Join(t.TempDir(), "config.yml")
	broken := "profiles: [this is not a map"
	writeFile(t, path, broken)
	if _, err := config.Load(path, ""); err == nil {
		t.Fatal("the fixture must not load")
	}

	cfg, ok := recoverConfig(path)
	if !ok {
		t.Fatal("recoverConfig refused a broken file")
	}
	if cfg.Missing || len(cfg.Bindings) == 0 {
		t.Fatalf("restored config has no bindings: missing=%v bindings=%d", cfg.Missing, len(cfg.Bindings))
	}
	if got := readFile(t, path+".bak"); got != broken {
		t.Fatalf("backup = %q, want the broken file", got)
	}
	if _, err := config.Load(path, ""); err != nil {
		t.Fatalf("restored file does not load: %v", err)
	}
}

// A recovery restores the shipped default, never a legacy copy: a stale
// config.yaml beside the exe must not be adopted, or the broken file returns.
func TestRecoverConfigIgnoresLegacyConfig(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, legacy, "profiles:\n  stale:\n    A: a\n")
	legacyConfigPaths = func() []string { return []string{legacy} }
	t.Cleanup(func() { legacyConfigPaths = defaultLegacyConfigPaths })
	path := filepath.Join(t.TempDir(), "config.yml")
	writeFile(t, path, "profiles: [this is not a map")

	if _, ok := recoverConfig(path); !ok {
		t.Fatal("recoverConfig refused a broken file")
	}
	if got := readFile(t, path); got != string(defaultConfig) {
		t.Fatal("recovery restored a legacy config instead of the shipped default")
	}
}

// A recovery must refuse before erasing anything when the current file cannot
// be read for the backup: here the config path is a directory.
func TestRecoverConfigRefusesWhenBackupFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := recoverConfig(path); ok {
		t.Fatal("recovery must fail when the file cannot be backed up")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatal("the unreadable path was erased before a backup")
	}
}
