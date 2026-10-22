package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func authPrompt(t *testing.T, cfg *AuthConfig) Model {
	t.Helper()
	c := testCfg()
	c.Auth = *cfg
	return New(c)
}

// typeText feeds printable keys to the focused field.
func typeText(m Model, s string) Model {
	for _, r := range s {
		next, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next.(Model)
	}
	return m
}

func tab(m Model) Model {
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	return next.(Model)
}

func TestAuthPromptSkipResolvesAndBanners(t *testing.T) {
	done := 0
	m := authPrompt(t, &AuthConfig{State: AuthPrompt, Done: func() { done++ }})
	if !m.authGate() {
		t.Fatal("the sign-in gate should be open")
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.authGate() {
		t.Fatal("Esc should close the gate")
	}
	if done != 1 {
		t.Fatalf("Done calls = %d, want 1", done)
	}
	// A second Esc on the status screen must not call Done again.
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if done != 1 {
		t.Fatalf("Done calls after a stray Esc = %d, want 1", done)
	}
	has(t, next.(Model).View(), "LEFT controller locked", "only the RIGHT controller works normally")
}

func TestAuthPromptTypesEmailAndMasksPassword(t *testing.T) {
	done := 0
	m := authPrompt(t, &AuthConfig{State: AuthPrompt, Done: func() { done++ }})
	m = typeText(m, "rider@example.com")
	m = tab(m)
	m = typeText(m, "hunter2")

	v := m.View()
	has(t, v, "rider@example.com", strings.Repeat("•", len("hunter2")))
	if strings.Contains(v.Content, "hunter2") {
		t.Fatalf("the raw password leaked into the screen:\n%s", v.Content)
	}
	if done != 0 {
		t.Fatal("typing must not close the gate")
	}
}

func TestAuthLoginSubmitRunsLoginCommand(t *testing.T) {
	var gotEmail, gotPass string
	done := 0
	m := authPrompt(t, &AuthConfig{
		State: AuthPrompt,
		Login: func(email, password string) error {
			gotEmail, gotPass = email, password
			return nil
		},
		Done: func() { done++ },
	})
	m = typeText(m, " rider@example.com ") // spaces are trimmed
	m = tab(m)
	m = typeText(m, "hunter2")

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should schedule the login")
	}
	if !next.(Model).auth.busy {
		t.Fatal("the form should show a pending login")
	}
	msg := cmd()
	if _, ok := msg.(authLoginDoneMsg); !ok {
		t.Fatalf("login cmd produced %T", msg)
	}
	next, _ = next.Update(msg)
	m = next.(Model)
	if m.authGate() {
		t.Fatal("a successful login should close the gate")
	}
	if done != 1 {
		t.Fatalf("Done calls = %d, want 1", done)
	}
	if gotEmail != "rider@example.com" || gotPass != "hunter2" {
		t.Fatalf("login args = %q/%q", gotEmail, gotPass)
	}
	if m.auth.password != "" {
		t.Fatal("the password should be dropped after a successful login")
	}
}

func TestAuthLoginRejectedStaysOnForm(t *testing.T) {
	done := 0
	m := authPrompt(t, &AuthConfig{
		State: AuthPrompt,
		Login: func(string, string) error { return errors.New("wrong email or password") },
		Done:  func() { done++ },
	})
	m = typeText(m, "rider@example.com")
	m = tab(m)
	m = typeText(m, "nope")

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next, _ = next.Update(cmd())
	m = next.(Model)
	if !m.authGate() {
		t.Fatal("a rejected login must keep the form open")
	}
	if done != 0 {
		t.Fatal("a rejected login must not start BLE")
	}
	has(t, m.View(), "Sign-in failed: wrong email or password")
}

func TestAuthCheckingResolves(t *testing.T) {
	done := 0
	m := authPrompt(t, &AuthConfig{State: AuthChecking, Check: func() error { return nil }, Done: func() { done++ }})
	cmd := m.authCheckCmd()
	if cmd == nil {
		t.Fatal("a checking gate needs a check command")
	}
	next, _ := m.Update(cmd())
	if next.(Model).authGate() {
		t.Fatal("a successful check should open the gate")
	}
	if done != 1 {
		t.Fatalf("Done calls = %d, want 1", done)
	}
}

func TestAuthCheckingFailureShowsForm(t *testing.T) {
	done := 0
	m := authPrompt(t, &AuthConfig{
		State: AuthChecking,
		Check: func() error { return errors.New("your Zwift session expired — sign in again") },
		Done:  func() { done++ },
	})
	next, _ := m.Update(m.authCheckCmd()())
	m = next.(Model)
	if !m.authGate() || m.auth.state != AuthPrompt {
		t.Fatalf("state = %v, want AuthPrompt", m.auth.state)
	}
	if done != 0 {
		t.Fatal("an expired session must not start BLE")
	}
	has(t, m.View(), "your Zwift session expired — sign in again", "Email", "Password")
}

