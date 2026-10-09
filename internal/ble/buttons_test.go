package ble

import (
	"sync"
	"testing"
	"time"

	"zwiftboard/internal/keys"
)

func resetTapState(t *testing.T) {
	t.Helper()
	tapMu.Lock()
	lastTapByName = map[string]time.Time{}
	tapMu.Unlock()
	old := TapDebounce
	TapDebounce = 50 * time.Millisecond
	t.Cleanup(func() {
		TapDebounce = old
		tapMu.Lock()
		lastTapByName = map[string]time.Time{}
		tapMu.Unlock()
	})
}

func TestButtonHandlerDebounce(t *testing.T) {
	resetTapState(t)

	keyMap := map[string]keys.Binding{"LEFT": {VK: 0x25, Token: "left"}}
	taps := 0
	h := ButtonHandler("t", keyMap, func(keys.Binding) { taps++ })

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

	keyMap := map[string]keys.Binding{"B": {VK: 0x42, Token: "b"}}
	taps := 0
	tap := func(keys.Binding) { taps++ }
	right := ButtonHandler("right", keyMap, tap)
	left := ButtonHandler("left", keyMap, tap)

	press := []byte{0x23, 0x08, 0xDF, 0xFF, 0xFF, 0xFF, 0x0F}
	right(press)
	left(press) // mirrored frame from the other unit
	if taps != 1 {
		t.Fatalf("mirrored press taps = %d, want 1", taps)
	}
}

func TestButtonHandlerPanicRecovered(t *testing.T) {
	resetTapState(t)

	// Simulate a runtime failure inside the tap path (e.g. a Windows BLE
	// callback hiccup): with recoverLog installed, the panic must not escape
	// the handler and later frames must still be processed.
	var mu sync.Mutex
	taps := 0
	burst := 0
	h := ButtonHandler("t", map[string]keys.Binding{"A": {VK: 0x41, Token: "a"}}, func(keys.Binding) {
		mu.Lock()
		burst++
		n := burst
		mu.Unlock()
		if n == 1 {
			panic("boom")
		}
		mu.Lock()
		taps++
		mu.Unlock()
	})

	press := []byte{0x23, 0x08, 0xEF, 0xFF, 0xFF, 0xFF, 0x0F} // A pressed
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic escaped the button handler: %v", r)
			}
		}()
		h(press) // first press panics inside tap; the handler must absorb it
	}()
	time.Sleep(60 * time.Millisecond) // out of the 50ms debounce window
	h(press)                          // a later press must still land
	mu.Lock()
	defer mu.Unlock()
	if taps != 1 {
		t.Fatalf("after panic taps = %d, want 1 subsequent tap to still land", taps)
	}
}

func TestButtonHandlerUnmappedButtonNoTap(t *testing.T) {
	resetTapState(t)
	TapDebounce = 0

	taps := 0
	h := ButtonHandler("t", nil, func(keys.Binding) { taps++ })
	h([]byte{0x23, 0x08, 0xFE, 0xFF, 0xFF, 0xFF, 0x0F})
	if taps != 0 {
		t.Fatalf("taps = %d, want 0 with empty key map", taps)
	}
}

func TestOnTapCalledOncePerPress(t *testing.T) {
	resetTapState(t)
	var got []string
	OnTap = func(b string) { got = append(got, b) }
	t.Cleanup(func() { OnTap = nil })

	keyMap := map[string]keys.Binding{"B": {VK: 0x42, Token: "b"}}
	tap := func(keys.Binding) {}
	right := ButtonHandler("right", keyMap, tap)
	left := ButtonHandler("left", keyMap, tap)

	press := []byte{0x23, 0x08, 0xDF, 0xFF, 0xFF, 0xFF, 0x0F}
	right(press)
	left(press) // mirrored duplicate
	if len(got) != 1 || got[0] != "B" {
		t.Fatalf("OnTap calls = %v, want [B]", got)
	}
}

func TestOnTapNotCalledUnmapped(t *testing.T) {
	resetTapState(t)
	called := false
	OnTap = func(string) { called = true }
	t.Cleanup(func() { OnTap = nil })

	h := ButtonHandler("t", map[string]keys.Binding{}, func(keys.Binding) {})
	h([]byte{0x23, 0x08, 0xDF, 0xFF, 0xFF, 0xFF, 0x0F})
	if called {
		t.Fatal("OnTap must not fire for an unmapped button")
	}
}
