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

func (r *keyRecorder) downFn(keys.Binding) bool {
	r.mu.Lock()
	r.down++
	r.mu.Unlock()
	return true
}

func (r *keyRecorder) upFn(keys.Binding) bool {
	r.mu.Lock()
	r.up++
	r.mu.Unlock()
	return true
}

func (r *keyRecorder) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.down, r.up
}

// tapDown/tapUp are void versions for ButtonHandler calls that bypass holdKeys
// (ButtonHandler's down/up seam is void; holdKeys needs the bool form).
func (r *keyRecorder) tapDown(b keys.Binding) { _ = r.downFn(b) }
func (r *keyRecorder) tapUp(b keys.Binding)   { _ = r.upFn(b) }

// One press/release cycle = one down + one up, and a bounce (quick
// release/re-press) must not swallow the second down.
func TestButtonHandlerDownUp(t *testing.T) {
	rec := &keyRecorder{}
	h := ButtonHandler("t", NewKeyMap(map[string]keys.Binding{"LEFT": {VK: 0x25, Token: "left"}}), rec.tapDown, rec.tapUp)

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
// same frame from BOTH controllers (two handlers). The shared held count is
// idempotent per VK, so the mirrored frame must not press the key again — and
// the mirrored release must lift it exactly once.
func TestCrossControllerMirrorSingleDown(t *testing.T) {
	rec := &keyRecorder{}
	rHK := holdKeys(rec.downFn, rec.upFn)
	lHK := holdKeys(rec.downFn, rec.upFn)
	right := ButtonHandler("right", NewKeyMap(map[string]keys.Binding{"B": {VK: 0x42, Token: "b"}}), rHK.Press, rHK.Release)
	left := ButtonHandler("left", NewKeyMap(map[string]keys.Binding{"B": {VK: 0x42, Token: "b"}}), lHK.Press, lHK.Release)
	t.Cleanup(rHK.ReleaseAll)
	t.Cleanup(lHK.ReleaseAll)

	press := []byte{0x23, 0x08, 0xDF, 0xFF, 0xFF, 0xFF, 0x0F}
	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}
	right(press)
	left(press) // mirrored frame from the other unit
	if down, _ := rec.counts(); down != 1 {
		t.Fatalf("mirrored press downs = %d, want 1", down)
	}
	right(idle)
	left(idle) // mirrored release
	if down, up := rec.counts(); down != 1 || up != 1 {
		t.Fatalf("mirrored cycle down=%d up=%d, want 1/1", down, up)
	}
}

// A key now in ReleaseAll-cross-session shape: one pod's session dropping
// mid-hold must NOT lift the key while the sibling session still sees it
// pressed (the physical button is held). Only the final release lifts it.
func TestDroppedSessionKeepsSiblingHold(t *testing.T) {
	rec := &keyRecorder{}
	rHK := holdKeys(rec.downFn, rec.upFn)
	lHK := holdKeys(rec.downFn, rec.upFn)
	right := ButtonHandler("right", NewKeyMap(map[string]keys.Binding{"A": {VK: 0x41, Token: "a"}}), rHK.Press, rHK.Release)
	left := ButtonHandler("left", NewKeyMap(map[string]keys.Binding{"A": {VK: 0x41, Token: "a"}}), lHK.Press, lHK.Release)
	t.Cleanup(rHK.ReleaseAll)
	t.Cleanup(lHK.ReleaseAll)

	press := []byte{0x23, 0x08, 0xEF, 0xFF, 0xFF, 0xFF, 0x0F}
	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}
	right(press)
	left(press)      // mirrored press, both sessions hold it
	rHK.ReleaseAll() // right session drops mid-hold
	if down, up := rec.counts(); down != 1 || up != 0 {
		t.Fatalf("after drop: down=%d up=%d, want 1/0 (key must stay down)", down, up)
	}
	left(idle) // the user releases: the surviving session lifts the key
	if down, up := rec.counts(); down != 1 || up != 1 {
		t.Fatalf("after release: down=%d up=%d, want 1/1", down, up)
	}
}

