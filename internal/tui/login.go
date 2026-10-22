package tui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// AuthState is the start-up sign-in gate. The gate runs before any BLE scan:
// the pod challenges as soon as it connects, so the session that follows the
// gate must have a usable token (or an explicit "skip" from the user).
type AuthState int

const (
	// AuthNone means no gate: already signed in, or the user skipped.
	AuthNone AuthState = iota
	// AuthChecking is a stored session being validated/refreshed.
	AuthChecking
	// AuthPrompt asks for the Zwift email and password.
	AuthPrompt
)

// AuthConfig wires the gate to the sign-in implementation. All callbacks run
// off the UI goroutine (Check/Login inside a tea.Cmd); Done is called exactly
// once, on the UI goroutine, when the gate resolves (signed in or skipped).
type AuthConfig struct {
	State   AuthState
	Message string // shown above the form, e.g. "your session expired"
	// Check validates the stored session. nil = usable; an error's text is
	// shown on the form (transient errors must be turned into nil by main:
	// offline is not a reason to ask for the password).
	Check func() error
	// Login signs in. Its error is shown on the form; the password never
	// leaves this call.
	Login func(email, password string) error
	// Done releases the BLE start-up gate. Must be idempotent.
	Done func()
}

// authModel is the gate's state inside the screen model.
type authModel struct {
	state    AuthState
	message  string
	email    string
	password string
	focus    int // 0 = email, 1 = password
	busy     bool
	err      string
	skipped  bool
	done     bool
}

// authCheckDoneMsg reports the stored-session check.
type authCheckDoneMsg struct{ err error }

// authLoginDoneMsg reports the sign-in attempt. It carries no credentials.
type authLoginDoneMsg struct{ err error }

func newAuthModel(cfg AuthConfig) authModel {
	return authModel{state: cfg.State, message: cfg.Message}
}

// authGate reports whether the sign-in screen owns the keyboard.
func (m Model) authGate() bool { return m.auth.state != AuthNone }

// authCheckCmd validates a stored session when the gate opens on AuthChecking.
func (m Model) authCheckCmd() tea.Cmd {
	if m.auth.state != AuthChecking {
		return nil
	}
	check := m.cfg.Auth.Check
	return func() tea.Msg {
		if check == nil {
			return authCheckDoneMsg{}
		}
		return authCheckDoneMsg{err: check()}
	}
}

// resolveAuth closes the gate exactly once and releases the BLE start-up gate.
func (m Model) resolveAuth(skipped bool) Model {
	if m.auth.state == AuthNone {
		return m
	}
	m.auth.state = AuthNone
	m.auth.skipped = skipped
	if !m.auth.done {
		m.auth.done = true
		if m.cfg.Auth.Done != nil {
			m.cfg.Auth.Done()
		}
	}
	return m
}

// updateAuth routes every key while the gate is open. Status-screen shortcuts
// must not fire: typing an email containing "q" cannot quit the app.
func (m Model) updateAuth(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if msg.String() == "esc" {
		// Esc during the stored-session check also skips: waiting is
		// optional. During a login request it is ignored instead: skipping
		// then would start BLE with no solver and a late success would have
		// no session left to serve. The request is short; wait for it.
		if m.auth.busy {
			return m, nil
		}
		return m.resolveAuth(true), nil
	}
	if m.auth.state != AuthPrompt || m.auth.busy {
		return m, nil
	}
	switch msg.String() {
	case "tab", "shift+tab":
		m.auth.focus = 1 - m.auth.focus
	case "enter":
		return m.submitAuth()
	case "backspace":
		if m.auth.focus == 0 {
			m.auth.email = dropLastRune(m.auth.email)
		} else {
			m.auth.password = dropLastRune(m.auth.password)
		}
	default:
		if msg.Text != "" {
			m.authFieldAppend(msg.Text)
		}
	}
	return m, nil
}

// pasteAuth appends a pasted blob to the focused field (password managers).
func (m Model) pasteAuth(content string) Model {
	if m.auth.state != AuthPrompt || m.auth.busy {
		return m
	}
	m.authFieldAppend(content)
	return m
}

func (m *Model) authFieldAppend(s string) {
	if m.auth.focus == 0 {
		m.auth.email += s
		return
	}
	m.auth.password += s
}

