package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveKey(t *testing.T) {
	cases := map[string]uint16{
		"a": 0x41, "z": 0x5A, "0": 0x30, "9": 0x39,
		"up": 0x26, "PAGEUP": 0x21, "pagedown": 0x22, "f1": 0x70, "f12": 0x7B,
		"-": 0xBD, "esc": 0x1B,
	}
	for in, want := range cases {
		got, err := resolveKey(in)
		if err != nil {
			t.Fatalf("resolveKey(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("resolveKey(%q) = %#x, want %#x", in, got, want)
		}
	}
	for _, bad := range []string{"", "nope", "f0", "f13", "ab", "f1x"} {
		if _, err := resolveKey(bad); err == nil {
			t.Errorf("resolveKey(%q): want error", bad)
		}
	}
}

func TestLoadKeyMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "LEFT: left\nPLUS: pageup\nA: f5\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m := loadKeyMap(path)
	if len(m) != 3 {
		t.Fatalf("got %d bindings, want 3", len(m))
	}
	if m["LEFT"].vk != 0x25 || m["PLUS"].vk != 0x21 || m["A"].vk != 0x74 {
		t.Errorf("wrong vk codes: %+v", m)
	}
}

func TestLoadKeyMapMissingFile(t *testing.T) {
	if m := loadKeyMap(filepath.Join(t.TempDir(), "nope.yaml")); m != nil {
		t.Errorf("want nil for missing file, got %+v", m)
	}
}
