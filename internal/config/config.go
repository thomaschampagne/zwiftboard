// Package config loads the YAML configuration: a map of named profiles, each
// mapping button name -> keyboard key token per controller mode, plus per-profile
// options.
package config

import (
	"fmt"
	"io"
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

// Mode says which Click V2 controllers drive a profile's keys: ModeSingle the
// right controller only, ModeDouble both controllers.
type Mode string

const (
	ModeSingle Mode = "single"
	ModeDouble Mode = "double"
)

// Toggle returns the other mode.
func (m Mode) Toggle() Mode {
	if m == ModeDouble {
		return ModeSingle
	}
	return ModeDouble
}

// focusProgramKey is the per-profile YAML key selecting the window zwiftboard
// brings to the foreground before each tap; null/"" disables the feature. It
// must not collide with a button name.
const focusProgramKey = "focusProgramNamePrefixOnClick"

// startProgramKey is the per-profile YAML key naming a program to launch once
// Bluetooth is up; null/"" disables it. It must not collide with a button name.
const startProgramKey = "startProgram"

// maxConfigBytes caps the file read: a real config is a few KB, so a larger
// file is a mistake or untrusted input.
const maxConfigBytes = 1 << 20

// doc is the config.yaml structure. Profile blocks stay as raw nodes because
// the legacy flat format and the single/double format share the same key.
type doc struct {
	LogLevel                      string               `yaml:"loglevel"`
	DefaultProfile                string               `yaml:"defaultProfile"`                // profile used when -p is absent
	FocusProgramNamePrefixOnClick *string              `yaml:"focusProgramNamePrefixOnClick"` // set at top level = error (moved into profiles)
	Profiles                      map[string]yaml.Node `yaml:"profiles"`
}

// Config is the parsed config file for the selected profile.
type Config struct {
	Single  map[string]keys.Binding // right controller; nil when the file is missing
	Double  map[string]keys.Binding // left and right controllers; nil when the file is missing
	Profile string                  // resolved profile name
	Level   slog.Level              // from loglevel:, default info
	Missing bool                    // config file not found
	// FocusProgramNamePrefixOnClick is the program (window title or .exe name,
	// per-profile key) brought to the foreground before every key tap; ""
	// disables the feature (focusProgramNamePrefixOnClick: null).
	FocusProgramNamePrefixOnClick string
	// StartProgram is the program (path or .exe name, per-profile key) launched
	// once Bluetooth is up; "" disables the feature (startProgram: null).
	StartProgram string
}

// Mapping returns the button map for mode.
func (c Config) Mapping(mode Mode) map[string]keys.Binding {
	if mode == ModeDouble {
		return c.Double
	}
	return c.Single
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

	data, err := readFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.Missing = true
			return cfg, nil
		}
		return cfg, err
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
	node, ok := d.Profiles[profile]
	if !ok {
		if !explicit && strings.TrimSpace(d.DefaultProfile) != "" {
			return cfg, fmt.Errorf("%s: defaultProfile %q not found (available: %s)", path, profile, strings.Join(ProfileNames(d.Profiles), ", "))
		}
		return cfg, fmt.Errorf("%s: profile %q not found (available: %s)", path, profile, strings.Join(ProfileNames(d.Profiles), ", "))
	}

	raw, err := splitProfile(path, profile, &node)
	if err != nil {
		return cfg, err
	}
	if raw.legacy {
		slog.Warn("profile uses the legacy flat format; treated as single — move its buttons under `single:` and add `double:`", "profile", profile, "file", path)
	}
	// Value comes in as "" for focusProgramNamePrefixOnClick: null; trim so a
	// padded value still disables via "".
	cfg.FocusProgramNamePrefixOnClick = strings.TrimSpace(raw.options[focusProgramKey])
	// Value comes in as "" for startProgram: null; trim so a padded value still
	// disables via "".
	cfg.StartProgram = strings.TrimSpace(raw.options[startProgramKey])

	singleValid := rightButtons
	if raw.legacy {
		singleValid = knownButtons
	}
	if cfg.Single, err = resolveBindings(path, profile, raw.single, singleValid); err != nil {
		return cfg, err
	}
	if cfg.Double, err = resolveBindings(path, profile, raw.double, knownButtons); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// rawProfile is one profile block before token resolution: the single and
// double button maps, and the per-profile options.
type rawProfile struct {
	single  map[string]string
	double  map[string]string
	options map[string]string
	legacy  bool // flat block: its buttons are both single and double
}

// splitProfile reads one profile block. A block with neither single: nor
// double: is the legacy flat format and becomes single (and a copy as double,
// so the profile stays usable in double mode). A block with one of the two
// must carry both, and only the per-profile options may sit beside them.
func splitProfile(path, profile string, node *yaml.Node) (rawProfile, error) {
	var block map[string]yaml.Node
	if err := node.Decode(&block); err != nil {
		return rawProfile{}, fmt.Errorf("%s: profile %s: must be a mapping of buttons, or of single and double", path, profile)
	}
	_, hasSingle := block[string(ModeSingle)]
	_, hasDouble := block[string(ModeDouble)]
	if !hasSingle && !hasDouble {
		flat := map[string]string{}
		if err := node.Decode(&flat); err != nil {
			return rawProfile{}, fmt.Errorf("%s: profile %s: buttons must map to key tokens", path, profile)
		}
		options := map[string]string{}
		for _, opt := range []string{focusProgramKey, startProgramKey} {
			if v, ok := flat[opt]; ok {
				options[opt] = v
				delete(flat, opt)
			}
		}
		return rawProfile{single: flat, double: flat, options: options, legacy: true}, nil
	}

	for _, need := range []Mode{ModeSingle, ModeDouble} {
		if _, ok := block[string(need)]; !ok {
			return rawProfile{}, fmt.Errorf("%s: profile %s: missing %s: — a profile needs both single: (right controller) and double: (both controllers)", path, profile, need)
		}
	}
	for name := range block {
		switch name {
		case string(ModeSingle), string(ModeDouble), focusProgramKey, startProgramKey:
		default:
			return rawProfile{}, fmt.Errorf("%s: profile %s: unknown key %q (valid: single, double, %s, %s)", path, profile, name, focusProgramKey, startProgramKey)
		}
	}

	var out rawProfile
	out.options = map[string]string{}
	for _, opt := range []string{focusProgramKey, startProgramKey} {
		if n, ok := block[opt]; ok {
			var v string
			if err := n.Decode(&v); err != nil {
				return rawProfile{}, fmt.Errorf("%s: profile %s: %s must be a program name or null", path, profile, opt)
			}
			out.options[opt] = v
		}
	}
	if err := decodeMode(path, profile, block, ModeSingle, &out.single); err != nil {
		return rawProfile{}, err
	}
	if err := decodeMode(path, profile, block, ModeDouble, &out.double); err != nil {
		return rawProfile{}, err
	}
	return out, nil
}

// decodeMode decodes one single:/double: block into dst. A null or scalar
// value fails here, so a typo like `double: LEFT` cannot pass silently.
func decodeMode(path, profile string, block map[string]yaml.Node, mode Mode, dst *map[string]string) error {
	n := block[string(mode)]
	if err := n.Decode(dst); err != nil {
		return fmt.Errorf("%s: profile %s: %s must be a mapping of buttons to key tokens", path, profile, mode)
	}
	if *dst == nil {
		return fmt.Errorf("%s: profile %s: %s is empty — map at least one button", path, profile, mode)
	}
	return nil
}

// resolveBindings turns one raw button map into key bindings. valid is the set
// of buttons this map may name: the right pod for single, every button for
// double and for the legacy flat format.
func resolveBindings(path, profile string, raw map[string]string, valid map[string]bool) (map[string]keys.Binding, error) {
	out := make(map[string]keys.Binding, len(raw))
	for btn, token := range raw {
		btn = strings.ToUpper(strings.TrimSpace(btn))
		if !knownButtons[btn] {
			return nil, fmt.Errorf("%s: profile %s: unknown button %q (valid: %s)", path, profile, btn, strings.Join(ButtonNames(), ", "))
		}
		if !valid[btn] {
			return nil, fmt.Errorf("%s: profile %s: button %s is a left-controller button — map it under double", path, profile, btn)
		}
		b, err := keys.Resolve(token)
		if err != nil {
			return nil, fmt.Errorf("%s: profile %s: button %s: %w", path, profile, btn, err)
		}
		out[btn] = b
	}
	return out, nil
}

// readFile reads at most maxConfigBytes of path. A longer file is refused
// before it is parsed.
func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, err
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxConfigBytes {
		return nil, fmt.Errorf("%s: larger than %d bytes", path, maxConfigBytes)
	}
	return data, nil
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
	data, rerr := readFile(path)
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
func ProfileNames[V any](m map[string]V) []string {
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
	data, err := readFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
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

// rightButtons is the right pod's button set: the only buttons single may map.
var rightButtons = func() map[string]bool {
	m := make(map[string]bool, len(zwift.RightButtons))
	for _, name := range zwift.RightButtons {
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
