package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"zwiftboard/internal/ble"
	"zwiftboard/internal/config"
	"zwiftboard/internal/keys"
	"zwiftboard/internal/tui"
	"zwiftboard/internal/unlock"
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

func TestIndexOf(t *testing.T) {
	if indexOf([]string{"a", "b", "c"}, "b") != 1 {
		t.Fatal("b")
	}
	if indexOf([]string{"a", "b"}, "z") != 0 {
		t.Fatal("absent")
	}
	if indexOf(nil, "x") != 0 {
		t.Fatal("nil")
	}
}

func TestBindingTokens(t *testing.T) {
	got := bindingTokens(map[string]keys.Binding{"A": {VK: 0x41, Token: "space"}})
	if got["A"] != "space" || len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestNextProfileIndex(t *testing.T) {
	cases := []struct{ n, idx, d, want int }{
		{3, 0, 1, 1},
		{3, 2, 1, 0},
		{3, 0, -1, 2},
		{3, 1, -5, 2},
		{2, 0, 7, 1},
	}
	for _, c := range cases {
		if got := nextProfileIndex(c.n, c.idx, c.d); got != c.want {
			t.Errorf("nextProfileIndex(%d,%d,%d)=%d want %d", c.n, c.idx, c.d, got, c.want)
		}
	}
}

func writeProfileConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSwitchByAppliesLive(t *testing.T) {
	path := writeProfileConfig(t, "profiles:\n  a:\n    A: space\n  b:\n    A: enter\n")
	km := ble.NewKeyMap(map[string]keys.Binding{"A": {VK: 0x41, Token: "space"}})
	s := &profileState{path: path, names: []string{"a", "b"}, idx: 0, km: km}
	s.vkIdx.Store(&map[uint16][]string{})

	s.switchBy(1)

	if s.idx != 1 || s.names[s.idx] != "b" {
		t.Fatalf("idx=%d names=%v", s.idx, s.names)
	}
	if b, ok := km.Get("A"); !ok || b.Token != "enter" {
		t.Fatalf("bindings not swapped: %v ok=%v", b, ok)
	}
}

func TestSwitchByErrorKeepsState(t *testing.T) {
	path := writeProfileConfig(t, "profiles:\n  a:\n    A: space\n  b:\n    A: space\n")
	km := ble.NewKeyMap(map[string]keys.Binding{"A": {VK: 0x41, Token: "space"}})
	s := &profileState{path: path, names: []string{"a", "b"}, idx: 0, km: km}
	s.vkIdx.Store(&map[uint16][]string{})
	if err := os.WriteFile(path, []byte("::not: yaml: ["), 0o644); err != nil {
		t.Fatal(err)
	}

	s.switchBy(1)

	if s.idx != 0 {
		t.Fatalf("idx changed to %d", s.idx)
	}
	if b, _ := km.Get("A"); b.Token != "space" {
		t.Fatalf("bindings changed: %v", b)
	}
}

// Reload edits the active profile in place — it must work with a single
// profile (unlike switchBy, which needs two) and pick up on-disk changes.
func TestReloadAppliesEditsWithSingleProfile(t *testing.T) {
	path := writeProfileConfig(t, "profiles:\n  a:\n    A: space\n")
	km := ble.NewKeyMap(map[string]keys.Binding{"A": {VK: 0x41, Token: "space"}})
	cur := config.Config{Profile: "a", Bindings: map[string]keys.Binding{"A": {VK: 0x41, Token: "space"}}}
	s := &profileState{path: path, names: []string{"a"}, idx: 0, cur: cur, km: km}
	s.vkIdx.Store(&map[uint16][]string{})

	if err := os.WriteFile(path, []byte("profiles:\n  a:\n    A: enter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.reload()

	if b, ok := km.Get("A"); !ok || b.Token != "enter" {
		t.Fatalf("bindings not reloaded: %v ok=%v", b, ok)
	}
	if s.idx != 0 || s.names[s.idx] != "a" {
		t.Fatalf("reload must stay on the same profile: idx=%d names=%v", s.idx, s.names)
	}
}

// A reload with a broken file must keep the current bindings, like a switch.
func TestReloadErrorKeepsState(t *testing.T) {
	path := writeProfileConfig(t, "profiles:\n  a:\n    A: space\n")
	km := ble.NewKeyMap(map[string]keys.Binding{"A": {VK: 0x41, Token: "space"}})
	cur := config.Config{Profile: "a", Bindings: map[string]keys.Binding{"A": {VK: 0x41, Token: "space"}}}
	s := &profileState{path: path, names: []string{"a"}, idx: 0, cur: cur, km: km}
	s.vkIdx.Store(&map[uint16][]string{})
	if err := os.WriteFile(path, []byte("::not: yaml: ["), 0o644); err != nil {
		t.Fatal(err)
	}

	s.reload()

	if b, _ := km.Get("A"); b.Token != "space" {
		t.Fatalf("bindings changed: %v", b)
	}
}

func TestLaunchCurrentNoProgramIsNoOp(t *testing.T) {
	s := &profileState{cur: config.Config{}}
	if err := s.launchCurrent(); err != nil {
		t.Fatalf("empty startProgram should be a no-op, got %v", err)
	}
}

func TestSolverForOnlyWithAStoredSession(t *testing.T) {
	// No token: the solver stays nil so BLE keeps the old ff 04 00 behaviour.
	// A stored session hands the client to every session, so the first
	// challenge after the gate gets the signed reply.
	dir := t.TempDir()
	c := unlock.New(unlock.NewStore(dir))
	_ = c.Load()
	if got := solverFor(c); got != nil {
		t.Fatal("solverFor = non-nil without a stored session")
	}

	if err := unlock.NewStore(dir).Save(unlock.Tokens{
		AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c = unlock.New(unlock.NewStore(dir))
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := solverFor(c); got == nil {
		t.Fatal("solverFor = nil with a stored session")
	}
}

func TestTuiAuthStateFollowsTheStoredSession(t *testing.T) {
	dir := t.TempDir()
	c := unlock.New(unlock.NewStore(dir))
	loadErr := c.Load()
	auth := tuiAuth(c, loadErr)
	if auth.State != tui.AuthPrompt {
		t.Fatalf("state = %v, want AuthPrompt without a token", auth.State)
	}
	if auth.Login == nil {
		t.Fatal("the prompt needs a Login callback")
	}
	if auth.Check != nil {
		t.Fatal("the prompt must not run a session check")
	}

	if err := unlock.NewStore(dir).Save(unlock.Tokens{
		AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c = unlock.New(unlock.NewStore(dir))
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	auth = tuiAuth(c, nil)
	if auth.State != tui.AuthChecking {
		t.Fatalf("state = %v, want AuthChecking with a token", auth.State)
	}
	if auth.Check == nil {
		t.Fatal("AuthChecking needs a Check callback")
	}
}
