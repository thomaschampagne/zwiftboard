package tui

import (
	"strings"
	"testing"
)

func TestInputWarningVisibleAndCleared(t *testing.T) {
	m := New(Config{InputWarning: "Accessibility permission required", Focus: "TextEdit"})
	if got := m.focusAlert(); !strings.Contains(got, "Accessibility permission required") {
		t.Fatalf("input warning hidden: %q", got)
	}
	next, _ := m.Update(InputWarningMsg(""))
	m = next.(Model)
	if got := m.focusAlert(); got != "" {
		t.Fatalf("stale input warning: %q", got)
	}
	next, _ = m.Update(InputWarningMsg("Auto-focus is unsupported on macOS"))
	m = next.(Model)
	if got := m.focusAlert(); !strings.Contains(got, "unsupported") {
		t.Fatal(got)
	}
}

func TestBluetoothSetupUsesPlatformHelp(t *testing.T) {
	m := New(Config{BluetoothHelp: "Allow Bluetooth in Privacy & Security"})
	got := m.View().Content
	if !strings.Contains(got, "Privacy & Security") || strings.Contains(got, "Windows:") {
		t.Fatalf("wrong platform setup guidance: %s", got)
	}
}
