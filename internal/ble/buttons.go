package ble

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

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
	// own counts how many of THIS handler's buttons currently hold the VK.
	// A count, not a flag: two different buttons of one handler can map to
	// the same key (the mirrored right-pod press plus the left pod's own
	// button), and the key must stay down until the LAST of them is
	// released. With a flag the first release wiped it and the second
	// release found nothing to lift — the key stuck down for the process.
	own map[uint16]int
}

// holdKeys wraps raw down/up key funcs with the per-VK refcount shared
// across ALL handlers. down/up stay injectable for tests; return whether the
// key was actually pressed/released.
func holdKeys(down, up func(keys.Binding) bool) *Holds {
	return &Holds{down: down, up: up, own: map[uint16]int{}}
}

// Press is called on a pressed transition of a mapped button. It reserves the
// VK in the shared count (deduping the mirrored duplicate from the pair's
// other unit), sends Down on the first reservation only, and undoes the
// reservation when the press was dropped (e.g. no focus window) so a later
// press can still land.
func (h *Holds) Press(b keys.Binding) {
	keyMu.Lock()
	h.own[b.VK]++
	heldCount[b.VK]++
	first := heldCount[b.VK] == 1
	keyMu.Unlock()
	if !first {
		return
	}
	if !h.down(b) {
		keyMu.Lock()
		// Give back exactly what this press took: ONE reservation, not the
		// whole key — another handler (or another button of this one) may
		// still hold it.
		if h.own[b.VK] > 0 {
			h.own[b.VK]--
		}
		if heldCount[b.VK] > 0 {
			heldCount[b.VK]--
		}
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
	if h.own[b.VK] == 0 {
		keyMu.Unlock() // never saw it pressed (e.g. subscribed mid-hold): nothing to lift
		return
	}
	h.own[b.VK]--
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
	for vk, n := range h.own {
		// Give back exactly what this handler took: its whole count, not
		// one — it may hold the same VK through several buttons.
		heldCount[vk] -= n
		if heldCount[vk] < 0 {
			heldCount[vk] = 0
		}
	}
	h.own = map[uint16]int{}
	lift := make([]uint16, 0, len(heldCount))
	for vk, n := range heldCount {
		if n <= 0 {
			delete(heldCount, vk)
			lift = append(lift, vk)
		}
	}
	keyMu.Unlock()
	releaseVKs(lift)
}

// KeyMap is the button→key binding map, swappable at runtime while BLE
// notification goroutines read it. Runtime profile switching replaces the map
// without reconnecting BLE, so storage is an atomic pointer: a Swap is seen by
// the next Get and no lock is held across a lookup.
type KeyMap struct {
	m atomic.Pointer[map[string]keys.Binding]
}

// NewKeyMap wraps a binding map. A nil map is valid and reads as unmapped.
func NewKeyMap(m map[string]keys.Binding) *KeyMap {
	km := &KeyMap{}
	km.Swap(m)
	return km
}

// Get reports the binding for a button and whether it is mapped.
func (k *KeyMap) Get(button string) (keys.Binding, bool) {
	m := k.m.Load()
	if m == nil {
		return keys.Binding{}, false
	}
	b, ok := (*m)[button]
	return b, ok
}

// Swap replaces the binding map in place; the next Get sees it.
func (k *KeyMap) Swap(m map[string]keys.Binding) {
	if m == nil {
		m = map[string]keys.Binding{}
	}
	k.m.Store(&m)
}

// releaseVKs force-releases each virtual key and tells the UI (OnLifted) so a
// dropped controller leaves no stale held row. Callers own the heldCount
// bookkeeping; this only performs the lifts.
func releaseVKs(vks []uint16) {
	for _, vk := range vks {
		keys.ReleaseVK(vk)
		if h := OnLifted; h != nil {
			h(vk)
		}
	}
}

// ReleaseAllHeld force-releases every key any handler currently holds: it
// clears the shared held count and lifts each VK (keys.ReleaseVK + OnLifted).
// A profile switch calls it so a key held across the swap cannot stick down.
func ReleaseAllHeld() {
	keyMu.Lock()
	vks := make([]uint16, 0, len(heldCount))
	for vk := range heldCount {
		vks = append(vks, vk)
	}
	heldCount = map[uint16]int{}
	keyMu.Unlock()
	releaseVKs(vks)
}

// ButtonHandler decodes 0x23 button frames for one controller and holds mapped
// keys down while their button is pressed: Press on the pressed transition, Up
// on the released transition. Two layers dedup the mirrored pair: the
// edge-triggered diff ignores identical retransmits within one handler, and
// the shared heldCount absorbs the same frame arriving from the pair's other
// unit. down/up injectable for tests.
func ButtonHandler(label string, km *KeyMap, down, up func(keys.Binding)) func([]byte) {
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
			bnd, mapped := km.Get(name)
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
