package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zwiftboard/internal/keys"
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
	m := cfg.Double
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
	if len(cfg.Single) != 1 || cfg.Single["A"].VK != 0x41 {
		t.Errorf("zwift profile wrong: %+v", cfg.Single)
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
	if len(cfg.Double) != 3 {
		t.Fatalf("default profile: got %d bindings, want 3", len(cfg.Double))
	}
}

func TestLoadUsesConfiguredDefaultProfile(t *testing.T) {
	path := writeConfig(t, `
defaultProfile: zwift
profiles:
  mywhoosh:
    A: a
  zwift:
    B: b
`)
	cfg, err := Load(path, "") // no -p: take the config's defaultProfile
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != "zwift" {
		t.Errorf("profile = %q, want zwift (from defaultProfile)", cfg.Profile)
	}
	if _, ok := cfg.Single["B"]; !ok {
		t.Errorf("bindings did not come from zwift: %+v", cfg.Single)
	}
}

func TestLoadExplicitProfileBeatsConfiguredDefault(t *testing.T) {
	path := writeConfig(t, `
defaultProfile: zwift
profiles:
  mywhoosh:
    A: a
  zwift:
    B: b
`)
	cfg, err := Load(path, "mywhoosh") // explicit -p wins
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != "mywhoosh" {
		t.Errorf("profile = %q, want mywhoosh (explicit beats defaultProfile)", cfg.Profile)
	}
}

func TestLoadEmptyConfiguredDefaultFallsBack(t *testing.T) {
	path := writeConfig(t, `
defaultProfile: ""
profiles:
  mywhoosh:
    A: a
`)
	cfg, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != DefaultProfile {
		t.Errorf("profile = %q, want %q (empty defaultProfile falls back)", cfg.Profile, DefaultProfile)
	}
}

func TestLoadUnknownConfiguredDefaultProfileIsFatal(t *testing.T) {
	path := writeConfig(t, `
defaultProfile: ghost
profiles:
  mywhoosh:
    A: a
`)
	_, err := Load(path, "")
	if err == nil {
		t.Fatal("an unknown defaultProfile must be an error")
	}
	if !strings.Contains(err.Error(), "defaultProfile") || !strings.Contains(err.Error(), `"ghost"`) {
		t.Errorf("error should name defaultProfile and its value: %v", err)
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

func TestLoadFocusProgramNamePrefixOnClick(t *testing.T) {
	empty := "profiles:\n  mywhoosh:\n    A: a\n    focusProgramNamePrefixOnClick: null\n"
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"absent", "profiles:\n  mywhoosh:\n    A: a\n", ""},
		{"null", empty, ""},
		{"empty-string", "profiles:\n  mywhoosh:\n    A: a\n    focusProgramNamePrefixOnClick: \"\"\n", ""},
		{"plain", "profiles:\n  mywhoosh:\n    A: a\n    focusProgramNamePrefixOnClick: MyWhoosh\n", "MyWhoosh"},
		{"trimmed", "profiles:\n  mywhoosh:\n    A: a\n    focusProgramNamePrefixOnClick: \" MyWhoosh \"\n", "MyWhoosh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.yaml)
			cfg, err := Load(path, "mywhoosh")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.FocusProgramNamePrefixOnClick != tc.want {
				t.Errorf("FocusProgramNamePrefixOnClick = %q, want %q", cfg.FocusProgramNamePrefixOnClick, tc.want)
			}
		})
	}
}

func TestLoadFocusProgramNamePrefixOnClickPerProfile(t *testing.T) {
	path := writeConfig(t, `profiles:
  mywhoosh:
    A: a
    focusProgramNamePrefixOnClick: MyWhoosh
  zwift:
    A: a
`)
	mywhoosh, err := Load(path, "mywhoosh")
	if err != nil {
		t.Fatal(err)
	}
	if mywhoosh.FocusProgramNamePrefixOnClick != "MyWhoosh" {
		t.Errorf("mywhoosh focus = %q, want %q", mywhoosh.FocusProgramNamePrefixOnClick, "MyWhoosh")
	}
	zwift, err := Load(path, "zwift")
	if err != nil {
		t.Fatal(err)
	}
	if zwift.FocusProgramNamePrefixOnClick != "" {
		t.Errorf("zwift focus = %q, want %q (per-profile, not global)", zwift.FocusProgramNamePrefixOnClick, "")
	}
}

