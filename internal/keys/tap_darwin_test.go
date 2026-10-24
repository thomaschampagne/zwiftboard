//go:build darwin

package keys

import (
	"strings"
	"testing"
	"time"
)

func TestDarwinFocusTargetBlocksDown(t *testing.T) {
	SetWindowTarget("Zwift")
	defer SetWindowTarget("")
	if Down(Binding{VK: 'A'}) {
		t.Fatal("Darwin must reject injection with unsupported focus target")
	}
}

type scheduled struct {
	delay time.Duration
	fire  func()
}
type harness struct {
	engine        *quartzKeyboard
	events        []quartzEvent
	timers        []scheduled
	trusted, fail bool
	target        string
}

func newHarness() *harness {
	h := &harness{trusted: true}
	h.engine = newQuartzKeyboard(func(e quartzEvent) bool {
		if h.fail {
			return false
		}
		h.events = append(h.events, e)
		return true
	}, func() bool { return h.trusted }, func() string { return h.target }, func(d time.Duration, f func()) func() { h.timers = append(h.timers, scheduled{d, f}); return func() {} }, func() uint64 { return 0 })
	return h
}
func TestQuartzMapping(t *testing.T) {
	for _, token := range strings.Fields("a b c d e f g h i j k l m n o p q r s t u v w x y z 0 1 2 3 4 5 6 7 8 9 up down left right enter space tab esc backspace delete home end pageup pagedown shift ctrl alt capslock - = , . / ; ' [ ] ` \\ f1 f2 f3 f4 f5 f6 f7 f8 f9 f10 f11 f12") {
		b, err := Resolve(token)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := darwinKeycode(b.VK); !ok {
			t.Errorf("unmapped %s", token)
		}
	}
	for vk, want := range map[uint16]uint16{'A': 0, 'Z': 6, 0x11: 59, 0x12: 58, 0x2e: 117, 0x70: 122, 0x7b: 111} {
		got, ok := darwinKeycode(vk)
		if !ok || got != want {
			t.Errorf("%x = %d/%v", vk, got, ok)
		}
	}
	for _, vk := range []uint16{0x2d, 0xffff} {
		if _, ok := darwinKeycode(vk); ok {
			t.Fatalf("unsupported %x mapped", vk)
		}
	}
}
func TestQuartzRepeatReleaseAndStaleTimer(t *testing.T) {
	h := newHarness()
	if !h.engine.down('A') {
		t.Fatal("down failed")
	}
	h.engine.down('A')
	if len(h.events) != 1 || h.events[0].code != 0 || !h.events[0].down {
		t.Fatal(h.events)
	}
	if len(h.timers) != 1 || h.timers[0].delay != 250*time.Millisecond {
		t.Fatal(h.timers)
	}
	old := h.timers[0].fire
	old()
	if len(h.events) != 2 || !h.events[1].repeat || h.timers[1].delay != 33*time.Millisecond {
		t.Fatal(h.events, h.timers)
	}
	h.engine.up('A')
	h.engine.down('A')
	n := len(h.events)
	old()
	h.timers[1].fire()
	if len(h.events) != n {
		t.Fatal("stale repeat posted after release/repress")
	}
	h.engine.shutdown()
	n = len(h.events)
	h.timers[len(h.timers)-1].fire()
	if h.engine.down('B') || len(h.events) != n {
		t.Fatal("events after shutdown")
	}
	if h.events[n-1].down {
		t.Fatal("shutdown did not release")
	}
}
func TestQuartzModifiersCapsAndFailure(t *testing.T) {
	h := newHarness()
	h.engine.down(0x10)
	h.engine.down(0x11)
	h.engine.down(0x12)
	h.engine.down('A')
	if h.events[3].flags != flagShift|flagControl|flagAlternate {
		t.Fatal(h.events)
	}
	if len(h.timers) != 1 {
		t.Fatal("modifiers repeat")
	}
	h.engine.up(0x10)
	if h.events[4].flags != flagControl|flagAlternate {
		t.Fatal("released modifier still flagged")
	}
	h.engine.down(0x14)
	h.engine.up(0x14)
	if h.events[len(h.events)-1].flags&flagCaps == 0 {
		t.Fatal("caps not latched")
	}
	h.engine.down(0x14)
	h.engine.up(0x14)
	if h.events[len(h.events)-1].flags&flagCaps != 0 {
		t.Fatal("caps not toggled off")
	}
	if len(h.timers) != 1 {
		t.Fatal("caps repeats")
	}
	for _, mode := range []string{"denied", "focus", "post", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness()
			vk := uint16('A')
			switch mode {
			case "denied":
				h.trusted = false
			case "focus":
				h.target = "Zwift"
			case "post":
				h.fail = true
			case "unsupported":
				vk = 0x2d
			}
			if h.engine.down(vk) {
				t.Fatal("accepted")
			}
			h.fail = false
			h.engine.up(vk)
			if len(h.events) != 0 || len(h.timers) != 0 {
				t.Fatal("failed down recorded")
			}
		})
	}
}
func TestQuartzRepeatGatedAndReleaseUngated(t *testing.T) {
	h := newHarness()
	h.engine.down('A')
	h.target = "Zwift"
	h.timers[0].fire()
	if len(h.events) != 1 {
		t.Fatal("repeat bypassed focus gate")
	}
	h.engine.up('A')
	if len(h.events) != 2 || h.events[1].down {
		t.Fatal("focus blocked release")
	}
}

