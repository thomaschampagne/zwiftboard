package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"zwiftboard/internal/config"
	"zwiftboard/internal/zwift"
)

// shippedProfiles is the full set of profiles the embedded default config
// ships AND the README "Included profiles" table documents. Adding a profile
// means adding it in both places; this set fails when either side drifts.
var shippedProfiles = []string{
	"biketerra", "mywhoosh", "mywhoosh-hd", "notepad", "rouvy",
	"systm", "tacx", "trainerroad", "trainingpeaks", "zwift",
}

func writeDefaultConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, defaultConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// readmeProfileRows returns the profile names (first table cell) of the
// README's "Included profiles" section, sorted.
func readmeProfileRows(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	start := strings.Index(s, "### Included profiles")
	if start < 0 {
		t.Fatal("README.md: no '### Included profiles' section")
	}
	rest := s[start:]
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		t.Fatal("README.md: Included profiles section has no following '## ' heading")
	}
	row := regexp.MustCompile("(?m)^\\| `([a-z0-9-]+)`")
	names := row.FindAllStringSubmatch(rest[:end], -1)
	var got []string
	for _, m := range names {
		got = append(got, m[1])
	}
	sort.Strings(got)
	return got
}

func TestShippedProfilesMatchREADME(t *testing.T) {
	names, err := config.ProfileList(writeDefaultConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), shippedProfiles...)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("config.yaml profiles = %v, want %v", names, want)
	}
	if rows := readmeProfileRows(t); strings.Join(rows, ",") != strings.Join(want, ",") {
		t.Errorf("README Included-profiles rows = %v, want %v", rows, want)
	}
}

// Every shipped profile must load: each key token resolves.
func TestEveryShippedProfileLoads(t *testing.T) {
	path := writeDefaultConfig(t)
	for _, name := range shippedProfiles {
		if _, err := config.Load(path, name); err != nil {
			t.Errorf("profile %s: %v", name, err)
		}
	}
}

// The notepad profile is the hardware-test profile of the left-controller
// POC: it must map the LEFT pod's five buttons as well as the RIGHT pod's.
func TestNotepadProfileMapsBothPods(t *testing.T) {
	cfg, err := config.Load(writeDefaultConfig(t), "notepad")
	if err != nil {
		t.Fatal(err)
	}
	for _, btn := range append(append([]string{}, zwift.LeftButtons...), zwift.RightButtons...) {
		if _, ok := cfg.Bindings[btn]; !ok {
			t.Errorf("profile notepad: missing binding for %s", btn)
		}
	}
}

// newProfiles are the five app profiles added for this feature; all five
// RIGHT-pod buttons must be mapped in each of them.
func TestNewProfilesMapEveryButton(t *testing.T) {
	path := writeDefaultConfig(t)
	for _, name := range []string{"biketerra", "rouvy", "tacx", "trainingpeaks", "zwift"} {
		cfg, err := config.Load(path, name)
		if err != nil {
			t.Errorf("profile %s: %v", name, err)
			continue
		}
		for _, btn := range []string{"PLUS", "A", "B", "Y", "Z"} {
			if _, ok := cfg.Bindings[btn]; !ok {
				t.Errorf("profile %s: missing binding for %s", name, btn)
			}
		}
	}
}
