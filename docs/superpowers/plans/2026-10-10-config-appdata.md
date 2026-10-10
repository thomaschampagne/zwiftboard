# Config in %LocalAppData% (drop config.yaml from the release zip) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store the config at `%LocalAppData%\zwiftboard\config.yml` (per-user, writable — the exe folder often is not) instead of next to the executable, and stop shipping `config.yaml` inside the release zip (the binary embeds and creates its own default).

**Architecture:** `defaultconfig.go` computes the path from `LOCALAPPDATA` (Windows) with an `os.UserConfigDir` fallback for dev/test machines; `ensureConfig` creates the directory, migrates a legacy next-to-exe config once (copy, never move), then writes the embedded default as before. The release script zips exe only. Docs updated.

**Tech Stack:** Go stdlib (`os`, `filepath`), bash release script, semantic-release asset labels.

**Spec:** User request — "Store config under ~\AppData\Local\zwiftboard\config.yml instead & remove release of config.yml zipped w/ .exe"

## Global Constraints

- Conventional commit, ONE atomic commit: `feat(config): ...`
- `go test ./...`, `go vet ./...`, `gofmt -l .` (CRLF noise pre-existing), `GOOS=windows go build ./...` green.
- `-config <path>` must keep overriding the default; never overwrite an existing config; migration COPIES (the legacy file stays).
- Tests run on Linux: inject env (`t.Setenv("LOCALAPPDATA", ...)`) and the legacy-candidates var; no Windows-only APIs (`%LOCALAPPDATA%` is read via `os.Getenv` — same env var Go uses, and the fallback keeps Linux/macOS dev working).
- Do NOT touch the user's uncommitted `config.yaml` working-tree edit.

---

### Task 2: Config in %LocalAppData% + zip contains exe only

**Files:**
- Modify: `defaultconfig.go` (path, mkdir, migration)
- Modify: `defaultconfig_test.go` (new tests)
- Modify: `main.go` (flag usage text only)
- Modify: `scripts/build-release.sh` (drop config.yaml from zip)
- Modify: `.releaserc.json` (asset label)
- Modify: `README.md` (quick start, flags table, configuration section, release asset list)
- Modify: `_todo.md` (check the item off)

**Interfaces:**
- Consumes: `ensureConfig` called from `main.go:68` with `defaultConfigPath()`; embedded `defaultConfig` bytes.
- Produces:
  - `func defaultConfigPath() string` → `<configDir>/config.yml` (same name, new location)
  - `var configDir = func() string` (env-driven, overridable in tests)
  - `var legacyConfigPaths = func() []string` (migration candidates, overridable in tests)

- [ ] **Step 1: Write the failing tests**

Replace the whole of `defaultconfig_test.go` with:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test . -run 'DefaultConfigPath|Migrat|CreatesDirectory'`
Expected: FAIL — `undefined: legacyConfigPaths`, wrong path (`config.yaml` next to exe).

- [ ] **Step 3: Implement in `defaultconfig.go`**

Replace `defaultConfigPath` and `ensureConfig` (keep the embedded `defaultConfig` and its doc comment):

```go
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

// defaultLegacyConfigPaths are the pre-AppData locations (config.yaml next to
// the executable; cwd for `go run` builds in temp). Overridable in tests.
var defaultLegacyConfigPaths = func() []string {
	var out []string
	if exe, err := os.Executable(); err == nil {
		out = append(out, filepath.Join(filepath.Dir(exe), "config.yaml"))
	}
	return append(out, "config.yaml")
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
```

Update the doc comment above `defaultConfig` (`// ... written next to the program` → `// ... written to %LocalAppData%\zwiftboard\config.yml`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test .`
Expected: PASS.

- [ ] **Step 5: Update the flag help in `main.go`**

Replace the `-config` flag line (main.go:50):

```go
	configPath := flag.String("config", defaultConfigPath(), "YAML file with key mapping profiles (default: %LocalAppData%\\zwiftboard\\config.yml, created from the built-in default if missing)")
```

- [ ] **Step 6: Strip config.yaml from the release zip**

`scripts/build-release.sh` — replace:

```bash
cp config.yaml dist/config.yaml
(cd dist && zip -q zwiftboard-windows-amd64.zip zwiftboard-windows-amd64.exe config.yaml && rm config.yaml)
```

with:

```bash
(cd dist && zip -q zwiftboard-windows-amd64.zip zwiftboard-windows-amd64.exe)
```

`.releaserc.json` — change the zip asset label:

```json
{ "path": "dist/zwiftboard-windows-amd64.zip", "label": "zwiftboard-windows-amd64.zip (exe)" }
```

Sanity: the embedded default still equals the repo `config.yaml` (the `//go:embed config.yaml` + build), so first run writes the reference config to AppData — nothing is lost by dropping it from the zip.

- [ ] **Step 7: Update README**

1. Quick start (line ~24): replace
   `` grab `zwiftboard-windows-amd64.exe` and
   `config.yaml` from the [Releases](../../releases) page. Put both in the same
   folder. ``
   with
   `` grab `zwiftboard-windows-amd64.exe` from the
   [Releases](../../releases) page. `` and, after the run step, note that the
   config is created on first run at `%LocalAppData%\zwiftboard\config.yml`.
   Concretely replace the two lines with:

   ```markdown
   1. **Download** the latest release: grab `zwiftboard-windows-amd64.exe`
      from the [Releases](../../releases) page. The config file is created on
      first run at `%LocalAppData%\zwiftboard\config.yml`.
   ```

2. Flags table (line ~55): `| -config | config.yaml | Config file path (relative to cwd) |` →
   `| `-config` | `%LocalAppData%\zwiftboard\config.yml` | Config file path (default: per-user AppData folder) |`

3. Configuration section (line ~104): replace `` `config.yaml` sits next to the executable (or pass `-config`). One file: `` with
   `` The config lives at `%LocalAppData%\zwiftboard\config.yml` (or pass `-config`); a config from the old location next to the exe is migrated there on first run. One file: ``

4. Release asset list (line ~271): drop the `- config.yaml (the reference profiles)` bullet; replace with a note under the exe bullet or just delete the line.

- [ ] **Step 8: Check off the todo item**

In `_todo.md`, change `- [ ] Store config under ~\AppData\Local\zwiftboard\config.yml + remove release of config.yml zipped w/ .exe` to `- [x] ...`.

- [ ] **Step 9: Run everything + build**

Run: `go test ./... && go vet ./... && GOOS=windows go build ./...`
Expected: all green. `gofmt -l .` — only pre-existing CRLF noise.

- [ ] **Step 10: Commit (atomic)**

```bash
git add defaultconfig.go defaultconfig_test.go main.go \
        scripts/build-release.sh .releaserc.json README.md _todo.md
git commit -m "feat(config): store config in %LocalAppData% and drop it from the release zip"
```

Body: why AppData (exe folder often read-only), first-run migration (copy, never move/overwrite), `-config` still overrides, zip now exe-only because the embedded default creates the file, `config.yml` extension per spec.