// A lone pod session has no sibling: dropping it mid-hold must force-lift the
// key (via keys.ReleaseVK) and clear the held state so a later press works.
func TestDroppedSoleSessionReleases(t *testing.T) {
	rec := &keyRecorder{}
	hk := holdKeys(rec.downFn, rec.upFn)
	h := ButtonHandler("t", NewKeyMap(map[string]keys.Binding{"A": {VK: 0x41, Token: "a"}}), hk.Press, hk.Release)
	t.Cleanup(hk.ReleaseAll)

	press := []byte{0x23, 0x08, 0xEF, 0xFF, 0xFF, 0xFF, 0x0F}
	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}
	h(press)
	hk.ReleaseAll() // session dies mid-hold
	if down, up := rec.counts(); down != 1 || up != 0 {
		t.Fatalf("after press+drop: down=%d up=%d, want 1/0 (ReleaseVK is the lifter, not Up)", down, up)
	}
	h(idle)
	h(press) // a fresh press after the drop must still land
	if down, _ := rec.counts(); down != 2 {
		t.Fatalf("press after drop downs = %d, want 2 (held state cleared)", down)
	}
}

// A press dropped because the focus window is missing must not be recorded as
// held: it is unwindible, so a later successful press (mirrored re-arrival or
// a re-press once the window exists) can still land, and nothing is released
// spuriously.
func TestDroppedPressNotRecordedHeld(t *testing.T) {
	rec := &keyRecorder{}
	fail := true
	hk := holdKeys(func(keys.Binding) bool { return !fail }, rec.upFn)
	h := ButtonHandler("t", NewKeyMap(map[string]keys.Binding{"A": {VK: 0x41, Token: "a"}}), hk.Press, hk.Release)
	t.Cleanup(hk.ReleaseAll)

	press := []byte{0x23, 0x08, 0xEF, 0xFF, 0xFF, 0xFF, 0x0F}
	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}
	h(press) // focus window missing: down dropped
	fail = false
	h(press) // same transition retransmitted — not new; nothing to re-arm yet
	if down, up := rec.counts(); down != 0 || up != 0 {
		t.Fatalf("dropped press: down=%d up=%d, want 0/0", down, up)
	}
	h(idle) // release after a dropped press: nothing to lift
	fail = true
	h(press) // a real re-press while focus is still missing: dropped again, no record
	if down, up := rec.counts(); down != 0 || up != 0 {
		t.Fatalf("re-press cycle: down=%d up=%d, want 0/0", down, up)
	}
}

