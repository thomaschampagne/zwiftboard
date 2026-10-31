package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// authForm builds a model whose sign-in config is cfg (runtime form: no
// start-up gate state).
func authForm(t *testing.T, cfg *AuthConfig) Model {
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

// nextArrow moves one arrow press into m and returns the model. Code is a
// rune: tea.KeyLeft is the sentinel for the left-arrow key.
func nextArrow(m Model, code rune) Model {
	next, _ := m.Update(tea.KeyPressMsg{Code: code})
	return next.(Model)
}

func TestAuthFormClosesOnEscAtRuntime(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.authGate() {
		t.Fatal("Esc on the runtime form should close it")
	}
}

func TestAuthFormSuccessClearsPasswordAndSetsStatus(t *testing.T) {
	m := authForm(t, &AuthConfig{
		State: AuthPrompt,
		Login: func(string, string) error { return nil },
	})
	m = typeText(m, "rider@example.com")
	m = tab(m)
	m = typeText(m, "hunter2")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if m.authGate() {
		t.Fatal("a successful login should close the form")
	}
	if m.auth.password != "" {
		t.Fatal("the password should be dropped after a successful login")
	}
}

func TestStatusScreenZOpensAuthForm(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthNone, Login: func(string, string) error { return nil }})
	next, _ := m.Update(ctrlKey("z"))
	m = next.(Model)
	if m.auth.state != AuthPrompt {
		t.Fatalf("state = %v, want AuthPrompt", m.auth.state)
	}
}

func TestZInertWithoutLogin(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthNone})
	next, _ := m.Update(ctrlKey("z"))
	if next.(Model).auth.state != AuthNone {
		t.Fatal("'z' must be inert when sign-in is not wired")
	}
}

func TestAuthSignInSuccessShowsStatusAndClearsNotice(t *testing.T) {
	m := authForm(t, &AuthConfig{
		State:  AuthPrompt,
		Notice: "The LEFT controller works only while its current unlock lasts (a few days).",
		Login:  func(string, string) error { return nil },
	})
	m = typeText(m, "rider@example.com")
	m = tab(m)
	m = typeText(m, "hunter2")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.authGate() {
		t.Fatal("a successful login should close the form")
	}
	if m.auth.password != "" {
		t.Fatal("the password should be dropped after a successful login")
	}
	if m.auth.notice != "" {
		t.Fatalf("notice = %q, want cleared on success", m.auth.notice)
	}
	if !strings.Contains(m.status, "Zwift sign-in succeeded") {
		t.Fatalf("status = %q, want sign-in success status", m.status)
	}
}

func TestAuthNoticeIsVisibleAndDoesNotGate(t *testing.T) {
	m := authForm(t, &AuthConfig{
		State:  AuthNone,
		Notice: "The LEFT controller works only while its current unlock lasts (a few days).",
	})
	if m.authGate() {
		t.Fatal("a no-token notice must not own the keyboard")
	}
	has(t, m.View(), "NO SIGN-IN", "LEFT unlock not renewable", "a few days",
		"Press ctrl+z to sign in", "the RIGHT controller always works")
}

func TestAuthNoticeKeepsStatusShortcuts(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthNone, Notice: "notice"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c should still quit from the status screen")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c should produce QuitMsg")
	}
}

func TestAuthFormTypesEmailAndMasksPassword(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	m = typeText(m, "rider@example.com")
	m = tab(m)
	m = typeText(m, "hunter2")

	v := m.View()
	// The masked password renders one bullet per rune, no caret glyph: the
	// pinbar cursor is a separate terminal cursor drawn over the field, not a
	// rune in the text, so the mask keeps all its bullets.
	has(t, v, "rider@example.com", strings.Repeat("•", len("hunter2")))
	if strings.Contains(v.Content, "hunter2") {
		t.Fatalf("the raw password leaked into the screen:\n%s", v.Content)
	}
	if !m.authGate() {
		t.Fatal("typing must not close the form")
	}
}

