// Package config loads the YAML configuration (currently a flat button ->
// key-token map; profiles land in a later change).
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

// Load reads path and returns the button -> key binding map.
// A missing file is not an error: returns nil and logging keeps working.
func Load(path string) (map[string]keys.Binding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("no key mapping (%s not found) — button logging only", path)
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]string
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	out := make(map[string]keys.Binding, len(raw))
	for btn, token := range raw {
		btn = strings.ToUpper(strings.TrimSpace(btn))
		if !knownButtons[btn] {
			return nil, fmt.Errorf("%s: unknown button %q (valid: %s)", path, btn, strings.Join(ButtonNames(), ", "))
		}
		b, err := keys.Resolve(token)
		if err != nil {
			return nil, fmt.Errorf("%s: button %s: %w", path, btn, err)
		}
		out[btn] = b
	}
	for _, name := range ButtonNames() {
		if b, ok := out[name]; ok {
			log.Printf("key: %-5s -> %s", name, b.Token)
		}
	}
	return out, nil
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