func TestAuthGateSwallowsStatusShortcuts(t *testing.T) {
	m := authPrompt(t, &AuthConfig{State: AuthPrompt})
	next, cmd := m.Update(key("q"))
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("q must type into the email field, not quit")
		}
	}
	if got := next.(Model).auth.email; got != "q" {
		t.Fatalf("email = %q, want q", got)
	}

	_, cmd = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+C should still quit from the gate")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("Ctrl+C should produce QuitMsg")
	}
}

func TestAuthPasteFillsFocusedField(t *testing.T) {
	m := authPrompt(t, &AuthConfig{State: AuthPrompt})
	next, _ := m.Update(tea.PasteMsg{Content: "rider@example.com"})
	m = next.(Model)
	if m.auth.email != "rider@example.com" {
		t.Fatalf("email = %q", m.auth.email)
	}
	m = tab(m)
	next, _ = m.Update(tea.PasteMsg{Content: "hunter2"})
	m = next.(Model)
	if m.auth.password != "hunter2" {
		t.Fatalf("password = %q", m.auth.password)
	}
	if strings.Contains(m.View().Content, "hunter2") {
		t.Fatal("pasted password rendered in clear")
	}
}

func TestAuthSkipBannerVisibleWithoutBluetooth(t *testing.T) {
	m := authPrompt(t, &AuthConfig{State: AuthPrompt})
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model) // bt stays off
	has(t, m.View(), "LEFT controller locked")
}

func TestLateCheckResultAfterSkipIsIgnored(t *testing.T) {
	// Esc during the stored-session check closes the gate and starts BLE with
	// no solver. A check failure that lands afterwards must not reopen the
	// form: nothing would consume its login, and the status screen would show
	// no warning.
	done := 0
	m := authPrompt(t, &AuthConfig{
		State: AuthChecking,
		Check: func() error { return errors.New("your Zwift session expired — sign in again") },
		Done:  func() { done++ },
	})
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.authGate() {
		t.Fatal("Esc should close the checking gate")
	}
	if done != 1 {
		t.Fatalf("Done calls = %d, want 1", done)
	}

	next, _ = m.Update(authCheckDoneMsg{err: errors.New("expired")})
	m = next.(Model)
	if m.authGate() {
		t.Fatal("a late check failure reopened the gate after a skip")
	}
	if done != 1 {
		t.Fatalf("Done calls = %d after the late check, want 1", done)
	}
	has(t, m.View(), "LEFT controller locked")
}

func TestLateLoginResultAfterSkipIsIgnored(t *testing.T) {
	// Belt and braces for any other path that closes the gate while a login
	// is in flight: a late success must not close it again (Done once) nor
	// clear the skip banner.
	done := 0
	m := authPrompt(t, &AuthConfig{
		State: AuthPrompt,
		Login: func(string, string) error { return nil },
		Done:  func() { done++ },
	})
	m = typeText(m, "rider@example.com")
	m = tab(m)
	m = typeText(m, "hunter2")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)

	m = m.resolveAuth(true)
	if done != 1 {
		t.Fatalf("Done calls = %d, want 1", done)
	}
	next, _ = m.Update(authLoginDoneMsg{})
	m = next.(Model)
	if m.authGate() {
		t.Fatal("a late login result reopened the gate")
	}
	if done != 1 {
		t.Fatalf("Done calls = %d after the late login, want 1", done)
	}
	if !m.auth.skipped {
		t.Fatal("a late login success cleared the skip banner")
	}
}

func TestEscWhileLoginIsBusyIsIgnored(t *testing.T) {
	// Skipping mid-request would start BLE with a nil solver and then a late
	// success would have nobody to hand the session to. The request is short;
	// wait for it, then decide.
	done := 0
	m := authPrompt(t, &AuthConfig{
		State: AuthPrompt,
		Login: func(string, string) error { return nil },
		Done:  func() { done++ },
	})
	m = typeText(m, "rider@example.com")
	m = tab(m)
	m = typeText(m, "hunter2")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if !m.authGate() || !m.auth.busy {
		t.Fatalf("Esc during the login must be ignored (gate=%v busy=%v)", m.authGate(), m.auth.busy)
	}
	if done != 0 {
		t.Fatal("Done ran while the login was still in flight")
	}

	next, _ = m.Update(authLoginDoneMsg{})
	m = next.(Model)
	if m.authGate() {
		t.Fatal("the login result should close the gate")
	}
	if done != 1 {
		t.Fatalf("Done calls = %d, want 1", done)
	}
}
