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
