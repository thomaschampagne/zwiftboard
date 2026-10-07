package config

import (
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

	m, err := Load(path, "mywhoosh")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 {
		t.Fatalf("got %d bindings, want 3", len(m))
	}
	if m["LEFT"].VK != 0x25 || m["PLUS"].VK != 0x21 || m["A"].VK != 0x74 {
		t.Errorf("wrong vk codes: %+v", m)
	}

	m, err = Load(path, "zwift")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["A"].VK != 0x41 {
		t.Errorf("zwift profile wrong: %+v", m)
	}
}

func TestLoadDefaultProfile(t *testing.T) {
	path := writeConfig(t, profilesYAML)
	m, err := Load(path, "") // empty -> DefaultProfile
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 {
		t.Fatalf("default profile: got %d bindings, want 3", len(m))
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
	m, err := Load(filepath.Join(t.TempDir(), "nope.yaml"), "mywhoosh")
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if m != nil {
		t.Errorf("want nil for missing file, got %+v", m)
	}
}

func TestLoadUnknownButton(t *testing.T) {
	path := writeConfig(t, "profiles:\n  mywhoosh:\n    NOPE: a\n")
	_, err := Load(path, "mywhoosh")
	if err == nil || !strings.Contains(err.Error(), "unknown button") {
		t.Errorf("want unknown button error, got: %v", err)
	}
}
