package main

import (
	"testing"
	"time"
)

func resetTapState(t *testing.T) {
	t.Helper()
	tapMu.Lock()
	lastTapByName = map[string]time.Time{}
	tapMu.Unlock()
	old := tapDebounce
	tapDebounce = 50 * time.Millisecond
	t.Cleanup(func() {
		tapDebounce = old
		tapMu.Lock()
		lastTapByName = map[string]time.Time{}
		tapMu.Unlock()
	})
}

func TestButtonHandlerDebounce(t *testing.T) {
	resetTapState(t)

	keys := map[string]keyBinding{"LEFT": {vk: 0x25, token: "left"}}
	taps := 0
	h := buttonHandler("t", keys, func(keyBinding) { taps++ })

	press := []byte{0x23, 0x08, 0xFE, 0xFF, 0xFF, 0xFF, 0x0F}
	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}

	h(press) // real press -> tap
	h(idle)  // bounce release
	h(press) // bounce re-press inside window -> duplicate
	if taps != 1 {
		t.Fatalf("after bounce taps = %d, want 1", taps)
	}

	// identical retransmit must not double-tap either (edge logic)
	h(press)
	if taps != 1 {
		t.Fatalf("after identical retransmit taps = %d, want 1", taps)
	}

	time.Sleep(60 * time.Millisecond)
	h(idle)
	h(press) // outside window -> tap
	if taps != 2 {
		t.Fatalf("after window taps = %d, want 2", taps)
	}
}

// The Click V2 pair mirrors button state: one physical press arrives as the
// same frame from BOTH controllers. Dedup must work across handlers.
func TestCrossControllerDuplicateTap(t *testing.T) {
	resetTapState(t)

	keys := map[string]keyBinding{"B": {vk: 0x42, token: "b"}}
	taps := 0
	tap := func(keyBinding) { taps++ }
	right := buttonHandler("right", keys, tap)
	left := buttonHandler("left", keys, tap)

	press := []byte{0x23, 0x08, 0xDF, 0xFF, 0xFF, 0xFF, 0x0F}
	right(press)
	left(press) // mirrored frame from the other unit
	if taps != 1 {
		t.Fatalf("mirrored press taps = %d, want 1", taps)
	}
}

func TestButtonHandlerUnmappedButtonNoTap(t *testing.T) {
	resetTapState(t)
	tapDebounce = 0

	taps := 0
	h := buttonHandler("t", nil, func(keyBinding) { taps++ })
	h([]byte{0x23, 0x08, 0xFE, 0xFF, 0xFF, 0xFF, 0x0F})
	if taps != 0 {
		t.Fatalf("taps = %d, want 0 with empty key map", taps)
	}
}
