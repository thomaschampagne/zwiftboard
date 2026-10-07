package main

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// keyBinding is one resolved button -> keyboard key edge.
type keyBinding struct {
	vk    uint16 // Windows virtual-key code
	token string // as written in config.yaml, for logging
}

// namedKeys maps config tokens to Windows virtual-key codes.
var namedKeys = map[string]uint16{
	"up":        0x26, // VK_UP
	"down":      0x28, // VK_DOWN
	"left":      0x25, // VK_LEFT
	"right":     0x27, // VK_RIGHT
	"enter":     0x0D, // VK_RETURN
	"space":     0x20, // VK_SPACE
	"tab":       0x09, // VK_TAB
	"esc":       0x1B, // VK_ESCAPE
	"escape":    0x1B,
	"backspace": 0x08, // VK_BACK
	"delete":    0x2E, // VK_DELETE
	"insert":    0x2D, // VK_INSERT
	"home":      0x24, // VK_HOME
	"end":       0x23, // VK_END
	"pageup":    0x21, // VK_PRIOR
	"pagedown":  0x22, // VK_NEXT
	"shift":     0x10, // VK_SHIFT
	"ctrl":      0x11, // VK_CONTROL
	"alt":       0x12, // VK_MENU
	"capslock":  0x14, // VK_CAPITAL
	"-":         0xBD, // VK_OEM_MINUS
	"=":         0xBB, // VK_OEM_PLUS
	",":         0xBC, // VK_OEM_COMMA
	".":         0xBE, // VK_OEM_PERIOD
	"/":         0xBF, // VK_OEM_2
	";":         0xBA, // VK_OEM_1
	"'":         0xDE, // VK_OEM_7
	"[":         0xDB, // VK_OEM_4
	"]":         0xDD, // VK_OEM_6
	"`":         0xC0, // VK_OEM_3
	"\\":        0xDC, // VK_OEM_5
}

const firstFunctionKeyVK = 0x70 // VK_F1

// resolveKey turns a config token into a virtual-key code.
func resolveKey(token string) (uint16, error) {
	tok := strings.ToLower(strings.TrimSpace(token))
	if tok == "" {
		return 0, fmt.Errorf("empty key")
	}
	if vk, ok := namedKeys[tok]; ok {
		return vk, nil
	}
	if len(tok) >= 2 && len(tok) <= 3 && tok[0] == 'f' {
		if n, err := strconv.Atoi(tok[1:]); err == nil && n >= 1 && n <= 12 {
			return firstFunctionKeyVK + uint16(n-1), nil
		}
	}
	if len(tok) == 1 {
		c := tok[0]
		if c >= 'a' && c <= 'z' {
			return uint16('A' + (c - 'a')), nil
		}
		if c >= '0' && c <= '9' {
			return uint16(c), nil
		}
	}
	return 0, fmt.Errorf("unknown key %q (use a-z, 0-9, f1-f12 or a named key: %s)", token, namedKeyList())
}

func namedKeyList() string {
	names := make([]string, 0, len(namedKeys))
	seen := map[string]bool{}
	for k := range namedKeys {
		if !seen[k] {
			names = append(names, k)
			seen[k] = true
		}
	}
	return strings.Join(names, ", ")
}

// knownButtonNames holds the button names accepted as YAML keys.
var knownButtonNames = func() map[string]bool {
	m := make(map[string]bool, len(buttons))
	for _, name := range buttons {
		m[name] = true
	}
	return m
}()

// loadKeyMap reads config.yaml (button name -> key token) from path.
// Missing file is not an error: returns nil and logging keeps working.
// Bad YAML, unknown buttons or unresolvable keys are fatal.
func loadKeyMap(path string) map[string]keyBinding {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("no key mapping (%s not found) — button logging only", path)
			return nil
		}
		log.Fatalf("read %s: %v", path, err)
	}
	var raw map[string]string
	if err := yaml.Unmarshal(data, &raw); err != nil {
		log.Fatalf("parse %s: %v", path, err)
	}

	out := make(map[string]keyBinding, len(raw))
	for btn, token := range raw {
		btn = strings.ToUpper(strings.TrimSpace(btn))
		if !knownButtonNames[btn] {
			log.Fatalf("%s: unknown button %q (valid: %s)", path, btn, strings.Join(sortedButtons(), ", "))
		}
		vk, err := resolveKey(token)
		if err != nil {
			log.Fatalf("%s: button %s: %v", path, btn, err)
		}
		out[btn] = keyBinding{vk: vk, token: strings.ToLower(strings.TrimSpace(token))}
	}
	for _, name := range sortedButtons() {
		if b, ok := out[name]; ok {
			log.Printf("key: %-5s -> %s", name, b.token)
		}
	}
	return out
}

func sortedButtons() []string {
	names := make([]string, 0, len(buttons))
	for _, n := range buttons {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
