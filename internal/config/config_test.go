package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const profilesYAML = `
profiles:
  mywhoosh:
    LEFT: left
    PLUS: pageup
    A: f5
  zwift:
    A: a
`

func TestLoadProfile(t *testing.T) {
	path := writeConfig(t, profilesYAML)

	cfg, err := Load(path, "mywhoosh")
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Bindings
	if len(m) != 3 {
		t.Fatalf("got %d bindings, want 3", len(m))
	}
	if m["LEFT"].VK != 0x25 || m["PLUS"].VK != 0x21 || m["A"].VK != 0x74 {
		t.Errorf("wrong vk codes: %+v", m)
	}
	if cfg.Level != slog.LevelInfo {
		t.Errorf("default level = %v, want info", cfg.Level)
	}

	cfg, err = Load(path, "zwift")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Bindings) != 1 || cfg.Bindings["A"].VK != 0x41 {
		t.Errorf("zwift profile wrong: %+v", cfg.Bindings)
	}
}

func TestLoadDefaultProfile(t *testing.T) {
	path := writeConfig(t, profilesYAML)
	cfg, err := Load(path, "") // empty -> DefaultProfile
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != DefaultProfile {
		t.Errorf("profile = %q, want %q", cfg.Profile, DefaultProfile)
	}
	if len(cfg.Bindings) != 3 {
		t.Fatalf("default profile: got %d bindings, want 3", len(cfg.Bindings))
	}
}

func TestLoadLogLevel(t *testing.T) {
	path := writeConfig(t, "loglevel: debug\nprofiles:\n  mywhoosh:\n    A: a\n")
	cfg, err := Load(path, "mywhoosh")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Level != slog.LevelDebug {
		t.Errorf("level = %v, want debug", cfg.Level)
	}

	path = writeConfig(t, "loglevel: WARN\nprofiles:\n  mywhoosh:\n    A: a\n")
	cfg, err = Load(path, "mywhoosh")
	if err != nil {
		t.Fatalf("WARN should be accepted: %v", err)
	}
	if cfg.Level != slog.LevelWarn {
		t.Errorf("level = %v, want warn", cfg.Level)
	}
}

func TestLoadFocusProgramNameOnClick(t *testing.T) {
	empty := "profiles:\n  mywhoosh:\n    A: a\n    focusProgramNameOnClick: null\n"
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"absent", "profiles:\n  mywhoosh:\n    A: a\n", ""},
		{"null", empty, ""},
		{"empty-string", "profiles:\n  mywhoosh:\n    A: a\n    focusProgramNameOnClick: \"\"\n", ""},
		{"plain", "profiles:\n  mywhoosh:\n    A: a\n    focusProgramNameOnClick: MyWhoosh\n", "MyWhoosh"},
		{"trimmed", "profiles:\n  mywhoosh:\n    A: a\n    focusProgramNameOnClick: \" MyWhoosh \"\n", "MyWhoosh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.yaml)
			cfg, err := Load(path, "mywhoosh")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.FocusProgramNameOnClick != tc.want {
				t.Errorf("FocusProgramNameOnClick = %q, want %q", cfg.FocusProgramNameOnClick, tc.want)
			}
		})
	}
}

func TestLoadFocusProgramNameOnClickPerProfile(t *testing.T) {
	path := writeConfig(t, `profiles:
  mywhoosh:
    A: a
    focusProgramNameOnClick: MyWhoosh
  zwift:
    A: a
`)
	mywhoosh, err := Load(path, "mywhoosh")
	if err != nil {
		t.Fatal(err)
	}
	if mywhoosh.FocusProgramNameOnClick != "MyWhoosh" {
		t.Errorf("mywhoosh focus = %q, want %q", mywhoosh.FocusProgramNameOnClick, "MyWhoosh")
	}
	zwift, err := Load(path, "zwift")
	if err != nil {
		t.Fatal(err)
	}
	if zwift.FocusProgramNameOnClick != "" {
		t.Errorf("zwift focus = %q, want %q (per-profile, not global)", zwift.FocusProgramNameOnClick, "")
	}
}

func TestLoadFocusProgramNameOnClickTopLevelRejected(t *testing.T) {
	path := writeConfig(t, "focusProgramNameOnClick: MyWhoosh\nprofiles:\n  mywhoosh:\n    A: a\n")
	_, err := Load(path, "mywhoosh")
	if err == nil || !strings.Contains(err.Error(), "moved") || !strings.Contains(err.Error(), focusProgramKey) {
		t.Errorf("want moved-per-profile error, got: %v", err)
	}
}

func TestLoadBadLogLevel(t *testing.T) {
	path := writeConfig(t, "loglevel: loud\nprofiles:\n  mywhoosh:\n    A: a\n")
	_, err := Load(path, "mywhoosh")
	if err == nil || !strings.Contains(err.Error(), "bad loglevel") {
		t.Errorf("want bad loglevel error, got: %v", err)
	}
}

func TestLoadUnknownProfile(t *testing.T) {
	path := writeConfig(t, profilesYAML)
	_, err := Load(path, "nope")
	if err == nil {
		t.Fatal("want error for unknown profile")
	}
	if !strings.Contains(err.Error(), `profile "nope" not found`) ||
		!strings.Contains(err.Error(), "mywhoosh, zwift") {
		t.Errorf("error should name the profile and list available: %v", err)
	}
}

func TestLoadNoProfiles(t *testing.T) {
	path := writeConfig(t, "LEFT: left\n") // old flat format
	_, err := Load(path, "mywhoosh")
	if err == nil || !strings.Contains(err.Error(), "no profiles found") {
		t.Errorf("flat file should error clearly, got: %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"), "mywhoosh")
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if !cfg.Missing || cfg.Bindings != nil {
		t.Errorf("want Missing with nil bindings, got %+v", cfg)
	}
}

func TestLoadUnknownButton(t *testing.T) {
	path := writeConfig(t, "profiles:\n  mywhoosh:\n    NOPE: a\n")
	_, err := Load(path, "mywhoosh")
	if err == nil || !strings.Contains(err.Error(), "unknown button") {
		t.Errorf("want unknown button error, got: %v", err)
	}
}

// The config shipped at the repo root must stay valid for every profile.
func TestShippedConfigProfiles(t *testing.T) {
	path := filepath.Join("..", "..", "config.yaml")
	for _, p := range []string{"zwift", "mywhoosh", "rouvy", "trainerroad", "systm"} {
		cfg, err := Load(path, p)
		if err != nil {
			t.Errorf("profile %s: %v", p, err)
			continue
		}
		if len(cfg.Bindings) == 0 {
			t.Errorf("profile %s: no bindings", p)
		}
	}
	if _, err := Load(path, "nope"); err == nil {
		t.Error("unknown profile: want error")
	}
}
