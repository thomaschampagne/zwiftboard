package keys

import (
	"sync"
	"time"
)

const (
	flagCaps      uint64 = 1 << 16
	flagShift     uint64 = 1 << 17
	flagControl   uint64 = 1 << 18
	flagAlternate uint64 = 1 << 19
)

type quartzEvent struct {
	code         uint16
	down, repeat bool
	flags        uint64
}
type quartzHold struct{ cancel func() }

// All event posts and timer callbacks share mu: a callback already queued when
// Stop runs must validate its hold identity before it can post another down.
type quartzKeyboard struct {
	mu            sync.Mutex
	post          func(quartzEvent) bool
	physicalFlags func() uint64
	trusted       func() bool
	target        func() string
	after         func(time.Duration, func()) func()
	held          map[uint16]*quartzHold
	flags         uint64
	stopped       bool
}

func newQuartzKeyboard(post func(quartzEvent) bool, trusted func() bool, target func() string, after func(time.Duration, func()) func(), physicalFlags func() uint64) *quartzKeyboard {
	return &quartzKeyboard{physicalFlags: physicalFlags, post: post, trusted: trusted, target: target, after: after, held: make(map[uint16]*quartzHold)}
}
func modifierFlag(vk uint16) uint64 {
	switch vk {
	case 0x10:
		return flagShift
	case 0x11:
		return flagControl
	case 0x12:
		return flagAlternate
	}
	return 0
}
func (k *quartzKeyboard) allowed() bool { return !k.stopped && k.target() == "" && k.trusted() }
func (k *quartzKeyboard) down(vk uint16) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	code, ok := darwinKeycode(vk)
	if !ok || !k.allowed() {
		return false
	}
	if _, ok := k.held[vk]; ok {
		return true
	}
	flags := k.flags | modifierFlag(vk)
	if vk == 0x14 {
		flags ^= flagCaps
	}
	if !k.post(quartzEvent{code: code, down: true, flags: k.eventFlags(flags)}) {
		return false
	}
	k.flags = flags
	h := &quartzHold{}
	k.held[vk] = h
	if modifierFlag(vk) == 0 && vk != 0x14 {
		k.schedule(vk, h, 250*time.Millisecond)
	}
	return true
}
func (k *quartzKeyboard) schedule(vk uint16, h *quartzHold, delay time.Duration) {
	h.cancel = k.after(delay, func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		if k.held[vk] != h || k.stopped {
			return
		}
		if k.allowed() {
			code, _ := darwinKeycode(vk)
			k.post(quartzEvent{code: code, down: true, repeat: true, flags: k.eventFlags(k.flags)})
		}
		k.schedule(vk, h, 33*time.Millisecond)
	})
}
func (k *quartzKeyboard) up(vk uint16) bool { k.mu.Lock(); defer k.mu.Unlock(); return k.release(vk) }
func (k *quartzKeyboard) release(vk uint16) bool {
	h, ok := k.held[vk]
	if !ok {
		return true
	}
	delete(k.held, vk)
	if h.cancel != nil {
		h.cancel()
	}
	k.flags &^= modifierFlag(vk)
	code, _ := darwinKeycode(vk)
	// Releases bypass focus/trust gating so changing profiles or permissions never
	// deliberately leaves a previously posted key held. Quartz may still deny it.
	return k.post(quartzEvent{code: code, flags: k.eventFlags(k.flags)})
}
func (k *quartzKeyboard) shutdown() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.stopped = true
	for vk := range k.held {
		k.release(vk)
	}
}

// Diagnostics read only immutable dependencies; no event-state lock is needed
// while asking macOS for current trust status.
func (k *quartzKeyboard) inputWarning() string {
	if k.target() != "" {
		return "macOS focus targets are unsupported; clear focusProgramNamePrefixOnClick to enable keyboard input"
	}
	if !k.trusted() {
		return "Keyboard input disabled: grant Accessibility access in System Settings > Privacy & Security > Accessibility, then restart zwiftboard"
	}
	return ""
}

// Hardware flags come from the HID source table, never the combined session
// table containing our own posted modifiers. Sample for every event so physical
// changes during a synthetic hold are reflected in repeats and releases.
func (k *quartzKeyboard) eventFlags(synthetic uint64) uint64 {
	physical := k.physicalFlags()
	// Caps Lock is a latch: each synthetic press inverts the physical baseline.
	// Unlike held modifiers, OR would prevent toggling preexisting Caps Lock off.
	return ((physical | synthetic) &^ flagCaps) | ((physical ^ synthetic) & flagCaps)
}
