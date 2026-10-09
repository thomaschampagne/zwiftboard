package ble

import (
	"sync"
	"testing"

	"zwiftboard/internal/keys"
)

// keyRecorder counts down/up calls so tests can assert hold semantics.
type keyRecorder struct {
	mu   sync.Mutex
	down int
	up   int
}

func (r *keyRecorder) downFn(keys.Binding) {
	r.mu.Lock()
	r.down++
	r.mu.Unlock()
}

func (r *keyRecorder) upFn(keys.Binding) {
	r.mu.Lock()
	r.up++
	r.mu.Unlock()
}

func (r *keyRecorder) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.down, r.up
}

// One press/release cycle = one down + one up, and a bounce (quick
// release/re-press) must not swallow the second down.
func TestButtonHandlerDownUp(t *testing.T) {
	rec := &keyRecorder{}
	h := ButtonHandler("t", map[string]keys.Binding{"LEFT": {VK: 0x25, Token: "left"}}, rec.downFn, rec.upFn)

	press := []byte{0x23, 0x08, 0xFE, 0xFF, 0xFF, 0xFF, 0x0F}
	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}

	h(press)
	h(idle)
	h(press) // bounce re-press: edge logic must down again
	h(idle)

	if down, up := rec.counts(); down != 2 || up != 2 {
		t.Fatalf("down = %d, up = %d, want 2/2", down, up)
	}
}

// The Click V2 pair mirrors button state: one physical press arrives as the
// same frame from BOTH controllers (two handlers). holdKeys is idempotent per
// VK, so the mirrored frame must not press the key again.
func TestCrossControllerMirrorSingleDown(t *testing.T) {
	rec := &keyRecorder{}
	rDown, rUp := holdKeys(rec.downFn, rec.upFn)
	lDown, lUp := holdKeys(rec.downFn, rec.upFn)
	right := ButtonHandler("right", map[string]keys.Binding{"B": {VK: 0x42, Token: "b"}}, rDown, rUp)
	left := ButtonHandler("left", map[string]keys.Binding{"B": {VK: 0x42, Token: "b"}}, lDown, lUp)

	press := []byte{0x23, 0x08, 0xDF, 0xFF, 0xFF, 0xFF, 0x0F}
	right(press)
	left(press) // mirrored frame from the other unit
	t.Cleanup(ReleaseAll)

	if down, _ := rec.counts(); down != 1 {
		t.Fatalf("mirrored press downs = %d, want 1", down)
	}
}

// Retransmission of the same frame (same bitmap) is not a new transition.
func TestIdenticalRetransmitNoDoubleDown(t *testing.T) {
	rec := &keyRecorder{}
	h := ButtonHandler("t", map[string]keys.Binding{"LEFT": {VK: 0x25, Token: "left"}}, rec.downFn, rec.upFn)

	press := []byte{0x23, 0x08, 0xFE, 0xFF, 0xFF, 0xFF, 0x0F}
	h(press)
	h(press)

	if down, _ := rec.counts(); down != 1 {
		t.Fatalf("retransmit downs = %d, want 1", down)
	}
}

func TestButtonHandlerPanicRecovered(t *testing.T) {
	// Simulate a runtime failure inside the key path (e.g. a Windows BLE
	// callback hiccup): with recoverLog installed, the panic must not escape
	// the handler and later frames must still be processed.
	var mu sync.Mutex
	burst := 0
	downs := 0
	h := ButtonHandler("t", map[string]keys.Binding{"A": {VK: 0x41, Token: "a"}}, func(keys.Binding) {
		mu.Lock()
		burst++
		n := burst
		mu.Unlock()
		if n == 1 {
			panic("boom")
		}
		mu.Lock()
		downs++
		mu.Unlock()
	}, func(keys.Binding) {})

	press := []byte{0x23, 0x08, 0xEF, 0xFF, 0xFF, 0xFF, 0x0F} // A pressed
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic escaped the button handler: %v", r)
			}
		}()
		h(press) // first press panics inside down; the handler must absorb it
	}()
	h(press) // a later press must still land
	t.Cleanup(ReleaseAll)
	mu.Lock()
	defer mu.Unlock()
	if downs != 1 {
		t.Fatalf("after panic downs = %d, want 1 subsequent down to still land", downs)
	}
}

func TestButtonHandlerUnmappedButtonNoDown(t *testing.T) {
	rec := &keyRecorder{}
	h := ButtonHandler("t", nil, rec.downFn, rec.upFn)
	h([]byte{0x23, 0x08, 0xFE, 0xFF, 0xFF, 0xFF, 0x0F})
	if down, up := rec.counts(); down != 0 || up != 0 {
		t.Fatalf("down = %d, up = %d, want 0/0 with empty key map", down, up)
	}
}

// OnTap fires on every pressed transition of a mapped button. The mirrored
// frame from the pair's other unit fires it again — observational only (the
// TUI highlight is idempotent), the key itself is pressed once.
func TestOnTapFiresPerPressedTransition(t *testing.T) {
	rec := &keyRecorder{}
	var got []string
	OnTap = func(b string) { got = append(got, b) }
	t.Cleanup(func() { OnTap = nil })

	rDown, rUp := holdKeys(rec.downFn, rec.upFn)
	lDown, lUp := holdKeys(rec.downFn, rec.upFn)
	right := ButtonHandler("right", map[string]keys.Binding{"B": {VK: 0x42, Token: "b"}}, rDown, rUp)
	left := ButtonHandler("left", map[string]keys.Binding{"B": {VK: 0x42, Token: "b"}}, lDown, lUp)

	press := []byte{0x23, 0x08, 0xDF, 0xFF, 0xFF, 0xFF, 0x0F}
	right(press)
	left(press) // mirrored duplicate
	t.Cleanup(ReleaseAll)
	if len(got) != 2 || got[0] != "B" || got[1] != "B" {
		t.Fatalf("OnTap calls = %v, want [B B]", got)
	}
	if down, _ := rec.counts(); down != 1 {
		t.Fatalf("mirrored press downs = %d, want 1", down)
	}
}

func TestOnTapNotCalledUnmapped(t *testing.T) {
	called := false
	OnTap = func(string) { called = true }
	t.Cleanup(func() { OnTap = nil })

	h := ButtonHandler("t", map[string]keys.Binding{}, func(keys.Binding) {}, func(keys.Binding) {})
	h([]byte{0x23, 0x08, 0xDF, 0xFF, 0xFF, 0xFF, 0x0F})
	if called {
		t.Fatal("OnTap must not fire for an unmapped button")
	}
}
