package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"zwiftboard/internal/config"
)

func testCfg() Config {
	return Config{
		Profile:      "test",
		Mode:         config.ModeSingle,
		LeftButtons:  []string{"LEFT", "UP", "RIGHT", "DOWN", "MIN"},
		RightButtons: []string{"Y", "Z", "A", "B", "PLUS"},
		Bindings:     map[string]string{"A": "a", "B": "b", "PLUS": "+"},
	}
}

func TestUpdateBTMsg(t *testing.T) {
	m := New(testCfg())
	next, _ := m.Update(BTMsg(true))
	if !next.(Model).bt {
		t.Fatal("BTMsg(true) should set bt")
	}
}

func TestUpdateMsgSetsLatest(t *testing.T) {
	m := New(testCfg())
	nm, _ := m.Update(UpdateMsg{Latest: "1.0.2"})
	mm := nm.(Model)
	if mm.latest != "1.0.2" {
		t.Errorf("latest = %q, want %q", mm.latest, "1.0.2")
	}
}

// clock returns a model with a fake clock and presses/releases helpers.
func keyModel() (Model, *time.Time) {
	now := time.Now()
	m := New(testCfg())
	m.clock = func() time.Time { return now }
	return m, &now
}

func TestKeyDownShowsPressedThenHold(t *testing.T) {
	m, now := keyModel()
	next, _ := m.Update(KeyStateMsg{Button: "A", Down: true})
	m = next.(Model)
	if p := m.keyPhase("A"); p != PhasePressed {
		t.Fatalf("phase right after down = %v, want pressed", p)
	}
	*now = now.Add(300 * time.Millisecond) // past holdAfter
	if p := m.keyPhase("A"); p != PhaseHold {
		t.Fatalf("phase after 300ms = %v, want hold", p)
	}
}

func TestKeyUpShowsReleasedThenIdle(t *testing.T) {
	m, now := keyModel()
	next, _ := m.Update(KeyStateMsg{Button: "A", Down: true})
	next, _ = next.Update(KeyStateMsg{Button: "A", Down: false})
	m = next.(Model)
	if p := m.keyPhase("A"); p != PhaseReleased {
		t.Fatalf("phase after up = %v, want released", p)
	}
	*now = now.Add(releaseFor)
	next, _ = m.Update(keyExpireMsg{button: "A"})
	if p := next.(Model).keyPhase("A"); p != PhaseIdle {
		t.Fatalf("phase after expiry = %v, want idle", p)
	}
	if _, ok := next.(Model).keys["A"]; ok {
		t.Fatal("expired key must leave the map")
	}
}

func TestKeyMirrorDedup(t *testing.T) {
	m, now := keyModel()
	next, _ := m.Update(KeyStateMsg{Button: "A", Down: true})
	first := next.(Model).keys["A"].at
	// Advance the clock: with the same frozen instant a missing dedup would
	// still restamp to an equal time and the assertion could not fail.
	*now = now.Add(100 * time.Millisecond)
	next, _ = next.Update(KeyStateMsg{Button: "A", Down: true}) // mirrored pod
	if got := next.(Model).keys["A"].at; !got.Equal(first) {
		t.Fatal("duplicate down must not restamp the press")
	}
	next, _ = next.Update(KeyStateMsg{Button: "A", Down: false})
	up := next.(Model).keys["A"].at
	*now = now.Add(100 * time.Millisecond)
	next, _ = next.Update(KeyStateMsg{Button: "A", Down: false}) // mirrored release
	if got := next.(Model).keys["A"].at; !got.Equal(up) {
		t.Fatal("duplicate up must not restamp the release")
	}
	if p := next.(Model).keyPhase("A"); p != PhaseReleased {
		t.Fatalf("phase after duplicate up = %v, want released", p)
	}
}

func TestKeyStateIgnoresUnmapped(t *testing.T) {
	m, _ := keyModel()
	next, cmd := m.Update(KeyStateMsg{Button: "X", Down: true})
	if cmd != nil || len(next.(Model).keys) != 0 {
		t.Fatal("unmapped button must not enter the key state")
	}
}

// A press must never hand the loop a tick-producing cmd: tickMsg re-arms the
// timer chain, so one chain per press would grow without bound (the chain is
// Init-driven only).
func TestKeyDownDoesNotStartTickChain(t *testing.T) {
	m := New(testCfg())
	var cmds []tea.Cmd
	for _, btn := range []string{"A", "B", "PLUS"} { // every mapped button
		next, cmd := m.Update(KeyStateMsg{Button: btn, Down: true})
		m = next.(Model)
		if cmd == nil {
			t.Fatalf("%s down returned no cmd", btn)
		}
		cmds = append(cmds, cmd)
	}
	for i, cmd := range cmds {
		if _, isTick := cmd().(tickMsg); isTick {
			t.Fatalf("press %d returned tickMsg: the press would start a permanent tick chain", i)
		}
	}
}