func TestLoadFocusProgramNamePrefixOnClickTopLevelRejected(t *testing.T) {
	path := writeConfig(t, "focusProgramNamePrefixOnClick: MyWhoosh\nprofiles:\n  mywhoosh:\n    A: a\n")
	_, err := Load(path, "mywhoosh")
	if err == nil || !strings.Contains(err.Error(), "moved") || !strings.Contains(err.Error(), focusProgramKey) {
		t.Errorf("want moved-per-profile error, got: %v", err)
	}
}

func TestLoadStartProgram(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
profiles:
  p:
    startProgram: 'C:\games\MyWhooshHD.exe'
    A: b
`), "p")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StartProgram != `C:\games\MyWhooshHD.exe` {
		t.Fatalf("got %q", cfg.StartProgram)
	}
	if _, ok := cfg.Single["STARTPROGRAM"]; ok {
		t.Fatal("startProgram treated as a button")
	}
}

func TestLoadStartProgramNullIsOff(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
profiles:
  p:
    startProgram: null
    A: b
`), "p")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StartProgram != "" {
		t.Fatalf("got %q", cfg.StartProgram)
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
	if !cfg.Missing || cfg.Single != nil {
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

func TestProfileList(t *testing.T) {
	path := writeConfig(t, "profiles:\n  b:\n    A: space\n  a:\n    A: space\n")
	names, err := ProfileList(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "a,b" {
		t.Fatalf("got %v", names)
	}
}

func TestProfileListMissingFile(t *testing.T) {
	names, err := ProfileList(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil || names != nil {
		t.Fatalf("names=%v err=%v", names, err)
	}
}

// The shipped config's defaultProfile must resolve to a real profile.
func TestShippedConfigDefaultProfileResolves(t *testing.T) {
	path := filepath.Join("..", "..", "config.yaml")
	cfg, err := Load(path, "") // no -p: the shipped defaultProfile: applies
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != "notepad" {
		t.Errorf("profile = %q, want notepad (shipped defaultProfile)", cfg.Profile)
	}
	if len(cfg.Single) == 0 {
		t.Error("shipped default profile has no bindings")
	}
}

// The config shipped at the repo root must stay valid for every profile.
func TestShippedConfigProfiles(t *testing.T) {
	path := filepath.Join("..", "..", "config.yaml")
	for _, p := range []string{"mywhoosh", "notepad", "trainerroad", "systm"} {
		cfg, err := Load(path, p)
		if err != nil {
			t.Errorf("profile %s: %v", p, err)
			continue
		}
		if len(cfg.Single) == 0 {
			t.Errorf("profile %s: no bindings", p)
		}
	}
	if _, err := Load(path, "nope"); err == nil {
		t.Error("unknown profile: want error")
	}
}

// Profiles validates every profile in one walk: the shipped file has no invalid
// profile, and a bad profile is reported with its name and error.
func TestProfilesReportsEveryProfile(t *testing.T) {
	path := filepath.Join("..", "..", "config.yaml")
	ps := Profiles(path)
	if len(ps) == 0 {
		t.Fatal("no profiles walked")
	}
	for _, p := range ps {
		if err := p.Error(); err != nil {
			t.Errorf("shipped profile %s: %v", p.Name(), err)
		}
	}

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	body := "profiles:\n  good:\n    A: a\n  bad:\n    A: no-such-key\n"
	if err := os.WriteFile(bad, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var failed []string
	for _, p := range Profiles(bad) {
		if p.Error() != nil {
			failed = append(failed, p.Name())
		}
	}
	if len(failed) != 1 || failed[0] != "bad" {
		t.Fatalf("failed profiles = %v, want [bad]", failed)
	}
}

const modesYAML = `
profiles:
  notepad:
    single:
      A: a
    double:
      LEFT: l
      A: a
    focusProgramNamePrefixOnClick: Notepad
    startProgram: notepad.exe
`

func TestLoadSingleAndDoubleFormat(t *testing.T) {
	cfg, err := Load(writeConfig(t, modesYAML), "notepad")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Single) != 1 || cfg.Single["A"].Token != "a" {
		t.Errorf("single = %+v, want only A", cfg.Single)
	}
	if len(cfg.Double) != 2 || cfg.Double["LEFT"].Token != "l" {
		t.Errorf("double = %+v, want LEFT and A", cfg.Double)
	}
	if cfg.FocusProgramNamePrefixOnClick != "Notepad" || cfg.StartProgram != "notepad.exe" {
		t.Errorf("profile options not read beside single/double: %+v", cfg)
	}
}

// A flat profile is the legacy format: it loads as single unchanged, and double
// is a copy so the profile still works after a toggle.
func TestLoadLegacyFlatFormatIsSingle(t *testing.T) {
	cfg, err := Load(writeConfig(t, profilesYAML), "mywhoosh")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Single) != 2 || cfg.Single["PLUS"].Token != "pageup" {
		t.Errorf("legacy single = %+v, want the right-pod buttons only", cfg.Single)
	}
	if _, ok := cfg.Single["LEFT"]; ok {
		t.Errorf("legacy single must not drive the left pod: %+v", cfg.Single)
	}
	if len(cfg.Double) != 3 || cfg.Double["LEFT"].Token != "left" || cfg.Double["PLUS"].Token != "pageup" {
		t.Errorf("legacy double = %+v, want the full flat mapping", cfg.Double)
	}
}

func TestLoadLegacyFlatFormatKeepsProfileOptions(t *testing.T) {
	cfg, err := Load(writeConfig(t, "profiles:\n  a:\n    A: a\n    startProgram: x.exe\n"), "a")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StartProgram != "x.exe" || len(cfg.Single) != 1 {
		t.Errorf("legacy options lost: %+v", cfg)
	}
}

func TestLoadSingleRejectsLeftButton(t *testing.T) {
	body := "profiles:\n  a:\n    single:\n      LEFT: left\n    double:\n      LEFT: left\n"
	_, err := Load(writeConfig(t, body), "a")
	if err == nil || !strings.Contains(err.Error(), "left-controller button") {
		t.Fatalf("err = %v, want a left-controller error", err)
	}
}

func TestLoadNewFormatNeedsBothModes(t *testing.T) {
	cases := map[string]string{
		"missing double": "profiles:\n  a:\n    single:\n      A: a\n",
		"missing single": "profiles:\n  a:\n    double:\n      A: a\n",
		"null double":    "profiles:\n  a:\n    single:\n      A: a\n    double:\n",
		"scalar single":  "profiles:\n  a:\n    single: A\n    double:\n      A: a\n",
		"unknown key":    "profiles:\n  a:\n    single:\n      A: a\n    double:\n      A: a\n    dobule: {}\n",
		"bad button":     "profiles:\n  a:\n    single:\n      NOPE: a\n    double:\n      A: a\n",
		"bad token":      "profiles:\n  a:\n    single:\n      A: notakey\n    double:\n      A: a\n",
		"bad option":     "profiles:\n  a:\n    single:\n      A: a\n    double:\n      A: a\n    startProgram: [x]\n",
	}
	for name, body := range cases {
		if _, err := Load(writeConfig(t, body), "a"); err == nil {
			t.Errorf("%s: Load succeeded, want an error", name)
		}
	}
}

func TestLoadMissingDoubleNamesTheMode(t *testing.T) {
	_, err := Load(writeConfig(t, "profiles:\n  a:\n    single:\n      A: a\n"), "a")
	if err == nil || !strings.Contains(err.Error(), "missing double") {
		t.Fatalf("err = %v, want a message naming double", err)
	}
}

func TestLoadRejectsOversizedFile(t *testing.T) {
	path := writeConfig(t, strings.Repeat("#", maxConfigBytes+1))
	if _, err := Load(path, "a"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v, want a size error", err)
	}
}

func TestModeToggle(t *testing.T) {
	if got := ModeSingle.Toggle(); got != ModeDouble {
		t.Errorf("single toggles to %q, want double", got)
	}
	if got := ModeDouble.Toggle(); got != ModeSingle {
		t.Errorf("double toggles to %q, want single", got)
	}
}

func TestMappingSelectsTheModeMap(t *testing.T) {
	cfg := Config{Single: map[string]keys.Binding{"A": {Token: "a"}}, Double: map[string]keys.Binding{"LEFT": {Token: "l"}}}
	if _, ok := cfg.Mapping(ModeSingle)["A"]; !ok {
		t.Error("single mode should use the single map")
	}
	if _, ok := cfg.Mapping(ModeDouble)["LEFT"]; !ok {
		t.Error("double mode should use the double map")
	}
}
