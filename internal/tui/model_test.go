package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func testCfg() Config {
	return Config{
		Profile:  "test",
		Buttons:  []string{"Y", "Z", "A", "B", "PLUS"},
		Bindings: map[string]string{"A": "a", "B": "b", "PLUS": "+"},
	}
}

func TestUpdateBTMsg(t *testing.T) {
	m := New(testCfg())
	next, _ := m.Update(BTMsg(true))
	if !next.(Model).bt {
		t.Fatal("BTMsg(true) should set bt")
	}
}

func TestClickFlashesMappedButton(t *testing.T) {
	m := New(testCfg())
	next, cmd := m.Update(ClickMsg("A"))
	m = next.(Model)
	if cmd == nil || !m.Flashing("A") {
		t.Fatalf("A should flash with an expiry cmd (cmd nil=%v)", cmd == nil)
	}
	next, _ = m.Update(expireMsg{button: "A", id: m.flash["A"]})
	if next.(Model).Flashing("A") {
		t.Fatal("flash should expire")
	}
}

func TestRapidClickExtendsFlash(t *testing.T) {
	m := New(testCfg())
	n, _ := m.Update(ClickMsg("A"))
	first := n.(Model).flash["A"]
	n, _ = n.Update(ClickMsg("A"))
	m = n.(Model)
	n, _ = m.Update(expireMsg{button: "A", id: first}) // stale
	if !n.(Model).Flashing("A") {
		t.Fatal("stale expiry must not clear a newer flash")
	}
}

func TestClickUnmappedIgnored(t *testing.T) {
	m := New(testCfg())
	next, cmd := m.Update(ClickMsg("Z"))
	if cmd != nil || next.(Model).Flashing("Z") {
		t.Fatal("unmapped click must be ignored")
	}
}

func TestQuitKey(t *testing.T) {
	_, cmd := New(testCfg()).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q cmd should produce QuitMsg")
	}
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

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

func TestDemoDigitClicks(t *testing.T) {
	n, cmd := demoModel().Update(key("3")) // Buttons[2] = "A"
	if cmd == nil {
		t.Fatal("digit should emit a click cmd")
	}
	n, _ = n.Update(cmd())
	if !n.(Model).Flashing("A") {
		t.Fatal("3 should flash A")
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