// An up for a key that was never down (stale or foreign frame) must change
// nothing and schedule nothing.
func TestKeyUpWithoutPriorDownIsNoOp(t *testing.T) {
	next, cmd := New(testCfg()).Update(KeyStateMsg{Button: "A", Down: false})
	if cmd != nil || len(next.(Model).keys) != 0 {
		t.Fatal("up for a key never seen down must be a no-op")
	}
}

func TestStaleExpiryDoesNotClearHeldKey(t *testing.T) {
	m, _ := keyModel()
	next, _ := m.Update(KeyStateMsg{Button: "A", Down: true})
	next, _ = next.Update(KeyStateMsg{Button: "A", Down: false})
	next, _ = next.Update(KeyStateMsg{Button: "A", Down: true}) // re-pressed
	next, _ = next.Update(keyExpireMsg{button: "A"})            // stale fade timer
	if p := next.(Model).keyPhase("A"); p == PhaseIdle {
		t.Fatal("stale expiry must not clear a re-pressed key")
	}
}

func TestDemoPressRunsFullCycle(t *testing.T) {
	n, cmd := demoModel().Update(key("8")) // demoButtons[7] = "A"
	if cmd == nil {
		t.Fatal("digit should emit a cmd")
	}
	if n.(Model).keyPhase("A") != PhasePressed {
		t.Fatal("3 should press A")
	}
	// Batch: the one-shot repaint (pressed → hold flips on time) + the
	// scheduled release. The repaint must not be a tickMsg: that would be a
	// recurring chain handed to every demo press.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("demo press cmds = %v, want a 2-command batch", cmd())
	}
	repaint := batch[0]() // blocks until holdAfter, like the program does
	if _, isRepaint := repaint.(repaintMsg); !isRepaint {
		t.Fatalf("first batch cmd = %T, want repaintMsg", repaint)
	}
	n, _ = n.Update(repaint)
	if n.(Model).keyPhase("A") != PhaseHold {
		t.Fatal("repaint should land on the pressed → hold flip")
	}
	up := batch[1]() // fires demoHoldFor later
	n, _ = n.Update(up)
	if n.(Model).keyPhase("A") != PhaseReleased {
		t.Fatal("scheduled up should release A")
	}
}

func TestQuitKey(t *testing.T) {
	_, cmd := New(testCfg()).Update(key("q"))
	if cmd == nil {
		t.Fatal("q should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q cmd should produce QuitMsg")
	}
}

func key(s string) tea.KeyPressMsg {
	r, _ := utf8.DecodeRuneInString(s)
	return tea.KeyPressMsg{Code: r, Text: s}
}

func demoModel() Model {
	cfg := testCfg()
	cfg.Demo = true
	return New(cfg)
}

func TestDemoToggleBT(t *testing.T) {
	n, _ := demoModel().Update(key("b"))
	if !n.(Model).bt {
		t.Fatal("b should turn bt on")
	}
	n, _ = n.Update(key("b"))
	if n.(Model).bt {
		t.Fatal("b again should turn bt off")
	}
}

func TestDemoCyclePods(t *testing.T) {
	var n tea.Model = demoModel()
	want := []PodState{PodDetected, PodConnected, PodOff}
	for i, w := range want {
		n, _ = n.Update(key("]"))
		if got := n.(Model).right; got != w {
			t.Fatalf("step %d: right=%v want %v", i, got, w)
		}
	}
	n, _ = n.Update(key("["))
	if n.(Model).left != PodDetected {
		t.Fatal("[ should advance left")
	}
}

func TestLiveIgnoresDemoKeys(t *testing.T) {
	n, cmd := New(testCfg()).Update(key("b"))
	if n.(Model).bt || cmd != nil {
		t.Fatal("demo keys must be inert in live mode")
	}
}

func TestLogToggleWorksLiveAndDemo(t *testing.T) {
	for _, m := range []Model{New(testCfg()), demoModel()} {
		n, _ := m.Update(key("l"))
		if !n.(Model).showLogs {
			t.Fatal("l should show logs")
		}
		n, _ = n.Update(key("l"))
		if n.(Model).showLogs {
			t.Fatal("l again should hide logs")
		}
	}
}

func TestDemoDoesNotCycleLeftOnL(t *testing.T) {
	n, _ := demoModel().Update(key("l"))
	if n.(Model).left != PodOff {
		t.Fatal("l is the log toggle now; left pod must not change")
	}
}

