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
	lacks(t, v, "Left Controller", "key bindings")
}

func TestViewInlinePrompts(t *testing.T) {
	v := on(testCfg(), PodConnected, PodOff).View()
	has(t, v, "Right Controller: not detected → please activate the Right Controller")
	lacks(t, v, "please activate the Left Controller", "disconnected")
	v = on(testCfg(), PodOff, PodOff).View()
	has(t, v, "Left Controller: not detected → please activate the Left Controller",
		"Right Controller: not detected → please activate the Right Controller")
	v = on(testCfg(), PodDetected, PodDetected).View()
	lacks(t, v, "please activate")
}

func TestViewIntroLine(t *testing.T) {
	v := on(testCfg(), PodOff, PodConnected).View()
	has(t, v, "WAITING", "Both controllers must be ON")
	lacks(t, v, "virtual keyboard", "READY")
	v = on(testCfg(), PodConnected, PodConnected).View()
	has(t, v, "READY", "Both controllers connected", "Only the Right controller is supported as a virtual keyboard")
	lacks(t, v, "Both controllers must be ON", "WAITING")
}

func TestViewTable(t *testing.T) {
	v := on(testCfg(), PodConnected, PodConnected).View()
	has(t, v, "test", "PLUS", "+")
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

func TestViewLogPanel(t *testing.T) {
	logs := NewLogBuffer(10)
	logs.Write([]byte("level=INFO msg=hello-from-slog\n"))
	cfg := testCfg()
	cfg.Logs = logs
	m := on(cfg, PodConnected, PodConnected)
	lacks(t, m.View(), "hello-from-slog")
	has(t, m.View(), "l logs")
	m.showLogs = true
	has(t, m.View(), "hello-from-slog", "Logs")
}

func TestViewLogPanelWithoutBuffer(t *testing.T) {
	m := on(testCfg(), PodConnected, PodConnected)
	m.showLogs = true
	has(t, m.View(), "no log output")
}

func TestViewNarrowWidthDoesNotPanic(t *testing.T) {
	for _, w := range []int{0, 1, 10, 30, 200} {
		m := on(testCfg(), PodOff, PodOff)
		m.width = w
		m.showLogs = true
		_ = m.View()
	}
}

func focusCfg() Config {
	cfg := testCfg()
	cfg.Focus = "MyWhoosh"
	return cfg
}

func TestViewFocusMissingAlert(t *testing.T) {
	m := on(focusCfg(), PodConnected, PodConnected)
	lacks(t, m.View(), "not found") // nothing reported yet: no false alarm
	m.focusMissing = true
	has(t, m.View(), "ALERT", `"MyWhoosh" not found`, "clicks are dropped")
	cfg := testCfg() // no focus configured: never alert
	m = on(cfg, PodConnected, PodConnected)
	m.focusMissing = true
	lacks(t, m.View(), "ALERT", "not found")
}

func TestViewLogPanelWiderThanCards(t *testing.T) {
	logs := NewLogBuffer(5)
	line := strings.Repeat("x", 130)
	logs.Write([]byte(line + "\n"))
	cfg := testCfg()
	cfg.Logs = logs
	m := on(cfg, PodConnected, PodConnected)
	m.showLogs = true
	m.width = 170
	has(t, m.View(), line)
	m.width = 80
	v := m.View()
	lacks(t, v, line)
	has(t, v, "…")
}

func TestViewFooterOpenConfigHint(t *testing.T) {
	has(t, on(openCfg(func() error { return nil }), PodConnected, PodConnected).View(), "o edit config")
	lacks(t, on(testCfg(), PodConnected, PodConnected).View(), "edit config")
}

func TestViewShowsStatus(t *testing.T) {
	m := on(testCfg(), PodConnected, PodConnected)
	m.status = "Opened config.yaml"
	has(t, m.View(), "Opened config.yaml")
	m.bt = false // also visible while Bluetooth is off
	has(t, m.View(), "Opened config.yaml")
}