// Retransmission of the same frame (same bitmap) is not a new transition.
func TestIdenticalRetransmitNoDoubleDown(t *testing.T) {
	rec := &keyRecorder{}
	h := ButtonHandler("t", NewKeyMap(map[string]keys.Binding{"LEFT": {VK: 0x25, Token: "left"}}), rec.tapDown, rec.tapUp)

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
	h := ButtonHandler("t", NewKeyMap(map[string]keys.Binding{"A": {VK: 0x41, Token: "a"}}), func(keys.Binding) {
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
	mu.Lock()
	defer mu.Unlock()
	if downs != 1 {
		t.Fatalf("after panic downs = %d, want 1 subsequent down to still land", downs)
	}
}

func TestButtonHandlerUnmappedButtonNoDown(t *testing.T) {
	rec := &keyRecorder{}
	h := ButtonHandler("t", NewKeyMap(nil), rec.tapDown, rec.tapUp)
	h([]byte{0x23, 0x08, 0xFE, 0xFF, 0xFF, 0xFF, 0x0F})
	if down, up := rec.counts(); down != 0 || up != 0 {
		t.Fatalf("down = %d, up = %d, want 0/0 with empty key map", down, up)
	}
}

// OnKey reports BOTH transitions of a mapped button. The mirrored frame from
// the pair's other unit fires it again — observational only (the UI dedups),
// the key itself is pressed once.
func TestOnKeyReportsBothTransitions(t *testing.T) {
	rec := &keyRecorder{}
	type ev struct {
		btn  string
		down bool
	}
	var got []ev
	OnKey = func(b string, down bool) { got = append(got, ev{b, down}) }
	t.Cleanup(func() { OnKey = nil })

	rHK := holdKeys(rec.downFn, rec.upFn)
	lHK := holdKeys(rec.downFn, rec.upFn)
	right := ButtonHandler("right", NewKeyMap(map[string]keys.Binding{"B": {VK: 0x42, Token: "b"}}), rHK.Press, rHK.Release)
	left := ButtonHandler("left", NewKeyMap(map[string]keys.Binding{"B": {VK: 0x42, Token: "b"}}), lHK.Press, lHK.Release)
	t.Cleanup(rHK.ReleaseAll)
	t.Cleanup(lHK.ReleaseAll)

	press := []byte{0x23, 0x08, 0xDF, 0xFF, 0xFF, 0xFF, 0x0F}
	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}
	right(press)
	left(press) // mirrored duplicate
	if len(got) != 2 || got[0] != (ev{"B", true}) || got[1] != (ev{"B", true}) {
		t.Fatalf("OnKey after press = %v, want two downs", got)
	}
	if down, _ := rec.counts(); down != 1 {
		t.Fatalf("mirrored press downs = %d, want 1", down)
	}
	right(idle)
	left(idle) // mirrored release
	if len(got) != 4 || got[2] != (ev{"B", false}) || got[3] != (ev{"B", false}) {
		t.Fatalf("OnKey after release = %v, want two ups", got)
	}
	if _, up := rec.counts(); up != 1 {
		t.Fatalf("mirrored release ups = %d, want 1", up)
	}
}

func TestOnKeyNotCalledUnmapped(t *testing.T) {
	called := false
	OnKey = func(string, bool) { called = true }
	t.Cleanup(func() { OnKey = nil })

	h := ButtonHandler("t", NewKeyMap(map[string]keys.Binding{}), func(keys.Binding) {}, func(keys.Binding) {})
	h([]byte{0x23, 0x08, 0xDF, 0xFF, 0xFF, 0xFF, 0x0F})
	if called {
		t.Fatal("OnKey must not fire for an unmapped button")
	}
}

func TestKeyMapSwap(t *testing.T) {
	km := NewKeyMap(map[string]keys.Binding{"A": {VK: 0x41, Token: "a"}})
	if b, _ := km.Get("A"); b.VK != 0x41 {
		t.Fatal("initial")
	}
	km.Swap(map[string]keys.Binding{"A": {VK: 0x42, Token: "b"}})
	if b, _ := km.Get("A"); b.VK != 0x42 {
		t.Fatal("after swap")
	}
}

func TestReleaseAllHeldLifts(t *testing.T) {
	var lifted []uint16
	OnLifted = func(vk uint16) { lifted = append(lifted, vk) }
	defer func() { OnLifted = nil }()
	hk := holdKeys(func(keys.Binding) bool { return true }, func(keys.Binding) bool { return true })
	hk.Press(keys.Binding{VK: 0x41})
	ReleaseAllHeld()
	if len(lifted) != 1 || lifted[0] != 0x41 {
		t.Fatalf("lifted=%v", lifted)
	}
}

// Two buttons of the SAME handler mapped to the SAME key (a profile that
// binds e.g. LEFT and UP to one key) must hold the key until the LAST of
// them is released, and lift it exactly once. The held state is a per-VK
// COUNT, not a flag: with a flag the first release wiped it and the second
// release found nothing to lift, leaving the key stuck down for the whole
// process.
func TestSameVKTwoButtonsHeldTogether(t *testing.T) {
	rec := &keyRecorder{}
	hk := holdKeys(rec.downFn, rec.upFn)
	km := NewKeyMap(map[string]keys.Binding{
		"LEFT": {VK: 0x25, Token: "left"},
		"UP":   {VK: 0x25, Token: "left"},
	})
	h := ButtonHandler("t", km, hk.Press, hk.Release)
	t.Cleanup(hk.ReleaseAll)

	both := []byte{0x23, 0x08, 0xFC, 0xFF, 0xFF, 0xFF, 0x0F}   // LEFT + UP pressed
	upOnly := []byte{0x23, 0x08, 0xFD, 0xFF, 0xFF, 0xFF, 0x0F} // LEFT released, UP held
	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}   // all released

	h(both)
	if down, up := rec.counts(); down != 1 || up != 0 {
		t.Fatalf("both buttons down: down=%d up=%d, want 1/0", down, up)
	}
	h(upOnly)
	if down, up := rec.counts(); down != 1 || up != 0 {
		t.Fatalf("one of them released: down=%d up=%d, want 1/0 (the key is still held)", down, up)
	}
	h(idle)
	if down, up := rec.counts(); down != 1 || up != 1 {
		t.Fatalf("both released: down=%d up=%d, want 1/1 (the key must not stay stuck)", down, up)
	}
}

