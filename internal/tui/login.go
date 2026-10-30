package tui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// AuthState is the runtime sign-in form state. The form opens on demand
// (status-screen "z") instead of blocking start-up; AuthNone means no form is
// open, AuthPrompt shows the email/password form.
type AuthState int

const (
	// AuthNone means no form is open: signed in, or the user closed the form.
	AuthNone AuthState = iota
	// AuthPrompt asks for the Zwift email and password.
	AuthPrompt
)

// AuthConfig wires the runtime sign-in form to the sign-in implementation.
// Login runs off the UI goroutine (inside a tea.Cmd). There is no start-up
// gate: AuthNone means no form is open, AuthPrompt opens the form.
type AuthConfig struct {
	State   AuthState
	Message string // shown above the form, e.g. "your session expired"
	// Notice is a persistent status-screen notice for a no-token start. It
	// never opens the form or takes the keyboard.
	Notice string
	// Login signs in. Its error is shown on the form; the password never
	// leaves this call. nil = sign-in not available in this build.
	Login func(email, password string) error
}

// authModel is the runtime sign-in form's state inside the screen model.
type authModel struct {
	state    AuthState
	message  string
	notice   string
	email    string
	password string
	focus    int // 0 = email, 1 = password
	busy     bool
	err      string
	// Caret (rune offset) of each field: where editing happens and where the
	// caret glyph renders. Zero value == empty text, so a fresh form starts
	// with the caret at the end of an empty field.
	emailCaret    int
	passwordCaret int
}

// authLoginDoneMsg reports the sign-in attempt. It carries no credentials.
type authLoginDoneMsg struct{ err error }

func newAuthModel(cfg AuthConfig) authModel {
	return authModel{
		state:   cfg.State,
		message: cfg.Message,
		notice:  cfg.Notice,
	}
}

// authGate reports whether the sign-in form owns the keyboard.
func (m Model) authGate() bool { return m.auth.state != AuthNone }

// updateAuth routes every key while the sign-in form is open. Status-screen
// shortcuts must not fire: typing an email containing "q" cannot quit the app.
func (m Model) updateAuth(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if msg.String() == "esc" {
		// Esc on the not-busy form closes it directly. During a login
		// request it is ignored: closing mid-request would abandon the
		// in-flight sign-in (BLE would start with no solver, and a late
		// success would have no session to serve). The request is short;
		// wait for it, then decide.
		if m.auth.busy {
			return m, nil
		}
		m.auth.state = AuthNone
		m.auth.password, m.auth.passwordCaret = "", 0
		return m, nil
	}
	if m.auth.state != AuthPrompt || m.auth.busy {
		return m, nil
	}
	switch msg.String() {
	case "tab", "shift+tab":
		m.auth.focus = 1 - m.auth.focus
	case "enter":
		return m.submitAuth()
	case "left":
		m.authMoveCaret(m.auth.focus, -1)
	case "right":
		m.authMoveCaret(m.auth.focus, 1)
	case "backspace":
		m.authDeleteBefore(m.auth.focus)
	case "delete":
		m.authDeleteAt(m.auth.focus)
	default:
		if msg.Text != "" {
			m.authInsert(m.auth.focus, msg.Text)
		}
	}
	return m, nil
}

// authCaretField returns the text and caret of field f (0 = email, 1 =
// password). The caret is a rune offset: 0 is before the first rune, len is
// after the last.
func (m Model) authCaretField(f int) (string, int) {
	if f == 0 {
		return m.auth.email, m.auth.emailCaret
	}
	return m.auth.password, m.auth.passwordCaret
}

// authSetField stores a field's text and caret back into the auth state.
func (m *Model) authSetField(f int, text string, caret int) {
	if f == 0 {
		m.auth.email, m.auth.emailCaret = text, caret
		return
	}
	m.auth.password, m.auth.passwordCaret = text, caret
}

// underCaretRuneIndex returns the 0-based index of the rune the caret occupies.
// The caret offset is 0 before the first rune and len after the last, so at
// either extreme the caret sits on the first/last rune: the under-char is
// clamped to the field (0 <= under <= len(runes)-1), never empty. This is the
// boundary under-caret mapping the sign-in cursor relies on; the *cursor column*
// (used for the block position) uses the caret's offset column instead — see
// caretFocusedCell — so every arrow press moves the cursor exactly one cell
// at both ends.
func underCaretRuneIndex(text string, caret int) int {
	runes := []rune(text)
	if len(runes) == 0 {
		return 0
	}
	hi := caret
	if hi < 0 {
		hi = 0
	}
	if hi > len(runes)-1 {
		hi = len(runes) - 1
	}
	return hi
}

