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

// DefaultProfile is used when -p/--profile is not given.
const DefaultProfile = "mywhoosh"

// focusProgramKey is the per-profile YAML key selecting the window zwiftboard
// brings to the foreground before each tap; null/"" disables the feature. It
// must not collide with a button name.
const focusProgramKey = "focusProgramNameOnClick"

// doc is the config.yaml structure.
type doc struct {
	LogLevel                string                       `yaml:"loglevel"`
	FocusProgramNameOnClick *string                      `yaml:"focusProgramNameOnClick"` // set at top level = error (moved into profiles)
	Profiles                map[string]map[string]string `yaml:"profiles"`
}

// Config is the parsed config file for the selected profile.
type Config struct {
	Bindings map[string]keys.Binding // nil when the file is missing
	Profile  string                  // resolved profile name
	Level    slog.Level              // from loglevel:, default info
	Missing  bool                    // config file not found
	// FocusProgramNameOnClick is the program (window title or .exe name,
	// per-profile key) brought to the foreground before every key tap; ""
	// disables the feature (focusProgramNameOnClick: null).
	FocusProgramNameOnClick string
}

// Load reads path and returns the config for profile (empty selects
// DefaultProfile). A missing file is not an error: Missing is set and
// logging-only mode applies.
func Load(path, profile string) (Config, error) {
	if profile == "" {
		profile = DefaultProfile
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
	if d.LogLevel != "" {
		if err := cfg.Level.UnmarshalText([]byte(d.LogLevel)); err != nil {
			return cfg, fmt.Errorf("%s: bad loglevel %q (use debug, info, warn, error)", path, d.LogLevel)
		}
	}
	if d.FocusProgramNameOnClick != nil {
		return cfg, fmt.Errorf("%s: %s moved — it is now a per-profile key, put it inside the profile block (e.g. under `%s:`)", path, focusProgramKey, DefaultProfile)
	}
	if len(d.Profiles) == 0 {
		return cfg, fmt.Errorf("%s: no profiles found — expected a top-level `profiles:` map, each profile mapping buttons to keys (see config.yaml)", path)
	}
	raw, ok := d.Profiles[profile]
	if !ok {
		return cfg, fmt.Errorf("%s: profile %q not found (available: %s)", path, profile, strings.Join(ProfileNames(d.Profiles), ", "))
	}

	cfg.Bindings = make(map[string]keys.Binding, len(raw))
	if v, ok := raw[focusProgramKey]; ok {
		// Value comes in as "" for focusProgramNameOnClick: null; trim so a
		// padded value still disables via "".
		cfg.FocusProgramNameOnClick = strings.TrimSpace(v)
		delete(raw, focusProgramKey)
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

// ProfileNames returns the profile names sorted, for messages.
func ProfileNames(m map[string]map[string]string) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
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