func TestProfileSwitchKeys(t *testing.T) {
	var called []int
	cfg := testCfg()
	cfg.Profiles = []string{"a", "b"}
	cfg.SwitchProfile = func(d int) { called = append(called, d) }
	m := New(cfg)
	run := func(msg tea.KeyPressMsg) {
		_, cmd := m.Update(msg)
		if cmd != nil {
			_ = cmd()
		}
	}
	run(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
	run(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModCtrl})
	run(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	run(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	run(tea.KeyPressMsg{Code: tea.KeyRight}) // plain arrow must NOT switch
	if !reflect.DeepEqual(called, []int{1, -1, 1, -1}) {
		t.Fatalf("called=%v", called)
	}
}

func TestReloadKeyRunsReloader(t *testing.T) {
	calls := 0
	cfg := testCfg()
	cfg.Reload = func() { calls++ }
	_, cmd := New(cfg).Update(key("r"))
	if cmd == nil {
		t.Fatal("r should return a cmd that reloads")
	}
	_ = cmd()
	if calls != 1 {
		t.Fatalf("reload called %d times, want 1", calls)
	}
}

func TestReloadKeyUnavailableIsInert(t *testing.T) {
	_, cmd := New(testCfg()).Update(key("r"))
	if cmd != nil {
		t.Fatal("r without a reload callback must do nothing")
	}
}

func TestStartProgramKeyRunsLauncher(t *testing.T) {
	calls := 0
	cfg := testCfg()
	cfg.StartProgram = "MyWhooshHD.exe"
	cfg.LaunchProgram = func() error { calls++; return nil }
	m := New(cfg)
	n, cmd := m.Update(key("s"))
	if cmd == nil {
		t.Fatal("s should return a cmd that starts the program")
	}
	n, _ = n.Update(cmd())
	if calls != 1 {
		t.Fatalf("launcher called %d times, want 1", calls)
	}
	if got := n.(Model).status; !strings.Contains(got, "MyWhooshHD.exe") {
		t.Fatalf("status = %q, want it to name the program", got)
	}
}

func TestStartProgramKeyErrorShown(t *testing.T) {
	cfg := testCfg()
	cfg.StartProgram = "MyWhooshHD.exe"
	cfg.LaunchProgram = func() error { return errors.New("not found") }
	m := New(cfg)
	_, cmd := m.Update(key("s"))
	n, _ := m.Update(cmd())
	if got := n.(Model).status; !strings.Contains(got, "not found") {
		t.Fatalf("status = %q, want the error", got)
	}
}

func TestStartProgramKeyHiddenWithoutProgram(t *testing.T) {
	cfg := testCfg()
	cfg.LaunchProgram = func() error { return nil } // wired, but no startProgram
	_, cmd := New(cfg).Update(key("s"))
	if cmd != nil {
		t.Fatal("s must be inert when the profile has no startProgram")
	}
}

func TestProfileReloadMsgShowsStatus(t *testing.T) {
	n, cmd := New(testCfg()).Update(ProfileMsg{Name: "mywhoosh", Reload: true})
	if cmd == nil {
		t.Fatal("a reload result should schedule the status expiry")
	}
	if got := n.(Model).status; !strings.Contains(got, "mywhoosh") {
		t.Fatalf("status = %q, want the reload notice", got)
	}
}

func TestProfileMsgSuccessUpdatesAllState(t *testing.T) {
	m := New(testCfg())
	// Precondition: a missing focus window and one key held down, so the
	// switch has something to clear.
	n, _ := m.Update(FocusMsg(false))
	n, _ = n.Update(KeyStateMsg{Button: "A", Down: true})
	if !n.(Model).focusMissing || len(n.(Model).keys) == 0 {
		t.Fatal("precondition: focus missing and a key down")
	}
	bindings := map[string]string{"Y": "space", "Z": "enter"}
	n, _ = n.Update(ProfileMsg{
		Name:         "systm",
		Bindings:     bindings,
		Focus:        "MyWhoosh",
		StartProgram: "MyWhooshHD.exe",
		NoMapping:    true,
		Index:        2,
	})
	got := n.(Model)
	if got.cfg.Profile != "systm" {
		t.Fatalf("Profile = %q", got.cfg.Profile)
	}
	if !reflect.DeepEqual(got.cfg.Bindings, bindings) {
		t.Fatalf("Bindings = %v", got.cfg.Bindings)
	}
	if got.cfg.Focus != "MyWhoosh" {
		t.Fatalf("Focus = %q", got.cfg.Focus)
	}
	if got.cfg.StartProgram != "MyWhooshHD.exe" {
		t.Fatalf("StartProgram = %q", got.cfg.StartProgram)
	}
	if !got.cfg.NoMapping {
		t.Fatal("NoMapping should propagate")
	}
	if got.cfg.ProfileIndex != 2 {
		t.Fatalf("ProfileIndex = %d", got.cfg.ProfileIndex)
	}
	if got.focusMissing {
		t.Fatal("switch must clear focusMissing")
	}
	if len(got.keys) != 0 {
		t.Fatalf("switch must clear key state, got %v", got.keys)
	}
}

func TestProfileMsgErrorShowsStatusOnly(t *testing.T) {
	m := New(testCfg())
	m.focusMissing = true
	n, cmd := m.Update(ProfileMsg{Name: "x", Err: "no such profile"})
	if cmd == nil {
		t.Fatal("error ProfileMsg should schedule the status expiry")
	}
	got := n.(Model)
	if !strings.Contains(got.status, "no such profile") {
		t.Fatalf("status = %q, want the error", got.status)
	}
	if got.cfg.Profile != "test" || !got.focusMissing {
		t.Fatal("error ProfileMsg must not change profile or focus state")
	}
}

func TestLogBufferKeepsLastLines(t *testing.T) {
	b := NewLogBuffer(3)
	b.Write([]byte("one\ntwo\nthr"))
	b.Write([]byte("ee\nfour\n"))
	got := b.Lines()
	want := []string{"two", "three", "four"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("Lines() = %q, want %q", got, want)
	}
}

func TestFocusMsgUpdatesModel(t *testing.T) {
	n, _ := New(testCfg()).Update(FocusMsg(false))
	if !n.(Model).focusMissing {
		t.Fatal("FocusMsg(false) should mark the window missing")
	}
	n, _ = n.Update(FocusMsg(true))
	if n.(Model).focusMissing {
		t.Fatal("FocusMsg(true) should clear it")
	}
}

func TestDemoToggleFocus(t *testing.T) {
	n, _ := demoModel().Update(key("f"))
	if !n.(Model).focusMissing {
		t.Fatal("f should simulate a missing focus window")
	}
	n, _ = New(testCfg()).Update(key("f"))
	if n.(Model).focusMissing {
		t.Fatal("f must be inert in live mode")
	}
}

func openCfg(open func() error) Config {
	cfg := testCfg()
	cfg.ConfigPath = "config.yaml"
	cfg.OpenConfig = open
	return cfg
}

func TestOpenConfigKeyRunsOpener(t *testing.T) {
	calls := 0
	m := New(openCfg(func() error { calls++; return nil }))
	n, cmd := m.Update(key("e"))
	if cmd == nil {
		t.Fatal("e should return a cmd that opens the editor")
	}
	n, _ = n.Update(cmd())
	if calls != 1 {
		t.Fatalf("opener called %d times, want 1", calls)
	}
	if got := n.(Model).status; !strings.Contains(got, "config.yaml") {
		t.Fatalf("status = %q, want it to name config.yaml", got)
	}
}

func TestOpenConfigErrorShown(t *testing.T) {
	m := New(openCfg(func() error { return errors.New("no editor") }))
	_, cmd := m.Update(key("e"))
	n, _ := m.Update(cmd())
	if got := n.(Model).status; !strings.Contains(got, "no editor") {
		t.Fatalf("status = %q, want the error", got)
	}
}

func TestOpenConfigUnavailableIsInert(t *testing.T) {
	_, cmd := New(testCfg()).Update(key("e"))
	if cmd != nil {
		t.Fatal("e without an opener must do nothing")
	}
}

func TestStatusExpires(t *testing.T) {
	m := New(openCfg(func() error { return nil }))
	_, cmd := m.Update(key("e"))
	n, _ := m.Update(cmd())
	m = n.(Model)
	n, _ = m.Update(statusExpireMsg{id: m.statusID})
	if n.(Model).status != "" {
		t.Fatal("status should clear on its expiry")
	}
	n, _ = m.Update(statusExpireMsg{id: m.statusID - 1}) // stale
	if n.(Model).status == "" {
		t.Fatal("a stale expiry must not clear a newer status")
	}
}

func scrollModel() Model {
	cfg := testCfg()
	cfg.Logs = NewLogBuffer(50)
	for i := 0; i < 20; i++ {
		cfg.Logs.Write([]byte(fmt.Sprintf("line%02d\n", i)))
	}
	m := New(cfg)
	m.showLogs = true
	return m
}

func TestScrollJK(t *testing.T) {
	m := scrollModel()
	n, _ := m.Update(key("k"))
	if n.(Model).logOffset != 1 {
		t.Fatalf("k should scroll up one line, got %d", n.(Model).logOffset)
	}
	n, _ = n.Update(key("k"))
	n, _ = n.Update(key("j"))
	if n.(Model).logOffset != 1 {
		t.Fatalf("j should scroll back down, got %d", n.(Model).logOffset)
	}
	n, _ = n.Update(key("j"))
	if n.(Model).logOffset != 0 {
		t.Fatalf("j must clamp at the bottom, got %d", n.(Model).logOffset)
	}
}

func TestScrollClampsAtTop(t *testing.T) {
	m := scrollModel()
	for i := 0; i < 30; i++ {
		n, _ := m.Update(key("k"))
		m = n.(Model)
	}
	if m.logOffset != 10 { // 20 lines - 10 visible
		t.Fatalf("logOffset = %d, want 10 (20 lines - 10 visible)", m.logOffset)
	}
}

func TestMouseWheelScrolls(t *testing.T) {
	m := scrollModel()
	wheel := func(b tea.MouseButton) tea.MouseWheelMsg { return tea.MouseWheelMsg{Button: b} }
	n, _ := m.Update(wheel(tea.MouseWheelUp))
	if n.(Model).logOffset != 1 {
		t.Fatalf("wheel up should scroll, got %d", n.(Model).logOffset)
	}
	n, _ = n.Update(wheel(tea.MouseWheelDown))
	if n.(Model).logOffset != 0 {
		t.Fatalf("wheel down should return to bottom, got %d", n.(Model).logOffset)
	}
	n, _ = n.Update(wheel(tea.MouseWheelDown))
	if n.(Model).logOffset != 0 {
		t.Fatalf("wheel down at the bottom must clamp, got %d", n.(Model).logOffset)
	}
}

func TestScrollInertWhenLogsHidden(t *testing.T) {
	m := New(testCfg())
	n, _ := m.Update(key("k"))
	if n.(Model).logOffset != 0 {
		t.Fatal("k must not scroll while the log panel is hidden")
	}
}

func TestLogViewShowsScrolledWindow(t *testing.T) {
	m := scrollModel()
	v := m.View()
	has(t, v, "line19") // following the bottom
	lacks(t, v, "line00")
	n, _ := m.Update(key("k"))
	m = n.(Model)
	v = m.View()
	has(t, v, "line18")
	lacks(t, v, "line19")
}

// "m" runs the wired toggle in a command, so the render loop never blocks.
func TestToggleModeKeyRunsCallback(t *testing.T) {
	calls := 0
	cfg := testCfg()
	cfg.ToggleMode = func() { calls++ }
	_, cmd := New(cfg).Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if cmd == nil {
		t.Fatal("m should return a command")
	}
	cmd()
	if calls != 1 {
		t.Fatalf("toggle ran %d times, want 1", calls)
	}
}

// Without a wired toggle "m" does nothing.
func TestToggleModeKeyInertWithoutCallback(t *testing.T) {
	_, cmd := New(testCfg()).Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if cmd != nil {
		t.Fatal("m without a toggle should return no command")
	}
}

// ModeMsg switches the header, the bindings and the status line live, and
// clears the held-key rows the toggle released.
func TestModeMsgUpdatesHeaderBindingsAndStatus(t *testing.T) {
	m := New(testCfg())
	m.keys = map[string]keyState{"A": {down: true}}
	bindings := map[string]string{"LEFT": "l", "A": "a"}
	next, _ := m.Update(ModeMsg{Mode: config.ModeDouble, Bindings: bindings})
	mm := next.(Model)
	if mm.cfg.Mode != config.ModeDouble || !reflect.DeepEqual(mm.cfg.Bindings, bindings) {
		t.Fatalf("mode/bindings not applied: %v %v", mm.cfg.Mode, mm.cfg.Bindings)
	}
	if !strings.Contains(mm.header(), "DOUBLE") {
		t.Errorf("header does not show DOUBLE: %q", mm.header())
	}
	if !strings.Contains(mm.status, "double") {
		t.Errorf("status = %q, want the new mode", mm.status)
	}
	if len(mm.keys) != 0 {
		t.Errorf("held-key rows survived the toggle: %v", mm.keys)
	}
}

// A profile switch carries the mode it was applied in.
func TestProfileMsgCarriesMode(t *testing.T) {
	next, _ := New(testCfg()).Update(ProfileMsg{Name: "b", Mode: config.ModeDouble, Bindings: map[string]string{"A": "a"}})
	if got := next.(Model).cfg.Mode; got != config.ModeDouble {
		t.Fatalf("mode = %q, want double", got)
	}
}
