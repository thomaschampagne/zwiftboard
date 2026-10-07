// Package keys resolves config tokens to Windows virtual-key codes and taps
// them into the focused window.
package keys

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Binding is one resolved button -> keyboard key edge.
type Binding struct {
	VK    uint16 // Windows virtual-key code
	Token string // as written in config.yaml, for logging
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

// Resolve turns a config token into a key binding.
func Resolve(token string) (Binding, error) {
	tok := strings.ToLower(strings.TrimSpace(token))
	if tok == "" {
		return Binding{}, fmt.Errorf("empty key")
	}
	if vk, ok := namedKeys[tok]; ok {
		return Binding{VK: vk, Token: tok}, nil
	}
	if len(tok) >= 2 && len(tok) <= 3 && tok[0] == 'f' {
		if n, err := strconv.Atoi(tok[1:]); err == nil && n >= 1 && n <= 12 {
			return Binding{VK: firstFunctionKeyVK + uint16(n-1), Token: tok}, nil
		}
	}
	if len(tok) == 1 {
		c := tok[0]
		if c >= 'a' && c <= 'z' {
			return Binding{VK: uint16('A' + (c - 'a')), Token: tok}, nil
		}
		if c >= '0' && c <= '9' {
			return Binding{VK: uint16(c), Token: tok}, nil
		}
	}
	return Binding{}, fmt.Errorf("unknown key %q (use a-z, 0-9, f1-f12 or a named key: %s)", token, namedKeyList())
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
	sort.Strings(names)
	return strings.Join(names, ", ")
}
