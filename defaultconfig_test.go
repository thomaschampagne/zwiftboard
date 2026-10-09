package main

import (
	"os"
	"path/filepath"
	"testing"

	"zwiftboard/internal/config"
)

func TestEnsureConfigCreatesValidDefaultOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	ensureConfig(path)
	if _, err := config.Load(path, config.DefaultProfile); err != nil {
		t.Fatalf("embedded default does not load: %v", err)
	}
	// An existing (user-edited) file must never be overwritten.
	if err := os.WriteFile(path, []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureConfig(path)
	if b, _ := os.ReadFile(path); string(b) != "custom" {
		t.Errorf("existing config overwritten: %q", b)
	}
}