func TestQuartzConcurrentReleaseWaitsForRepeat(t *testing.T) {
	entered, proceed := make(chan struct{}), make(chan struct{})
	var events []quartzEvent
	var callback func()
	k := newQuartzKeyboard(func(e quartzEvent) bool {
		if e.repeat {
			close(entered)
			<-proceed
		}
		events = append(events, e)
		return true
	}, func() bool { return true }, func() string { return "" }, func(_ time.Duration, f func()) func() { callback = f; return func() {} }, func() uint64 { return 0 })
	k.down('A')
	repeatDone := make(chan struct{})
	go func() { callback(); close(repeatDone) }()
	<-entered
	releaseDone := make(chan struct{})
	go func() { k.up('A'); close(releaseDone) }()
	close(proceed)
	<-releaseDone
	<-repeatDone
	if len(events) != 3 || events[2].down {
		t.Fatalf("release must be the last event: %+v", events)
	}
	callback()
	if len(events) != 3 {
		t.Fatal("queued repeat posted after Up returned")
	}
}

func TestQuartzPermissionRevokedStopsRepeats(t *testing.T) {
	h := newHarness()
	h.engine.down('A')
	h.trusted = false
	h.timers[0].fire()
	if len(h.events) != 1 {
		t.Fatal("repeat bypassed trust gate")
	}
	h.engine.shutdown()
	if len(h.events) != 2 || h.events[1].down {
		t.Fatal("release was not attempted")
	}
}

func TestQuartzFailedModifierDoesNotAffectFollowingKey(t *testing.T) {
	h := newHarness()
	h.fail = true
	h.engine.down(0x10)
	h.engine.down(0x14)
	h.fail = false
	h.engine.down('A')
	if len(h.events) != 1 || h.events[0].flags != 0 {
		t.Fatal("failed modifier/caps down altered state")
	}
}

func TestQuartzInputWarning(t *testing.T) {
	h := newHarness()
	if h.engine.inputWarning() != "" {
		t.Fatal("unexpected warning")
	}
	h.trusted = false
	if !strings.Contains(h.engine.inputWarning(), "Accessibility") {
		t.Fatal("missing permission diagnostic")
	}
	h.trusted = true
	h.target = "Zwift"
	if !strings.Contains(h.engine.inputWarning(), "focus") {
		t.Fatal("missing focus diagnostic")
	}
}

func TestQuartzPreservesPhysicalFlagsThroughoutHold(t *testing.T) {
	h := newHarness()
	physical := uint64(flagCaps | flagShift | flagControl | flagAlternate | 1<<20)
	h.engine.physicalFlags = func() uint64 { return physical }
	h.engine.down('A')
	if h.events[0].flags != physical {
		t.Fatalf("physical flags lost: %#x", h.events[0].flags)
	}
	physical = 1 << 20
	h.timers[0].fire()
	if h.events[1].flags != physical {
		t.Fatalf("repeat kept stale hardware modifiers: %#x", h.events[1].flags)
	}
	h.engine.down(0x10)
	physical |= flagShift
	h.engine.up(0x10)
	if h.events[len(h.events)-1].flags != physical {
		t.Fatal("synthetic release cleared physical shift")
	}
	physical = flagControl
	h.engine.up('A')
	if h.events[len(h.events)-1].flags != physical {
		t.Fatal("release lost current physical modifiers")
	}
}

func TestQuartzCapsTogglesRelativeToPhysicalState(t *testing.T) {
	h := newHarness()
	physical := uint64(flagCaps)
	h.engine.physicalFlags = func() uint64 { return physical }
	h.engine.down(0x14)
	h.engine.up(0x14)
	if h.events[0].flags&flagCaps != 0 || h.events[1].flags&flagCaps != 0 {
		t.Fatal("preexisting caps was not toggled off")
	}
	h.engine.down('A')
	physical = 0
	h.timers[0].fire()
	if h.events[len(h.events)-1].flags&flagCaps == 0 {
		t.Fatal("physical caps toggle during hold was ignored")
	}
	h.engine.down(0x14)
	h.engine.up(0x14)
	if h.events[len(h.events)-1].flags&flagCaps != 0 {
		t.Fatal("second synthetic caps toggle failed")
	}
}

func TestDarwinResolveRejectsInsert(t *testing.T) {
	if _, err := Resolve("insert"); err == nil || !strings.Contains(err.Error(), "unsupported on macOS") {
		t.Fatalf("Resolve(insert) error = %v", err)
	}
}