// authInsert inserts s at the caret of field f and moves the caret just
// past the inserted text. Empty strings are no-ops.
func (m *Model) authInsert(f int, s string) {
	text, caret := m.authCaretField(f)
	r := []rune(text)
	end := caret
	if end > len(r) {
		end = len(r)
	}
	next := string(r[:end]) + s + string(r[end:])
	m.authSetField(f, next, end+utf8.RuneCountInString(s))
}

// authDeleteBefore removes the rune ending at the caret of field f and steps
// the caret back over it. At the start of a field it is a no-op.
func (m *Model) authDeleteBefore(f int) {
	text, caret := m.authCaretField(f)
	r := []rune(text)
	if caret <= 0 || caret > len(r) {
		return
	}
	m.authSetField(f, string(r[:caret-1])+string(r[caret:]), caret-1)
}

// authDeleteAt removes the rune starting at the caret of field f. The caret
// itself does not move.
func (m *Model) authDeleteAt(f int) {
	text, caret := m.authCaretField(f)
	r := []rune(text)
	if caret < 0 || caret >= len(r) {
		return
	}
	m.authSetField(f, string(r[:caret])+string(r[caret+1:]), caret)
}

// authMoveCaret moves the caret of field f by delta runes, clamped to the
// field's bounds.
func (m *Model) authMoveCaret(f int, delta int) {
	text, caret := m.authCaretField(f)
	n := utf8.RuneCountInString(text)
	if caret < 0 {
		caret = 0
	}
	if caret > n {
		caret = n
	}
	if delta < 0 && caret > 0 {
		caret--
	}
	if delta > 0 && caret < n {
		caret++
	}
	m.authSetField(f, text, caret)
}

// pasteAuth appends a pasted blob to the focused field (password managers).
// A paste from a password manager or a chat message often carries a trailing
// newline or an invisible character; those are invisible in the masked field
// and make the grant fail, so keep only the first line and strip them.
func (m Model) pasteAuth(content string) Model {
	if m.auth.state != AuthPrompt || m.auth.busy {
		return m
	}
	m.authInsert(m.auth.focus, pasteValue(content))
	return m
}

// submitAuth validates the form and schedules the network call off the UI
// goroutine. The password is captured by value; the screen keeps it only so a
// rejected attempt can be retried without retyping.
func (m Model) submitAuth() (tea.Model, tea.Cmd) {
	email := cleanEmail(m.auth.email)
	if email == "" {
		m.auth.err = "Enter your Zwift email."
		return m, nil
	}
	if m.auth.password == "" {
		m.auth.err = "Enter your Zwift password."
		return m, nil
	}
	if hasInvisible(m.auth.password) {
		m.auth.err = "The password contains an invisible control character — clear it and paste again."
		return m, nil
	}
	login := m.cfg.Auth.Login
	if login == nil {
		m.auth.err = "Sign-in is not available in this build."
		return m, nil
	}
	m.auth.busy = true
	m.auth.err = ""
	password := m.auth.password
	return m, func() tea.Msg { return authLoginDoneMsg{err: login(email, password)} }
}

// authView is the runtime sign-in form: the reason, the two fields. It opens
// on demand from the status screen ("z"), not at start-up.
func (m Model) authView() tea.View {
	v := screen(m.authContent())
	// Nano/TextEdit/Vim caret: the classic blinking block cursor drawn over
	// the focused field's under-caret cell (not a glyph in the text). Bubble
	// Tea v2 supports view cursors out of the box — they render a blinking
	// block cursor at the frame cell the position points at. Only set while
	// the form owns the keyboard (the form isn't otherwise rendered).
	caret := &tea.Cursor{
		Position: tea.Position{},
		Color:    nil,
		Shape:    tea.CursorBlock, // nano/TextEdit/Vim square block cursor
		Blink:    true,
	}
	if x, y := caretFocusedCell(m); x >= 0 {
		caret.Position = tea.Position{X: x, Y: y}
	}
	v.Cursor = caret
	return v
}

