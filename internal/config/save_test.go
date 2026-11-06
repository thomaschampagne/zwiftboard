package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveSelectionUpdatesValuesAndKeepsComments(t *testing.T) {
	path := writeConfig(t, "# top comment\n# more\ndefaultProfile: notepad\n# defaultMode rule\nloglevel: info\nprofiles:\n  a:\n    A: space\n  b:\n    A: enter\n")
	if err := SaveSelection(path, "b", ModeSingle); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"defaultProfile: b", "defaultMode: single", "# top comment", "# defaultMode rule", "loglevel: info"} {
		if !strings.Contains(got, want) {
			t.Fatalf("saved config lost %q:\n%s", want, got)
		}
	}
	cfg, err := Load(path, "") // the new selection must still load
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != "b" || cfg.DefaultMode != ModeSingle {
		t.Fatalf("after save: %q / %q, want b / single", cfg.Profile, cfg.DefaultMode)
	}
}

func TestSaveSelectionAddsMissingKeys(t *testing.T) {
	path := writeConfig(t, "profiles:\n  a:\n    A: space\n")
	if err := SaveSelection(path, "a", ModeDouble); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != "a" || cfg.DefaultMode != ModeDouble {
		t.Fatalf("got %q / %q, want a / double", cfg.Profile, cfg.DefaultMode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// defaultMode is created right under defaultProfile.
	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "defaultProfile: ") {
			if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "defaultMode: ") {
				t.Fatalf("defaultMode is not right under defaultProfile:\n%s", data)
			}
			break
		}
	}
}

func TestSaveSelectionErrors(t *testing.T) {
	if err := SaveSelection(filepath.Join(t.TempDir(), "nope.yml"), "a", ModeSingle); !os.IsNotExist(err) {
		t.Fatalf("missing file: err = %v, want IsNotExist", err)
	}
	path := writeConfig(t, "- just\n- a list\n")
	if err := SaveSelection(path, "a", ModeSingle); err == nil {
		t.Fatal("a non-mapping config must fail the save")
	}
}
