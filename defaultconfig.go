package main

import (
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"zwiftboard/internal/config"
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
// A release exe must never adopt an unrelated config.yaml from a folder it was
// started in: a file copied into %LocalAppData% that fails to load would
// otherwise ask for recovery on every start. Overridable in tests.
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

// resolveConfigFile returns the config path to load. When the user passed
// -config explicitly (explicit=true) the file must already exist: a missing
// file is a configuration error, not a first run, so it is reported rather than
// created. For the default path (explicit=false) a missing file is created from
// the embedded default via ensureConfig (which also migrates a legacy config).
func resolveConfigFile(path string, explicit bool) (string, error) {
	if explicit {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("config file not found: %s", path)
		}
		return path, nil
	}
	ensureConfig(path)
	return path, nil
}

// ensureConfig migrates a legacy config once, then writes the embedded default
// to path when the file is missing. Never overwrites; a false return means the
// file could not be created (the app then runs in log-only mode).
func ensureConfig(path string) bool {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			slog.Warn("cannot create config directory", "dir", dir, "error", err)
			return false
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
				slog.Warn("cannot migrate legacy config", "from", legacy, "to", path, "error", werr)
				return false
			}
			slog.Info("migrated config to the new location", "from", legacy, "to", path)
			return true
		}
	}
	return writeDefault(path)
}

// writeDefault writes the embedded shipped config to a missing path. It never
// overwrites (O_EXCL) and never migrates: recovery must restore the shipped
// default, not a legacy copy that may be the very file that broke.
func writeDefault(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if !os.IsExist(err) {
			slog.Warn("cannot create default config", "file", path, "error", err)
		}
		return false
	}
	defer f.Close()
	if _, err := f.Write(defaultConfig); err != nil {
		slog.Warn("cannot write default config", "file", path, "error", err)
		return false
	}
	slog.Info("created default config", "file", path)
	return true
}

// backupConfig copies path to path+".bak" before a recovery erases it. An
// existing backup is never clobbered — the user may be editing it — so the
// chain shifts first: .bak.2 is dropped, .bak.1 becomes .bak.2, .bak becomes
// .bak.1, and the current file becomes the new .bak. A false return means the
// caller must abort the recovery (the broken file is left untouched).
func backupConfig(path string) (string, bool) {
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		if err := rotateBackup(bak); err != nil {
			slog.Error("cannot rotate the config backup — refusing to erase", "file", bak, "error", err)
			return "", false
		}
	} else if !os.IsNotExist(err) {
		slog.Error("cannot inspect the config backup — refusing to erase", "file", bak, "error", err)
		return "", false
	}
	if err := copyTo(bak, path); err != nil {
		slog.Error("cannot back up the config — refusing to erase", "file", path, "error", err)
		return "", false
	}
	return bak, true
}

// rotateBackup shifts the backup chain one slot: .bak.1 -> .bak.2 (the oldest
// .bak.2 is dropped) and .bak -> .bak.1. A missing .bak.1 is not an error.
func rotateBackup(bak string) error {
	_ = os.Remove(bak + ".2")
	if _, err := os.Stat(bak + ".1"); err == nil {
		if err := os.Rename(bak+".1", bak+".2"); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(bak, bak+".1")
}

// copyTo copies src to dst. The recovery path must never crash on a failed
// copy: the error is returned so the caller can abort before erasing.
func copyTo(dst, src string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o644)
}

// recoverConfig is the one-shot recovery the startup dialog invokes on user
// yes: back the broken file up, erase it, rebuild the shipped default, then
// verify every profile of the rebuilt file loads (fail fast on the first bad
// one). Any failure leaves the broken file in place and returns ok == false.
func recoverConfig(configPath string) (config.Config, bool) {
	if _, ok := backupConfig(configPath); !ok {
		return config.Config{}, false
	}
	// writeDefault only writes to a missing file (O_EXCL), so the broken file
	// must be gone first.
	if err := os.Remove(configPath); err != nil {
		slog.Error("cannot erase the broken config", "file", configPath, "error", err)
		return config.Config{}, false
	}
	if !writeDefault(configPath) {
		slog.Error("cannot rebuild the built-in default — restore it from the backup", "file", configPath)
		return config.Config{}, false
	}
	for _, p := range config.Profiles(configPath) {
		if err := p.Error(); err != nil {
			slog.Error("the built-in default has an invalid profile — restore the backup and report this", "file", configPath, "profile", p.Name(), "error", err)
			return config.Config{}, false
		}
	}
	cfg, err := config.Load(configPath, "")
	if err != nil {
		slog.Error("the rebuilt config does not load — restore the backup and report this", "file", configPath, "error", err)
		return config.Config{}, false
	}
	return cfg, true
}