// caretFocusedCell returns the frame cell (X,Y) the classic block cursor
// should sit on for the focused field: the caret's row/column within the form
// content. Y is the caret's field-row index (the value sits inside the box's
// border line, so the row is the field line itself — not +1 — or the cursor
// lands one row below the letters). X is the value's caret column within the
// field — the caret's rune OFFSET (clamped to the value range, past the last
// rune at the end) — so every arrow press shifts the cursor exactly one cell at
// both boundaries. Field has no under-caret (no focus) returns -1,0.
func caretFocusedCell(m Model) (int, int) {
	f := m.auth.focus
	var label string
	label = "Email"
	if f == 1 {
		label = "Password"
	}
	var value string
	var caret int
	if f == 0 {
		value, caret = m.auth.email, m.auth.emailCaret
	} else {
		value, caret = maskPassword(m.auth.password), m.auth.passwordCaret
	}
	// Locate the value's start column inside the field from the box layout
	// directly (not by scanning the rendered field): an EMPTY field has no value
	// rune to stop a scan at, so scanning would walk to the box border and place
	// the caret outside the field — surviving only after typing. The value area
	// starts one display cell past the label, since the box has one left border
	// cell and zero left padding.
	valueStart := ansi.StringWidth(padRight(label, 10)) + 1
	n := utf8.RuneCountInString(value)
	// The cursor column is the field's caret OFFSET column (clamped to the
	// value's rune range), not the clamped under-rune index. The two coincide
	// at interior offsets but collide at either extreme — offset n-1 and n both
	// sat on the last rune's cell under the under-index clamp — so an arrow off
	// the last character left the block cursor unmoved (an extra arrow was
	// needed). The offset column instead makes every arrow press shift the cursor
	// exactly one cell at both boundaries (nano/TextEdit/Vim cursor column).
	// The field value itself stays plain text; the caret is a separate library
	// block cursor, so the rune count (e.g. the masked password's bullets) is
	// unchanged.
	x := valueStart + caret
	if x < valueStart {
		x = valueStart
	}
	if x > valueStart+int(n) {
		x = valueStart + int(n)
	}

	// Find the field's row index within the form content. The label and the
	// boxed value render on the SAME line (the box's left/right borders and
	// inner padding are inline, within this one content line), so the value's
	// column X already accounts for everything before it on that row; the row
	// Y is just the line index itself. Using i+1 would drop the cursor one row
	// below the value (visually "a line under the letters").
	content := m.authContent()
	for i, ln := range strings.Split(content, "\n") {
		lbl := strings.TrimSpace(ansi.Strip(ln))
		if strings.HasPrefix(lbl, label) {
			return x, i
		}
	}
	return -1, 0
}

// authContent is the sign-in form's rendered content (before the screen
// wrapper), split into lines by authClickField for mouse click mapping.
func (m Model) authContent() string {
	inner := m.innerWidth()
	if inner > 72 {
		inner = 72
	}
	var b strings.Builder
	b.WriteString(podLogo() + " " + sTitle.Render("zwiftboard") + "\n\n")

	why := sBold.Render("The LEFT controller needs a valid Zwift unlock (it lasts a few days).") + "\n" +
		"Sign in once so zwiftboard can renew it automatically.\n" +
		sWarn.Render("Without it, the LEFT may stop when its unlock expires; only the RIGHT is guaranteed.") + "\n\n" +
		sMuted.Render("Your email and password are used once and never stored.") + "\n" +
		sMuted.Render("Only the resulting session token is saved next to config.yml.")
	if m.auth.message != "" {
		why = sWarn.Render(m.auth.message) + "\n\n" + why
	}
	b.WriteString(m.cardW(inner, cAccent, "Zwift sign-in", why) + "\n\n")

	b.WriteString(m.authField("Email", m.auth.email, m.auth.emailCaret, m.auth.focus == 0) + "\n")
	b.WriteString(m.authField("Password", maskPassword(m.auth.password), m.auth.passwordCaret, m.auth.focus == 1) + "\n")
	if hint := passwordHint(m.auth.password); hint != "" && !m.auth.busy {
		b.WriteString(sWarn.Render(hint) + "\n")
	}
	b.WriteString("\n")

	if m.auth.err != "" {
		b.WriteString(sBad.Render(m.auth.err) + "\n")
	}
	if m.auth.busy {
		b.WriteString(sWarn.Render(pulse(m.now())+" Signing in…") + "\n")
	}
	hint := func(k, label string) string { return sKeyHint.Render(k) + sMuted.Render(" "+label) }
	b.WriteString(hint("enter", "sign in") + sMuted.Render("   ") +
		hint("tab", "switch field") + sMuted.Render("   ") +
		hint("esc", "close") + sMuted.Render("   ") +
		hint("ctrl+c", "quit") + "\n")
	return b.String()
}

