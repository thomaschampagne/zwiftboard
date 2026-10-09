package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	os.Exit(m.Run())
}

func on(cfg Config, l, r PodState) Model {
	m := New(cfg)
	m.bt, m.left, m.right = true, l, r
	return m
}

func has(t *testing.T, v string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(v, s) {
			t.Errorf("view missing %q:\n%s", s, v)
		}
	}
}

func lacks(t *testing.T, v string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if strings.Contains(v, s) {
			t.Errorf("view should not contain %q:\n%s", s, v)
		}
	}
}

func TestViewBTOffHaltsSetup(t *testing.T) {
	m := New(testCfg())
	m.left, m.right = PodConnected, PodConnected // stale poll
	v := m.View()
	has(t, v, "Bluetooth", "turn")
	lacks(t, v, "Left Controller", "test")
}

func TestViewRightDisconnectedPrompt(t *testing.T) {
	v := on(testCfg(), PodConnected, PodOff).View()
	has(t, v, "Right controller disconnected: please activate the Right Controller")
	lacks(t, v, "Left controller disconnected")
}

func TestViewBothOffTwoPrompts(t *testing.T) {
	v := on(testCfg(), PodOff, PodOff).View()
	has(t, v, "Left controller disconnected: please activate the Left Controller",
		"Right controller disconnected: please activate the Right Controller")
}

func TestViewTable(t *testing.T) {
	v := on(testCfg(), PodConnected, PodConnected).View()
	has(t, v, "test", "PLUS", "+", "only the Right controller sends clicks")
}

func TestViewFocus(t *testing.T) {
	cfg := testCfg()
	cfg.Focus = "MyWhoosh"
	has(t, on(cfg, PodConnected, PodConnected).View(), "MyWhoosh")
	has(t, on(testCfg(), PodConnected, PodConnected).View(), "off")
}

func TestViewNoMapping(t *testing.T) {
	cfg := testCfg()
	cfg.NoMapping = true
	has(t, on(cfg, PodConnected, PodConnected).View(), "no key mapping")
}

func TestViewZeroSize(t *testing.T) {
	_ = New(testCfg()).View()
	_ = on(testCfg(), PodOff, PodDetected).View()
}
