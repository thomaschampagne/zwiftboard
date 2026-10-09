package ble

import (
	"fmt"
	"log/slog"
	"sync"

	"zwiftboard/internal/keys"
	"zwiftboard/internal/zwift"
)

// OnTap, when set, is told the button name on every pressed transition of a
// mapped button (the UI's click highlight). Observational only: it runs after
// the key goes down and must not block, since it is called on the BLE
// notification goroutine.
var OnTap func(button string)

var (
	heldMu sync.Mutex
	heldVK = map[uint16]bool{} // VKs logically held, shared across handlers
)

// holdKeys wraps raw down/up key funcs with per-VK idempotency shared across
// ALL handlers: the Click pair mirrors every press, so one physical press
// arrives as the same frame from both units (two handlers) and the second
// call finds the key already held. Session wires keys.Down/keys.Up through
// this. down/up stay injectable for tests.
func holdKeys(down, up func(keys.Binding)) (func(keys.Binding), func(keys.Binding)) {
	return func(b keys.Binding) {
			heldMu.Lock()
			already := heldVK[b.VK]
			heldVK[b.VK] = true
			heldMu.Unlock()
			if !already {
				down(b)
			}
		}, func(b keys.Binding) {
			heldMu.Lock()
			wasDown := heldVK[b.VK]
			delete(heldVK, b.VK)
			heldMu.Unlock()
			if wasDown {
				up(b)
			}
		}
}

// ReleaseAll releases every key still held. Called when a session dies (a
// failed keepalive = real disconnect) so a dropped controller can never leave
// a key stuck down.
func ReleaseAll() {
	heldMu.Lock()
	vks := make([]uint16, 0, len(heldVK))
	for vk := range heldVK {
		vks = append(vks, vk)
	}
	clear(heldVK)
	heldMu.Unlock()
	for _, vk := range vks {
		keys.ReleaseVK(vk)
	}
}

// ButtonHandler decodes 0x23 button frames for one controller and holds mapped
// keys down while their button is pressed: down on the pressed transition, up
// on the released transition. Two layers dedup the mirrored pair: the
// edge-triggered diff ignores identical retransmits within one handler, and
// holdKeys' per-VK idempotency absorbs the same frame arriving from the pair's
// other unit. down/up injectable for tests.
func ButtonHandler(label string, keyMap map[string]keys.Binding, down, up func(keys.Binding)) func([]byte) {
	prev := uint32(0xFFFFFFFF) // all released
	return func(b []byte) {
		// Notification callbacks run on the BLE stack's goroutines; a panic
		// here would crash the process, so absorb it and stay alive.
		defer recoverLog("button frame " + label)
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
			bnd, mapped := keyMap[name]
			if mapped {
				args = append(args, "key", bnd.Token)
				if state == "pressed" {
					down(bnd)
					if h := OnTap; h != nil {
						h(name)
					}
				} else {
					up(bnd)
				}
			}
			slog.Info("button", args...)
		}
		prev = cur
	}
}
