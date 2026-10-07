package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "LEFT: left\nPLUS: pageup\nA: f5\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 {
		t.Fatalf("got %d bindings, want 3", len(m))
	}
	if m["LEFT"].VK != 0x25 || m["PLUS"].VK != 0x21 || m["A"].VK != 0x74 {
		t.Errorf("wrong vk codes: %+v", m)
	}
}

func TestLoadMissingFile(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if m != nil {
		t.Errorf("want nil for missing file, got %+v", m)
	}
}