// authClickField maps a click row to the sign-in field row it landed on:
// 0 = email, 1 = password. The two field rows are the first lines of the
// form whose labels start with "Email" and "Password"; any other row (or
// out of bounds) returns -1 so the click leaves the fields alone. Click
// coordinates (Y) are zero-based row indices within the view content.
func (m Model) authClickField(y int) int {
	content := m.authContent()
	lines := strings.Split(content, "\n")
	emailRow, passwordRow := -1, -1
	for i, ln := range lines {
		label := strings.TrimSpace(ansi.Strip(ln))
		switch {
		case strings.HasPrefix(label, "Email"):
			emailRow = i
		case strings.HasPrefix(label, "Password"):
			passwordRow = i
		}
	}
	if emailRow < 0 || passwordRow < 0 {
		return -1
	}
	if y == emailRow {
		return 0
	}
	if y == passwordRow {
		return 1
	}
	return -1
}

// authField renders one labelled input box; the focused box gets the accent
// border and a caret at the field's rune offset. Long values are truncated so
// the layout never wraps.
func (m Model) authField(label, value string, caret int, focused bool) string {
	w := 42
	if inner := m.innerWidth(); inner < w+14 {
		w = inner - 14
	}
	if w < 16 {
		w = 16
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(cMuted).Padding(0, 1).Width(w)
	if focused {
		box = box.BorderForeground(cAccent)
	}
	shown := ansi.Truncate(value, w, "…")
	return sMuted.Render(padRight(label, 10)) + box.Render(shown)
}

// pasteValue keeps the first line of a paste and drops invisible characters.
// A copied password often ends with a newline; a copied address can carry a
// zero-width character. Neither is visible in the masked field, and either
// makes the token request fail.
func pasteValue(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.Map(stripInvisible, s)
}

// cleanEmail trims the email and removes invisible characters that TrimSpace
// does not cover (NUL, DEL, zero-width spaces, BOM).
func cleanEmail(s string) string {
	return strings.Map(stripInvisible, strings.TrimSpace(s))
}

// hasInvisible reports whether s contains a character stripInvisible removes.
func hasInvisible(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return stripInvisible(r) < 0 }) >= 0
}

// stripInvisible removes C0 controls, DEL and the zero-width characters a
// paste can smuggle in. It returns -1 so it can be used with strings.Map.
func stripInvisible(r rune) rune {
	switch r {
	case 0x7f, '\u200b', '\u200c', '\u200d', '\u2060', '\ufeff':
		return -1
	}
	if r < 0x20 {
		return -1
	}
	return r
}

// passwordHint flags input that is almost certainly a paste artifact. It is a
// hint, not a rewrite: a password may legitimately contain spaces. The value is
// never logged or rendered, only the reason it will probably be rejected.
func passwordHint(password string) string {
	if strings.ContainsAny(password, "\r\n") {
		return "Check the password: it contains a line break (likely from a paste)."
	}
	if password != strings.TrimSpace(password) {
		return "Check the password: it starts or ends with a space."
	}
	return ""
}

// maskPassword hides the real password: one bullet per rune, so the length is
// visible (typing feedback) without the content.
func maskPassword(s string) string {
	return strings.Repeat("•", utf8.RuneCountInString(s))
}

// authAlert is the persistent status-screen notice for a no-token start: the
// RIGHT pod is unaffected, the LEFT pod's button engine will stop once its
// unlock expires.
func (m Model) authAlert() string {
	if m.auth.notice == "" {
		return ""
	}
	return m.cardW(m.innerWidth(), cWarn, "",
		sChipWarn.Render("NO SIGN-IN")+" "+sBold.Render("LEFT unlock not renewable")+"\n"+
			sMuted.Render(m.auth.notice)+"\n"+
			sMuted.Render("Press z to sign in to renew it; the RIGHT controller always works."))
}
