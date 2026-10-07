// Package config loads the YAML configuration: a map of named profiles, each
// mapping button name -> keyboard key token, plus global settings.
package config

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"zwiftclickv2-keyboard/internal/keys"
	"zwiftclickv2-keyboard/internal/zwift"
)

// DefaultProfile is used when -p/--profile is not given.
const DefaultProfile = "mywhoosh"

// doc is the config.yaml structure.
type doc struct {
	Profiles map[string]map[string]string `yaml:"profiles"`
}

// Load reads path and returns the button -> key binding map for profile.
// An empty profile name selects DefaultProfile. A missing file is not an
// error: returns nil and logging keeps working.
func Load(path, profile string) (map[string]keys.Binding, error) {
	if profile == "" {
		profile = DefaultProfile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("no key mapping (%s not found) — button logging only", path)
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var d doc
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(d.Profiles) == 0 {
		return nil, fmt.Errorf("%s: no profiles found — expected a top-level `profiles:` map, each profile mapping buttons to keys (see config.yaml)", path)
	}
	raw, ok := d.Profiles[profile]
	if !ok {
		return nil, fmt.Errorf("%s: profile %q not found (available: %s)", path, profile, strings.Join(ProfileNames(d.Profiles), ", "))
	}

	out := make(map[string]keys.Binding, len(raw))
	for btn, token := range raw {
		btn = strings.ToUpper(strings.TrimSpace(btn))
		if !knownButtons[btn] {
			return nil, fmt.Errorf("%s: profile %s: unknown button %q (valid: %s)", path, profile, btn, strings.Join(ButtonNames(), ", "))
		}
		b, err := keys.Resolve(token)
		if err != nil {
			return nil, fmt.Errorf("%s: profile %s: button %s: %w", path, profile, btn, err)
		}
		out[btn] = b
	}
	log.Printf("profile %q: %d binding(s)", profile, len(out))
	for _, name := range ButtonNames() {
		if b, ok := out[name]; ok {
			log.Printf("key: %-5s -> %s", name, b.Token)
		}
	}
	return out, nil
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
