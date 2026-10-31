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
//
// The tokens are asserted too, not only their presence: the left pod types
// the letters l u r d m (not the arrow keys) so the character shows up in
// the Notepad text box, and a silent edit of the shipped config would
// otherwise ship a wrong mapping that only the hardware test would catch.
func TestNotepadProfileMapsBothPods(t *testing.T) {
	cfg, err := config.Load(writeDefaultConfig(t), "notepad")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"LEFT": "l", "UP": "u", "RIGHT": "r", "DOWN": "d", "MIN": "m",
		"PLUS": "p", "A": "a", "B": "b", "Y": "y", "Z": "z",
	}
	for _, btn := range append(append([]string{}, zwift.LeftButtons...), zwift.RightButtons...) {
		bind, ok := cfg.Double[btn]
		if !ok {
			t.Errorf("profile notepad: missing binding for %s", btn)
			continue
		}
		if bind.Token != want[btn] {
			t.Errorf("profile notepad: %s is bound to %q, want %q", btn, bind.Token, want[btn])
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
			if _, ok := cfg.Single[btn]; !ok {
				t.Errorf("profile %s: missing binding for %s", name, btn)
			}
		}
	}
}

// App profiles must support 2 controllers: each maps the left pod's LEFT/RIGHT
// steer buttons (tokens left/right) and keeps every right-pod binding. notepad
// already asserts the full both-pods mapping; the app profiles follow the
// design's scope: only the two steering buttons are mapped to the left pod,
// the other left-pod buttons (UP/DOWN/MIN) stay unmapped to keep the binding
// set minimal (every app profile only maps buttons whose function it has a key for).
func TestAppProfilesMapBothPods(t *testing.T) {
	path := writeDefaultConfig(t)
	for _, name := range []string{
		"mywhoosh", "mywhoosh-hd", "trainerroad", "systm",
		"zwift", "trainingpeaks", "biketerra", "rouvy", "tacx",
	} {
		cfg, err := config.Load(path, name)
		if err != nil {
			t.Errorf("profile %s: %v", name, err)
			continue
		}
		for _, want := range []struct{ btn, wantToken string }{
			{"LEFT", "left"},
			{"UP", "up"},
			{"RIGHT", "right"},
			{"DOWN", "down"},
		} {
			bind, ok := cfg.Double[want.btn]
			if !ok {
				t.Errorf("profile %s: missing binding for %s", name, want.btn)
				continue
			}
			if bind.Token != want.wantToken {
				t.Errorf("profile %s: %s is bound to %q, want %q", name, want.btn, bind.Token, want.wantToken)
			}
		}
		// RIGHT pod unchanged: each app profile keeps its original (shipped)
		// right-pod bindings byte-for-byte. trainerroad and systm legitimately
		// never mapped PLUS (workout players), and that stays the case: the
		// design's scope is adding only the left-pod steering bindings, so
		// right-pod bindings must not grow here either.
		var wantRight map[string]string
		switch name {
		case "mywhoosh", "mywhoosh-hd":
			wantRight = map[string]string{"PLUS": "k", "A": "right", "B": "i", "Y": "u", "Z": "left"}
		case "trainerroad":
			wantRight = map[string]string{"A": "space", "B": "t", "Y": "h", "Z": "w"}
		case "systm":
			wantRight = map[string]string{"A": "space", "B": "`", "Y": "m", "Z": "esc"}
		case "zwift":
			wantRight = map[string]string{"PLUS": "pagedown", "A": "right", "B": "pageup", "Y": "space", "Z": "left"}
		case "trainingpeaks":
			wantRight = map[string]string{"PLUS": "-", "A": "right", "B": "=", "Y": "g", "Z": "left"}
		case "biketerra":
			wantRight = map[string]string{"PLUS": "s", "A": "right", "B": "w", "Y": "u", "Z": "left"}
		case "rouvy":
			wantRight = map[string]string{"PLUS": ",", "A": "right", "B": ".", "Y": "h", "Z": "left"}
		case "tacx":
			wantRight = map[string]string{"PLUS": "down", "A": "right", "B": "up", "Y": "space", "Z": "left"}
		}
		for btn, want := range wantRight {
			bind, ok := cfg.Single[btn]
			if !ok {
				t.Errorf("profile %s: missing right-pod binding for %s", name, btn)
				continue
			}
			if bind.Token != want {
				t.Errorf("profile %s: %s is bound to %q, want %q", name, btn, bind.Token, want)
			}
		}
	}
}

// Every shipped profile splits cleanly: single holds only right-pod buttons,
// and double keeps all of them so the right pod works in both modes.
func TestShippedProfilesSplitIntoModes(t *testing.T) {
	path := writeDefaultConfig(t)
	names, err := config.ProfileList(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		cfg, err := config.Load(path, name)
		if err != nil {
			t.Errorf("profile %s: %v", name, err)
			continue
		}
		for _, btn := range zwift.LeftButtons {
			if _, ok := cfg.Single[btn]; ok {
				t.Errorf("profile %s: single maps left-pod button %s", name, btn)
			}
		}
		for btn := range cfg.Single {
			if _, ok := cfg.Double[btn]; !ok {
				t.Errorf("profile %s: %s is in single but not double", name, btn)
			}
		}
	}
}
