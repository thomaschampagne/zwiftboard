//go:build darwin

package keys

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <stdbool.h>

static void zwiftboard_key_event(CGKeyCode key, bool down) {
	CGEventRef event = CGEventCreateKeyboardEvent(NULL, key, down);
	if (event == NULL) {
		return;
	}
	CGEventPost(kCGHIDEventTap, event);
	CFRelease(event);
}
*/
import "C"

import (
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	keyRepeatDelay    = 250 * time.Millisecond
	keyRepeatInterval = 33 * time.Millisecond
)

var (
	repeatMu   sync.Mutex
	repeatStop = map[uint16]chan struct{}{}
)

// macKeyCode translates the Windows-compatible codes used by the config
// resolver into Apple's hardware-independent virtual key codes.
func macKeyCode(vk uint16) (uint16, bool) {
	switch {
	case vk >= 'A' && vk <= 'Z':
		return map[uint16]uint16{
			'A': 0, 'S': 1, 'D': 2, 'F': 3, 'H': 4, 'G': 5, 'Z': 6, 'X': 7,
			'C': 8, 'V': 9, 'B': 11, 'Q': 12, 'W': 13, 'E': 14, 'R': 15,
			'Y': 16, 'T': 17, 'U': 32, 'I': 34, 'O': 31, 'P': 35, 'L': 37,
			'J': 38, 'K': 40, 'N': 45, 'M': 46,
		}[vk], true
	case vk >= '0' && vk <= '9':
		return map[uint16]uint16{
			'0': 29, '1': 18, '2': 19, '3': 20, '4': 21,
			'5': 23, '6': 22, '7': 26, '8': 28, '9': 25,
		}[vk], true
	}
	codes := map[uint16]uint16{
		0x09: 48, 0x0D: 36, 0x20: 49, 0x1B: 53, 0x08: 51,
		0x2E: 117, 0x2D: 114, 0x24: 115, 0x23: 119,
		0x21: 116, 0x22: 121, 0x26: 126, 0x28: 125,
		0x25: 123, 0x27: 124, 0x10: 56, 0x11: 59, 0x12: 58,
		0x14: 57, 0x70: 122, 0x71: 120, 0x72: 99, 0x73: 118,
		0x74: 96, 0x75: 97, 0x76: 98, 0x77: 100, 0x78: 101,
		0x79: 109, 0x7A: 103, 0x7B: 111,
		0xBD: 27, 0xBB: 24, 0xBC: 43, 0xBE: 47, 0xBF: 44,
		0xBA: 41, 0xDE: 39, 0xDB: 33, 0xDD: 30, 0xC0: 50, 0xDC: 42,
	}
	code, ok := codes[vk]
	return code, ok
}

func Tap(b Binding) {
	Down(b)
	Up(b)
}

func Down(b Binding) bool {
	if !focusTarget() {
		return false
	}
	code, ok := macKeyCode(b.VK)
	if !ok {
		slog.Warn("unsupported macOS key", "key", b.Token, "code", b.VK)
		return false
	}
	startRepeat(code)
	C.zwiftboard_key_event(C.CGKeyCode(code), C.bool(true))
	return true
}

func Up(b Binding) bool {
	code, ok := macKeyCode(b.VK)
	if !ok {
		return false
	}
	stopRepeat(code)
	C.zwiftboard_key_event(C.CGKeyCode(code), C.bool(false))
	return true
}

func ReleaseVK(vk uint16) {
	if code, ok := macKeyCode(vk); ok {
		stopRepeat(code)
		C.zwiftboard_key_event(C.CGKeyCode(code), C.bool(false))
	}
}

func startRepeat(code uint16) {
	repeatMu.Lock()
	if _, ok := repeatStop[code]; ok {
		repeatMu.Unlock()
		return
	}
	stop := make(chan struct{})
	repeatStop[code] = stop
	repeatMu.Unlock()
	go func() {
		timer := time.NewTimer(keyRepeatDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-stop:
			return
		}
		ticker := time.NewTicker(keyRepeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				C.zwiftboard_key_event(C.CGKeyCode(code), C.bool(true))
			}
		}
	}()
}

func stopRepeat(code uint16) {
	repeatMu.Lock()
	stop, ok := repeatStop[code]
	delete(repeatStop, code)
	repeatMu.Unlock()
	if ok {
		close(stop)
	}
}

// TargetWindowPresent checks whether the configured application is running.
// The actual activation is done immediately before the key event.
func TargetWindowPresent() bool {
	target := strings.TrimSpace(CurrentWindowTarget())
	if target == "" {
		return true
	}
	return exec.Command("pgrep", "-if", target).Run() == nil
}

func focusTarget() bool {
	target := strings.TrimSpace(CurrentWindowTarget())
	if target == "" {
		return true
	}
	if err := exec.Command("open", "-a", target).Run(); err != nil {
		slog.Warn("focus program not found or inaccessible — tap dropped", "program", target, "error", err)
		return false
	}
	return true
}