func TestAuthLoginSubmitRunsLoginCommand(t *testing.T) {
	var gotEmail, gotPass string
	m := authForm(t, &AuthConfig{
		State: AuthPrompt,
		Login: func(email, password string) error {
			gotEmail, gotPass = email, password
			return nil
		},
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
	next, _ = next.Update(cmd())
	m = next.(Model)
	if m.authGate() {
		t.Fatal("a successful login should close the form")
	}
	if m.auth.password != "" {
		t.Fatal("the password should be dropped after a successful login")
	}
	if gotEmail != "rider@example.com" || gotPass != "hunter2" {
		t.Fatalf("login args = %q/%q", gotEmail, gotPass)
	}
}

func TestAuthLoginRejectedStaysOnForm(t *testing.T) {
	m := authForm(t, &AuthConfig{
		State: AuthPrompt,
		Login: func(string, string) error { return errors.New("wrong email or password") },
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
	has(t, m.View(), "Sign-in failed: wrong email or password")
}

func TestAuthGateSwallowsStatusShortcuts(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
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

func TestAuthTypingInsertsAtCaret(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	m = typeText(m, "ab")
	if m.auth.emailCaret != 2 {
		t.Fatalf("caret after typing = %d, want 2", m.auth.emailCaret)
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = next.(Model)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = next.(Model)
	if m.auth.emailCaret != 0 {
		t.Fatalf("caret after two lefts = %d, want 0", m.auth.emailCaret)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	m = next.(Model)
	if m.auth.email != "Xab" {
		t.Fatalf("email = %q, want Xab (typing must insert at the caret)", m.auth.email)
	}
}

func TestAuthArrowsMoveCaret(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	m = typeText(m, "abc") // caret at end
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = next.(Model)
	if m.auth.emailCaret != 3 {
		t.Fatalf("caret right of end must clamp: got %d, want 3", m.auth.emailCaret)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = next.(Model)
	if m.auth.emailCaret != 2 {
		t.Fatalf("caret after one left = %d, want 2", m.auth.emailCaret)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = next.(Model)
	if m.auth.emailCaret != 1 {
		t.Fatalf("caret after two lefts = %d, want 1", m.auth.emailCaret)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = next.(Model)
	if m.auth.emailCaret != 0 {
		t.Fatalf("caret after three lefts = %d, want 0", m.auth.emailCaret)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = next.(Model)
	if m.auth.emailCaret != 0 {
		t.Fatalf("caret at start must clamp: got %d, want 0", m.auth.emailCaret)
	}
}

func TestAuthBackspaceDeletesBeforeCaret(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	m = typeText(m, "abc") // caret 3
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = next.(Model) // caret 2
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = next.(Model) // caret 1
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = next.(Model)
	if m.auth.email != "bc" {
		t.Fatalf("email = %q, want bc (rune before the caret is deleted)", m.auth.email)
	}
	if m.auth.emailCaret != 0 {
		t.Fatalf("caret = %d, want 0 after backspace", m.auth.emailCaret)
	}
}

func TestAuthDeleteDeletesAtCaret(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	m = typeText(m, "abc") // caret 3
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = next.(Model) // caret 2
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDelete})
	m = next.(Model)
	if m.auth.email != "ab" {
		t.Fatalf("email = %q, want ab (rune at the caret is deleted)", m.auth.email)
	}
	if m.auth.emailCaret != 2 {
		t.Fatalf("caret = %d, want 2 after delete (caret does not move)", m.auth.emailCaret)
	}
}

func TestAuthViewRendersCaretAtMovedPosition(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	m = typeText(m, "ab") // caret at end of the email field (offset len = 2 → on "b")
	v := m.View()
	// Classic look: the caret is a block cursor drawn over the field by the
	// library (not a glyph between the letters, not a colored cell). Text stays
	// inline and no caret glyph is in the content.
	if !strings.Contains(v.Content, "ab") {
		t.Fatalf("text should stay inline with the block caret: %q", v.Content)
	}
	lacks(t, v, "█", "no separate caret glyph in the text")
	// The box borders legitimately use "│"; only care that no bare pinbar
	// glyph was spliced into the field text.
	if v.Cursor == nil {
		t.Fatal("the focused field should set the classic block caret cursor")
	}
	if v.Cursor.Shape != tea.CursorBlock {
		t.Fatalf("caret shape = %v, want tea.CursorBlock (nano square)", v.Cursor.Shape)
	}
	if !v.Cursor.Blink {
		t.Fatal("the caret should blink like a classic editor cursor")
	}
	// Y must be the field's row (the line the label+boxed value share) — not
	// +1 — or the block cursor lands one row below the letters.
	lines := strings.Split(m.authContent(), "\n")
	emailRow := -1
	for i, ln := range lines {
		lbl := strings.TrimSpace(ansi.Strip(ln))
		if strings.HasPrefix(lbl, "Email") {
			emailRow = i
		}
	}
	if v.Cursor.Y != emailRow {
		t.Fatalf("caret Y = %d, want field row %d (on the value, not one row below)", v.Cursor.Y, emailRow)
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = next.(Model) // caret on "a" (offset 1 → under "a")
	v = m.View()
	if v.Cursor == nil {
		t.Fatal("the focused field should set the classic block caret cursor")
	}
	if v.Cursor.Shape != tea.CursorBlock {
		t.Fatalf("caret shape = %v, want tea.CursorBlock (nano square)", v.Cursor.Shape)
	}
}

func TestUnderCaretRuneIndexMapsAllCaretPositions(t *testing.T) {
	// The caret sits ON a character at every offset: offset 0 before the first
	// rune → highlight the first, offset len after the last → the last, and all
	// interior offsets highlight the matching rune (boundary only, no gap). This
	// is the boundary under-caret mapping the sign-in cursor relies on; the
	// *cursor column* (for the block position) uses the caret offset directly
	// (see TestCaretCursorsMoveOneCellPerArrowAtBothBoundaries) so every arrow
	// press moves the block one cell even at either boundary.
	type want struct {
		caret int
		under int
	}
	cases := []struct {
		text   string
		starts []want // offset 0..n
	}{
		{"", []want{{0, 0}}},
		{"a", []want{{0, 0}}},
		{"ab", []want{{0, 0}, {1, 1}, {2, 1}}},
		{"abc", []want{{0, 0}, {1, 1}, {2, 2}, {3, 2}}},
	}
	for _, c := range cases {
		r := []rune(c.text)
		for i, wantCaret := range c.starts {
			got := underCaretRuneIndex(c.text, i)
			if got != wantCaret.under {
				t.Errorf("underCaretRuneIndex(%q, %d) = %d, want %d (rune %d under caret)",
					c.text, i, got, wantCaret.under, r[got])
			}
		}
	}
	// Boundary clamp: a caret never flags a rune past the field length.
	if got := underCaretRuneIndex("ab", 99); got != 1 {
		t.Errorf("underCaretRuneIndex clamp overflow = %d, want 1 (last rune)", got)
	}
	if got := underCaretRuneIndex("ab", 999); got != 1 {
		t.Errorf("underCaretRuneIndex clamp overflow = %d, want 1 (last rune)", got)
	}
	if got := underCaretRuneIndex("ab", -5); got != 0 {
		t.Errorf("underCaretRuneIndex negative caret = %d, want 0 (clamp to first)", got)
	}
}

func TestCaretFocusedCellEmptyFieldLandsInValueArea(t *testing.T) {
	// First display (empty field) must place the block caret inside the field's
	// value area, past the box border — not on the border line (which an
	// empty-field start scan would otherwise reach) and not off into the box
	// border on a later render. The value area starts one cell past the label:
	// the box has one left padding cell and no left border.
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	valueStart := ansi.StringWidth(padRight("Email", 10)) + 1
	x, y := caretFocusedCell(m)
	if x != valueStart {
		t.Fatalf("empty field caret X = %d, want %d (value area, past the box border)", x, valueStart)
	}
	if y < 0 {
		t.Fatalf("empty field caret Y < 0, want the field's row")
	}
}

func TestAuthHomeEndMoveCaretToFieldBounds(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	m = typeText(m, "abc")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	m = next.(Model)
	if m.auth.emailCaret != 0 {
		t.Fatalf("home caret = %d, want 0", m.auth.emailCaret)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m = next.(Model)
	if m.auth.emailCaret != 3 {
		t.Fatalf("end caret = %d, want 3", m.auth.emailCaret)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(Model)
	m = typeText(m, "xy")
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	m = next.(Model)
	if m.auth.passwordCaret != 0 || m.auth.emailCaret != 3 {
		t.Fatalf("home must move only the focused field: password=%d email=%d", m.auth.passwordCaret, m.auth.emailCaret)
	}
}

func TestCaretCursorsMoveOneCellPerArrowAtBothBoundaries(t *testing.T) {
	// Each arrow press must shift the block cursor exactly one cell at BOTH
	// boundaries: the old under-index clamp mapped offset n-1 and n onto the
	// last rune's cell, so an arrow off the last character left the cursor
	// unmoved (an extra arrow was needed). The cursor now uses the caret's
	// offset column (clamped to the value's rune range, past the last rune, so
	// the end has no stuck cell), so every press moves it one cell at both ends,
	// clamped once past them.
	m := authForm(t, &AuthConfig{State: AuthPrompt})

	valueStart := ansi.StringWidth(padRight("Email", 10)) + 1

	// "abc" (n=3, caret at end=3 on "c"), caret starts at offset 3 → cursor
	// past the last rune (valueStart+3). Each left moves the cursor one cell
	// toward the start — the reported bug (the old code left the cursor unmoved
	// from the last character, needing a second press) — and each is a real
	// move at the last-char boundary, until the start is reached.
	m = typeText(m, "abc")
	steps := 3 // offsets 3 → 2 → 1 → 0 over 3 lefts (each is a real move)
	for step := 1; step <= steps; step++ {
		m = nextArrow(m, tea.KeyLeft)
		x, _ := caretFocusedCell(m)
		want := valueStart + 3 - step
		if x != want {
			t.Fatalf("left arrow %d: cursor X = %d, want %d (one-cell-per-press off the last char)", step, x, want)
		}
	}
	// One more left clamps at the start: cursor stays on the start cell.
	m = nextArrow(m, tea.KeyLeft)
	x, _ := caretFocusedCell(m)
	if x != valueStart {
		t.Fatalf("left arrow past the start moved the cursor (%d -> %d), want it clamped to %d", x, x, valueStart)
	}
	if m.auth.emailCaret != 0 {
		t.Fatalf("caret walked to = %d, want 0 at the start", m.auth.emailCaret)
	}
}

func TestAuthPasteInsertsAtCaret(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	m = tab(m) // focus the password field
	m.auth.password = "abc"
	m.auth.passwordCaret = 1
	next, _ := m.Update(tea.PasteMsg{Content: "XY"})
	m = next.(Model)
	if m.auth.password != "aXYbc" {
		t.Fatalf("password = %q, want aXYbc (paste inserts at the caret)", m.auth.password)
	}
}

func TestAuthClickFieldMapsRows(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	content := ansi.Strip(m.authContent())
	lines := strings.Split(content, "\n")

	// The Email row is the line whose first label is "Email"; Password the
	// one whose first label is "Password".
	emailRow, passRow := -1, -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "Email"):
			emailRow = i
		case strings.HasPrefix(t, "Password"):
			passRow = i
		}
	}
	if emailRow < 0 || passRow < 0 {
		t.Fatalf("email/password rows not found in form content:\n%s", content)
	}

	if got := m.authClickField(emailRow); got != 0 {
		t.Fatalf("click on the email row = %d, want 0", got)
	}
	if got := m.authClickField(passRow); got != 1 {
		t.Fatalf("click on the password row = %d, want 1", got)
	}
	if got := m.authClickField(emailRow + 5); got != -1 {
		t.Fatalf("click outside the field rows = %d, want -1", got)
	}
	if got := m.authClickField(-5); got != -1 {
		t.Fatalf("click above the form = %d, want -1", got)
	}
}

func TestAuthClickFocusMovesField(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	m.auth.password = "abc"

	emailRow, passwordRow := locateFormRows(t, m)
	if emailRow < 0 || passwordRow < 0 {
		t.Fatal("email/password rows not found in the form content")
	}
	next, _ := m.Update(tea.MouseClickMsg{Y: emailRow})
	m = next.(Model)
	if got := m.auth.focus; got != 0 {
		t.Fatalf("click on email row focused %d, want 0", got)
	}
	next, _ = m.Update(tea.MouseClickMsg{Y: passwordRow})
	m = next.(Model)
	if got := m.auth.focus; got != 1 {
		t.Fatalf("click on password row focused %d, want 1", got)
	}
}

// locateFormRows returns the row indices (within form content) of the Email
// and Password field rows, stripping ANSI before matching the label prefix.
func locateFormRows(t *testing.T, m Model) (int, int) {
	t.Helper()
	content := ansi.Strip(m.authContent())
	lines := strings.Split(content, "\n")
	emailRow, passwordRow := -1, -1
	for i, ln := range lines {
		label := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(label, "Email"):
			emailRow = i
		case strings.HasPrefix(label, "Password"):
			passwordRow = i
		}
	}
	return emailRow, passwordRow
}

func TestAuthClickInertWhenFormClosed(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthNone})
	next, _ := m.Update(tea.MouseClickMsg{Y: 3})
	m = next.(Model)
	if m.authGate() {
		t.Fatal("clicking with the form closed must not open it")
	}
}

func TestPasswordHintFlagsLikelyPasteArtifacts(t *testing.T) {
	if got := passwordHint("hunter2"); got != "" {
		t.Fatalf("clean password hinted: %q", got)
	}
	if got := passwordHint("hunter2\n"); !strings.Contains(got, "line break") {
		t.Fatalf("newline hint = %q", got)
	}
	if got := passwordHint(" hunter2"); !strings.Contains(got, "space") {
		t.Fatalf("space hint = %q", got)
	}
}

func TestAuthViewUsesTimeLimitedCopy(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	v := m.View()
	has(t, v, "a few days")
	lacks(t, v, "~24h")
	lacks(t, v, "daily")
}

func TestAuthViewWarnsAboutPasswordControlCharacter(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	m = tab(m)
	// A paste is sanitized; inject directly to exercise the hint.
	m.auth.password = "hunter2\n"

	has(t, m.View(), "line break")
	lacks(t, m.View(), "hunter2")
}

func TestPasteTakesFirstLineAndStripsInvisible(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	next, _ := m.Update(tea.PasteMsg{Content: "rider@example.com\u200b"})
	m = next.(Model)
	if m.auth.email != "rider@example.com" {
		t.Fatalf("email = %q, want the invisible character stripped", m.auth.email)
	}

	m = tab(m)
	next, _ = m.Update(tea.PasteMsg{Content: "hunter2\nignored"})
	m = next.(Model)
	if m.auth.password != "hunter2" {
		t.Fatalf("password = %q, want only the first line", m.auth.password)
	}
}

func TestSubmitBlocksInvisiblePassword(t *testing.T) {
	called := false
	m := authForm(t, &AuthConfig{
		State: AuthPrompt,
		Login: func(string, string) error { called = true; return nil },
	})
	m.auth.email = "rider@example.com"
	m.auth.password = "hunter2\n"

	next, cmd := m.submitAuth()
	m = next.(Model)
	if cmd != nil || called {
		t.Fatal("a password with a control character must not reach Login")
	}
	if !strings.Contains(m.auth.err, "control character") {
		t.Fatalf("err = %q", m.auth.err)
	}
}

func TestCleanEmailStripsInvisible(t *testing.T) {
	if got := cleanEmail(" rider@example.com\x00\u200b "); got != "rider@example.com" {
		t.Fatalf("cleanEmail = %q", got)
	}
}

func TestAuthPasteFillsFocusedField(t *testing.T) {
	m := authForm(t, &AuthConfig{State: AuthPrompt})
	next, _ := m.Update(tea.PasteMsg{Content: "rider@example.com"})
	m = next.(Model)
	if m.auth.email != "rider@example.com" {
		t.Fatalf("email = %q, want the invisible character stripped", m.auth.email)
	}
	m = tab(m)
	next, _ = m.Update(tea.PasteMsg{Content: "hunter2"})
	m = next.(Model)
	if m.auth.password != "hunter2" {
		t.Fatalf("password = %q, want only the first line", m.auth.password)
	}
	if strings.Contains(m.View().Content, "hunter2") {
		t.Fatal("pasted password rendered in clear")
	}
}

func TestEscWhileLoginIsBusyIsIgnored(t *testing.T) {
	// Closing mid-request would abandon the in-flight sign-in (BLE would
	// start with no solver, and a late success would have no session to
	// serve). The request is short; wait for it, then decide.
	m := authForm(t, &AuthConfig{
		State: AuthPrompt,
		Login: func(string, string) error { return nil },
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

	next, _ = m.Update(authLoginDoneMsg{})
	m = next.(Model)
	if m.authGate() {
		t.Fatal("the login result should close the gate")
	}
}

func TestBackspaceRemovesOneMultiByteRune(t *testing.T) {
	m := Model{}
	m.auth.email, m.auth.emailCaret = "aéb", 2
	m.authDeleteBefore(0)
	if m.auth.email != "ab" || m.auth.emailCaret != 1 {
		t.Fatalf("got %q caret %d, want %q caret 1", m.auth.email, m.auth.emailCaret, "ab")
	}
}
