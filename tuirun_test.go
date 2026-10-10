package main

import (
	"path/filepath"
	"testing"

	"zwiftboard/internal/keys"
)

func TestEditorCommandPerOS(t *testing.T) {
	p := filepath.Join("some", "config.yaml")
	cases := []struct {
		goos string
		want []string
	}{
		{"windows", []string{"notepad.exe", p}},
		{"darwin", []string{"open", "-t", p}},
		{"linux", []string{"xdg-open", p}},
	}
	for _, c := range cases {
		got := editorCommand(c.goos, p).Args
		if len(got) != len(c.want) {
			t.Fatalf("%s: args = %v, want %v", c.goos, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: args = %v, want %v", c.goos, got, c.want)
			}
		}
	}
}

func TestOpenConfigFileReportsStartFailure(t *testing.T) {
	old := startEditor
	startEditor = func(string) error { return errTest }
	t.Cleanup(func() { startEditor = old })
	if err := openConfigFile("config.yaml")(); err == nil {
		t.Fatal("a failed editor start must surface as an error")
	}
}

var errTest = testErr("start failed")

type testErr string

func (e testErr) Error() string { return string(e) }

func TestVKIndex(t *testing.T) {
	idx := vkIndex(map[string]keys.Binding{
		"A": {VK: 0x41, Token: "a"},
		"B": {VK: 0x41, Token: "a"}, // two buttons, same key
		"Z": {VK: 0x5A, Token: "z"},
	})
	if got := idx[0x41]; len(got) != 2 {
		t.Fatalf("0x41 buttons = %v, want A and B", got)
	}
	if got := idx[0x5A]; len(got) != 1 || got[0] != "Z" {
		t.Fatalf("0x5A buttons = %v, want [Z]", got)
	}
}