// submitAuth validates the form and schedules the network call off the UI
// goroutine. The password is captured by value; the screen keeps it only so a
// rejected attempt can be retried without retyping.
func (m Model) submitAuth() (tea.Model, tea.Cmd) {
	email := strings.TrimSpace(m.auth.email)
	if email == "" {
		m.auth.err = "Enter your Zwift email."
		return m, nil
	}
	if m.auth.password == "" {
		m.auth.err = "Enter your Zwift password."
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

// authView is the full-screen gate: the reason, the two fields, and the skip
// route. It is the only screen before the BLE scan starts.
func (m Model) authView() tea.View {
	inner := m.innerWidth()
	if inner > 72 {
		inner = 72
	}
	var b strings.Builder
	b.WriteString(podLogo() + " " + sTitle.Render("zwiftboard") + "\n\n")

	if m.auth.state == AuthChecking {
		body := sBold.Render("Checking your Zwift session "+pulse(m.now())) + "\n" +
			sMuted.Render("A stored token was found — renewing it before the controllers connect.")
		b.WriteString(m.cardW(inner, cAccent, "", body) + "\n")
		b.WriteString(sMuted.Render("Ctrl+C quits") + "\n")
		return screen(b.String())
	}

	why := sBold.Render("The LEFT controller needs the ~24h unlock Zwift grants to your account.") + "\n" +
		"Sign in once so zwiftboard can renew it automatically.\n" +
		sWarn.Render("Without it, only the RIGHT controller works normally.") + "\n\n" +
		sMuted.Render("Your email and password are used once and never stored.") + "\n" +
		sMuted.Render("Only the resulting session token is saved next to config.yml.")
	if m.auth.message != "" {
		why = sWarn.Render(m.auth.message) + "\n\n" + why
	}
	b.WriteString(m.cardW(inner, cAccent, "Zwift sign-in", why) + "\n\n")

	b.WriteString(m.authField("Email", strings.TrimSpace(m.auth.email), m.auth.focus == 0) + "\n")
	b.WriteString(m.authField("Password", maskPassword(m.auth.password), m.auth.focus == 1) + "\n\n")

	if m.auth.err != "" {
		b.WriteString(sBad.Render(m.auth.err) + "\n")
	}
	if m.auth.busy {
		b.WriteString(sWarn.Render(pulse(m.now())+" Signing in…") + "\n")
	}
	hint := func(k, label string) string { return sKeyHint.Render(k) + sMuted.Render(" "+label) }
	b.WriteString(hint("enter", "sign in") + sMuted.Render("   ") +
		hint("tab", "switch field") + sMuted.Render("   ") +
		hint("esc", "skip") + sMuted.Render("   ") +
		hint("ctrl+c", "quit") + "\n")
	return screen(b.String())
}

// authField renders one labelled input box; the focused box gets the accent
// border and a cursor. Long values are truncated so the layout never wraps.
func (m Model) authField(label, value string, focused bool) string {
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
	if focused {
		shown += "▏"
	}
	return sMuted.Render(padRight(label, 10)) + box.Render(shown)
}

// maskPassword hides the real password: one bullet per rune, so the length is
// visible (typing feedback) without the content.
func maskPassword(s string) string {
	return strings.Repeat("•", utf8.RuneCountInString(s))
}

func dropLastRune(s string) string {
	_, size := utf8.DecodeLastRuneInString(s)
	if size <= 0 {
		return s
	}
	return s[:len(s)-size]
}

// authAlert is the persistent status-screen warning after a skip: the RIGHT
// pod is unaffected, the LEFT pod's button engine will stop once its unlock
// expires. Restarting is the sign-in path because the pods must re-challenge.
func (m Model) authAlert() string {
	if !m.auth.skipped {
		return ""
	}
	return m.cardW(m.innerWidth(), cBad, "",
		sChipBad.Render("NO SIGN-IN")+" "+sBold.Render("LEFT controller locked")+"\n"+
			sMuted.Render("Zwift sign-in is required to renew its unlock.")+"\n"+
			sMuted.Render("Until then only the RIGHT controller works normally.")+"\n"+
			sMuted.Render("Restart zwiftboard to sign in."))
}
