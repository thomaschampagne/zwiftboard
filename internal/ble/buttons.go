package ble

import (
	"fmt"
	"log/slog"
	"sync"

	"zwiftboard/internal/keys"
	"zwiftboard/internal/zwift"
)

// OnKey, when set, is told every transition of a mapped button: down=true on
// press, down=false on release (the UI's live key state). Observational only:
// it runs on the BLE notification goroutine and must not block, since nothing
// in a session waits on it. The pair mirrors every frame, so one physical
// transition fires it twice — the UI dedups.
var OnKey func(button string, down bool)

// OnLifted, when set, is told each VK force-released by ReleaseAll (a session
// dropped mid-hold: the release edge never arrived). Normal releases are
// reported through OnKey instead. Same observational rules.
var OnLifted func(vk uint16)

var (
	keyMu sync.Mutex
	// heldCount is how many LIVE handlers currently see each VK pressed.
	// The Click pair mirrors every press — one physical press arrives as the
	// same frame from both units (two handlers) — so a healthy pair holds 2
	// for a pressed key. The count is the whole dedup: the first handler to
	// see the press sends Down, the last to see the release sends Up, and a
	// session dying releases only a key no handler sees pressed anymore.
	heldCount = map[uint16]int{}
)

// Holds is one session's handle on the shared held-key state. Session wires
// keys.Down/keys.Up through it for its ButtonHandler (Press/Release) and
// defers ReleaseAll, so a dropped controller can never leave a key stuck
// down — while a key the sibling session still sees pressed stays down (that
// sibling's own release lifts it).
type Holds struct {
	down func(keys.Binding) bool
	up   func(keys.Binding) bool
	own  map[uint16]bool // VKs THIS handler currently sees pressed
}

// holdKeys wraps raw down/up key funcs with the per-VK refcount shared
// across ALL handlers. down/up stay injectable for tests; return whether the
// key was actually pressed/released.
func holdKeys(down, up func(keys.Binding) bool) *Holds {
	return &Holds{down: down, up: up, own: map[uint16]bool{}}
}

// Press is called on a pressed transition of a mapped button. It reserves the
// VK in the shared count (deduping the mirrored duplicate from the pair's
// other unit), sends Down on the first reservation only, and undoes the
// reservation when the press was dropped (e.g. no focus window) so a later
// press can still land.
func (h *Holds) Press(b keys.Binding) {
	keyMu.Lock()
	h.own[b.VK] = true
	heldCount[b.VK]++
	first := heldCount[b.VK] == 1
	keyMu.Unlock()
	if !first {
		return
	}
	if !h.down(b) {
		keyMu.Lock()
		delete(h.own, b.VK)
		heldCount[b.VK]--
		if heldCount[b.VK] == 0 {
			delete(heldCount, b.VK)
		}
		keyMu.Unlock()
	}
}

// Release is called on a released transition. It removes this handler's
// reservation and sends Up when no live handler sees the key pressed anymore.
func (h *Holds) Release(b keys.Binding) {
	keyMu.Lock()
	if !h.own[b.VK] {
		keyMu.Unlock() // never saw it pressed (e.g. subscribed mid-hold): nothing to lift
		return
	}
	delete(h.own, b.VK)
	if heldCount[b.VK] > 0 {
		heldCount[b.VK]--
	}
	last := heldCount[b.VK] == 0
	keyMu.Unlock()
	if last {
		h.up(b)
	}
}

// ReleaseAll frees every key this session held and no other live handler
// still sees pressed. Session defers it, so a keepalive failure (real
// disconnect) can never leave a key stuck down. A key the sibling session
// still sees pressed is left down: the same mirrored release is on its way to
// that sibling and will lift it.
func (h *Holds) ReleaseAll() {
	keyMu.Lock()
	for vk := range h.own {
		delete(h.own, vk)
		if heldCount[vk] > 0 {
			heldCount[vk]--
		}
	}
	lift := make([]uint16, 0, len(heldCount))
	for vk, n := range heldCount {
		if n <= 0 {
			delete(heldCount, vk)
			lift = append(lift, vk)
		}
	}
	keyMu.Unlock()
	for _, vk := range lift {
		keys.ReleaseVK(vk)
		if h := OnLifted; h != nil {
			h(vk)
		}
	}
}

// ButtonHandler decodes 0x23 button frames for one controller and holds mapped
// keys down while their button is pressed: Press on the pressed transition, Up
// on the released transition. Two layers dedup the mirrored pair: the
// edge-triggered diff ignores identical retransmits within one handler, and
// the shared heldCount absorbs the same frame arriving from the pair's other
// unit. down/up injectable for tests.
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
					if h := OnKey; h != nil {
						h(name, true)
					}
				} else {
					up(bnd)
					if h := OnKey; h != nil {
						h(name, false)
					}
				}
			}
			slog.Info("button", args...)
		}
		prev = cur
	}
}
