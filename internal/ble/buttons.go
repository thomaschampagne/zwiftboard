package ble

import (
	"fmt"
	"log/slog"
	"time"

	"zwiftboard/internal/keys"
	"zwiftboard/internal/zwift"
)

// claimTap reserves the right to tap key for button name. Shared by ALL
// controllers: the Click V2 pair mirrors button state (one press produces the
// same frame from both units within milliseconds), so the dedup window must be
// global, not per-device. Returns false if the same button was claimed within
// TapDebounce.
func claimTap(name string) bool {
	tapMu.Lock()
	defer tapMu.Unlock()
	now := time.Now()
	if t0, ok := lastTapByName[name]; ok && now.Sub(t0) < TapDebounce {
		return false
	}
	lastTapByName[name] = now
	return true
}

// ButtonHandler decodes 0x23 button frames for one controller and taps keys
// for mapped presses. tap is injectable for tests.
func ButtonHandler(label string, keyMap map[string]keys.Binding, tap func(keys.Binding)) func([]byte) {
	prev := uint32(0xFFFFFFFF) // all released
	return func(b []byte) {
		if len(b) == 0 {
			return
		}
		slog.Debug("raw frame", "controller", label, "frame", fmt.Sprintf("% X", b))
		if b[0] != zwift.MsgKeyPad {
			return // 0x15 / 0x19 idle & status frames
		}
		cur, ok := zwift.Bitmap(b[1:])
		if !ok {
			return
		}
		changed := cur ^ prev
		if !debugEnabled() {
			changed &= zwift.KnownMask
		}
		for bit := 0; bit < 32; bit++ {
			m := uint32(1) << bit
			if changed&m == 0 {
				continue
			}
			name, known := zwift.Buttons[m]
			if !known {
				name = fmt.Sprintf("BIT%d", bit)
			}
			state := "released"
			if cur&m == 0 {
				state = "pressed"
			}
			args := []any{"controller", label, "button", name, "state", state}
			if state == "pressed" {
				if bnd, mapped := keyMap[name]; mapped {
					// claimTap is GLOBAL across controllers: the pair mirrors
					// button state, so one physical press arrives as the same
					// frame from both units (and possibly retransmitted).
					// One claim per button inside the window = one key.
					args = append(args, "key", bnd.Token)
					if claimTap(name) {
						tap(bnd)
					} else {
						args = append(args, "duplicate", true)
					}
				}
			}
			slog.Info("button", args...)
		}
		prev = cur
	}
}
