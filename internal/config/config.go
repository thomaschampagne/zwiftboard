// Package config loads the YAML configuration: a map of named profiles, each
// mapping button name -> keyboard key token plus per-profile options.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"zwiftboard/internal/keys"
	"zwiftboard/internal/zwift"
)

// DefaultProfile is the fallback profile when neither -p nor the file's
// top-level defaultProfile: key selects one.
const DefaultProfile = "mywhoosh"

// focusProgramKey is the per-profile YAML key selecting the window zwiftboard
// brings to the foreground before each tap; null/"" disables the feature. It
// must not collide with a button name.
const focusProgramKey = "focusProgramNamePrefixOnClick"

// startProgramKey is the per-profile YAML key naming a program to launch once
// Bluetooth is up; null/"" disables it. It must not collide with a button name.
const startProgramKey = "startProgram"

// doc is the config.yaml structure.
type doc struct {
	LogLevel                      string                       `yaml:"loglevel"`
	DefaultProfile                string                       `yaml:"defaultProfile"`                // profile used when -p is absent
	FocusProgramNamePrefixOnClick *string                      `yaml:"focusProgramNamePrefixOnClick"` // set at top level = error (moved into profiles)
	Profiles                      map[string]map[string]string `yaml:"profiles"`
}

// Config is the parsed config file for the selected profile.
type Config struct {
	Bindings map[string]keys.Binding // nil when the file is missing
	Profile  string                  // resolved profile name
	Level    slog.Level              // from loglevel:, default info
	Missing  bool                    // config file not found
	// FocusProgramNamePrefixOnClick is the program (window title or .exe name,
	// per-profile key) brought to the foreground before every key tap; ""
	// disables the feature (focusProgramNamePrefixOnClick: null).
	FocusProgramNamePrefixOnClick string
	// StartProgram is the program (path or .exe name, per-profile key) launched
	// once Bluetooth is up; "" disables the feature (startProgram: null).
	StartProgram string
}

// Load reads path and returns the config for profile. An empty profile means
// -p was not given: the file's top-level defaultProfile: is used, falling back
// to DefaultProfile. A missing file is not an error: Missing is set and
// logging-only mode applies.
func Load(path, profile string) (Config, error) {
	explicit := profile != ""
	if !explicit {
		profile = DefaultProfile // provisional; the file's defaultProfile: may override it below
	}
	cfg := Config{Profile: profile, Level: slog.LevelInfo}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.Missing = true
			return cfg, nil
		}
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	var d doc
	if err := yaml.Unmarshal(data, &d); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	if def := strings.TrimSpace(d.DefaultProfile); !explicit && def != "" {
		profile = def
		cfg.Profile = profile
	}
	if d.LogLevel != "" {
		if err := cfg.Level.UnmarshalText([]byte(d.LogLevel)); err != nil {
			return cfg, fmt.Errorf("%s: bad loglevel %q (use debug, info, warn, error)", path, d.LogLevel)
		}
	}
	if d.FocusProgramNamePrefixOnClick != nil {
		return cfg, fmt.Errorf("%s: %s moved — it is now a per-profile key, put it inside the profile block (e.g. under `%s:`)", path, focusProgramKey, DefaultProfile)
	}
	if len(d.Profiles) == 0 {
		return cfg, fmt.Errorf("%s: no profiles found — expected a top-level `profiles:` map, each profile mapping buttons to keys", path)
	}
	raw, ok := d.Profiles[profile]
	if !ok {
		if !explicit && strings.TrimSpace(d.DefaultProfile) != "" {
			return cfg, fmt.Errorf("%s: defaultProfile %q not found (available: %s)", path, profile, strings.Join(ProfileNames(d.Profiles), ", "))
		}
		return cfg, fmt.Errorf("%s: profile %q not found (available: %s)", path, profile, strings.Join(ProfileNames(d.Profiles), ", "))
	}

	cfg.Bindings = make(map[string]keys.Binding, len(raw))
	if v, ok := raw[focusProgramKey]; ok {
		// Value comes in as "" for focusProgramNamePrefixOnClick: null; trim so a
		// padded value still disables via "".
		cfg.FocusProgramNamePrefixOnClick = strings.TrimSpace(v)
		delete(raw, focusProgramKey)
	}
	if v, ok := raw[startProgramKey]; ok {
		// Value comes in as "" for startProgram: null; trim so a padded value
		// still disables via "".
		cfg.StartProgram = strings.TrimSpace(v)
		delete(raw, startProgramKey)
	}
	for btn, token := range raw {
		btn = strings.ToUpper(strings.TrimSpace(btn))
		if !knownButtons[btn] {
			return cfg, fmt.Errorf("%s: profile %s: unknown button %q (valid: %s)", path, profile, btn, strings.Join(ButtonNames(), ", "))
		}
		b, err := keys.Resolve(token)
		if err != nil {
			return cfg, fmt.Errorf("%s: profile %s: button %s: %w", path, profile, btn, err)
		}
		cfg.Bindings[btn] = b
	}
	return cfg, nil
}

// Profiles walks path and validates every profile: it returns one entry per
// profile with the profile name and an error when that profile failed Load
// (a non-nil error means the profile is not fixable from zwiftboard and the
// file must be fixed or restored from a backup). Only a top-level YAML parse
// failure or a missing profile list is fatal to the walk. A missing file
// returns an empty list (nothing to recover from).
func Profiles(path string) []Profile {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		slog.Error("cannot read config for recovery", "file", path, "error", rerr)
		return nil
	}
	var d doc
	if perr := yaml.Unmarshal(data, &d); perr != nil {
		slog.Error("cannot parse config for recovery", "file", path, "error", perr)
		return nil
	}
	if len(d.Profiles) == 0 {
		slog.Error("config has no profiles for recovery — fix it manually", "file", path)
		return nil
	}
	out := make([]Profile, 0, len(d.Profiles))
	for name := range d.Profiles {
		_, perr := Load(path, name)
		out = append(out, Profile{name: name, err: perr})
	}
	return out
}

// Profile is one profile from Profiles: its name and a nil err when it parsed
// cleanly. A non-nil err means the profile is not fixable from zwiftboard and
// the file must be restored from a backup.
type Profile struct {
	name string
	err  error
}

// Name is the profile name.
func (p Profile) Name() string {
	return p.name
}

// Error is the parse error, or nil if the profile parsed cleanly.
func (p Profile) Error() error {
	return p.err
}

// ProfileNames returns the profile names sorted, for messages.
func ProfileNames(m map[string]map[string]string) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ProfileList returns the profile names in path, sorted. A missing file is not
// an error (nil list); a parse failure is.
func ProfileList(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var d doc
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return ProfileNames(d.Profiles), nil
}

// knownButtons is the set of valid YAML keys.
var knownButtons = func() map[string]bool {
	m := make(map[string]bool, len(zwift.Buttons))
	for _, name := range zwift.Buttons {
		m[name] = true
	}
	return m
}()

// ButtonNames returns all button names sorted, for messages and logs.
func ButtonNames() []string {
	names := make([]string, 0, len(zwift.Buttons))
	for _, n := range zwift.Buttons {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