// The realistic shape: the LEFT pod's stream carries the mirrored RIGHT-pod
// state AND the left pod's own button, so one handler sees two buttons on
// one VK. The key must go down once and come up once — never stay down.
func TestSameVKMirroredHeldTogether(t *testing.T) {
	rec := &keyRecorder{}
	rHK := holdKeys(rec.downFn, rec.upFn)
	lHK := holdKeys(rec.downFn, rec.upFn)
	// The right pod presses Z; the left pod mirrors Z and presses its own
	// LEFT. Both are bound to the same key (left arrow).
	rKM := NewKeyMap(map[string]keys.Binding{"Z": {VK: 0x25, Token: "left"}})
	lKM := NewKeyMap(map[string]keys.Binding{
		"Z":    {VK: 0x25, Token: "left"}, // mirrored from the right pod
		"LEFT": {VK: 0x25, Token: "left"}, // the left pod's own button
	})
	right := ButtonHandler("right", rKM, rHK.Press, rHK.Release)
	left := ButtonHandler("left", lKM, lHK.Press, lHK.Release)
	t.Cleanup(rHK.ReleaseAll)
	t.Cleanup(lHK.ReleaseAll)

	zPress := []byte{0x23, 0x08, 0xFF, 0xFE, 0xFF, 0xFF, 0x0F} // Z pressed
	zLeft := []byte{0x23, 0x08, 0xFE, 0xFE, 0xFF, 0xFF, 0x0F}  // Z + LEFT pressed
	lPress := []byte{0x23, 0x08, 0xFE, 0xFF, 0xFF, 0xFF, 0x0F} // LEFT pressed, Z released
	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}   // all released

	right(zPress)
	left(zPress) // the mirrored copy of the right pod's press
	left(zLeft)  // the left pod's own LEFT joins the mirrored Z
	left(lPress) // Z released (mirrored), LEFT still held
	right(idle)  // Z released on the right pod
	left(idle)   // the mirrored release

	if down, up := rec.counts(); down != 1 || up != 1 {
		t.Fatalf("mirrored + own button on one VK: down=%d up=%d, want 1/1", down, up)
	}
}

// A session dropped mid-hold never sees the release edge: ReleaseAll must
// report each force-lifted VK so the UI can clear the held row.
func TestReleaseAllLiftNotifiesOnLifted(t *testing.T) {
	rec := &keyRecorder{}
	var lifted []uint16
	OnLifted = func(vk uint16) { lifted = append(lifted, vk) }
	t.Cleanup(func() { OnLifted = nil })

	hk := holdKeys(rec.downFn, rec.upFn)
	h := ButtonHandler("t", NewKeyMap(map[string]keys.Binding{"A": {VK: 0x41, Token: "a"}}), hk.Press, hk.Release)
	h([]byte{0x23, 0x08, 0xEF, 0xFF, 0xFF, 0xFF, 0x0F})
	hk.ReleaseAll()
	if len(lifted) != 1 || lifted[0] != 0x41 {
		t.Fatalf("OnLifted calls = %v, want [0x41]", lifted)
	}
}
