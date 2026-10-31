package tui

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"zwiftboard/internal/config"
)

// Palette. ANSI-256 numbers so it degrades on 256-colour terminals.
var (
	cAccent = lipgloss.Color("75")  // blue
	cOK     = lipgloss.Color("42")  // green
	cWarn   = lipgloss.Color("214") // amber
	cBad    = lipgloss.Color("203") // red
	cMuted  = lipgloss.Color("244")
	cCap    = lipgloss.Color("238") // keycap background

	// Pod-mark colours: the four dots of the Click V2 app icon — cyan on top,
	// orange and green on the middle row, magenta below.
	cDotTop    = lipgloss.Color("45")
	cDotLeft   = lipgloss.Color("208")
	cDotRight  = lipgloss.Color("42")
	cDotBottom = lipgloss.Color("201")

	sTitle   = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sMuted   = lipgloss.NewStyle().Foreground(cMuted)
	sOK      = lipgloss.NewStyle().Foreground(cOK)
	sWarn    = lipgloss.NewStyle().Foreground(cWarn)
	sBad     = lipgloss.NewStyle().Foreground(cBad)
	sBold    = lipgloss.NewStyle().Bold(true)
	sKeycap  = lipgloss.NewStyle().Background(cCap).Padding(0, 1)
	sDown    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(cOK).Padding(0, 1)
	sHold    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(cWarn).Padding(0, 1)
	sChip    = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(cAccent).Padding(0, 1)
	sKeyHint = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	// sReleased gets the same Padding(0,1) as sDown/sHold so "○ released"
	// fills the same 12-column status cell as the other two states.
	sReleased = lipgloss.NewStyle().Foreground(cMuted).Padding(0, 1)
)

const (
	minWidth     = 40
	maxWidth     = 120 // cards (was 100; widened for the shortcut hints in the footer)
	maxLogWidth  = 320 // the log panel may use the whole terminal (double the old 160 cap)
	defaultWidth = 80  // before the first WindowSizeMsg
	logLines     = 10
	// keycapW is the fixed keycap cell width, so the status column starts at
	// the same offset on every row whatever the token length ("a" vs the
	// longest valid token, "backspace").
	keycapW = 9

	// cellWidth is the fixed width of ONE button cell in the two-column
	// table: the widest a row can get, so the columns line up whatever
	// the button's state.
	//
	// The row is name(6) + "→ "(2) + keycap(11) + "  "(2) + status(12..14).
	// The keycap is 11, not 9: the keycap style carries its own one-space
	// padding on each side of the 9-cell token cell. The status cell is 12
	// when the row is idle and 14 when it carries a label (the label style
	// has Padding(0,1) too), so the cell is sized for the LABELLED row:
	// 6+2+11+2+14 = 35. An unmapped row (a "—" with no keycap style) is
	// only 31 and is padded up to the cell width.
	//
	// Measured with ansi.StringWidth, not guessed.
	cellWidth = 35
	// columnGap separates the two pod columns.
	columnGap = 2
	// leftHeading and rightHeading label the two controller columns in double mode.
	leftHeading  = "Left controller"
	rightHeading = "Right controller"
	// twoColumnWidth is the inner width the side-by-side table needs:
	// two cells of 35 plus the gap.
	twoColumnWidth = 2*cellWidth + columnGap
)

// innerWidth is the usable width inside a card, following the terminal.
func (m Model) innerWidth() int { return m.innerWidthMax(maxWidth) }

func (m Model) innerWidthMax(limit int) int {
	w := defaultWidth
	if m.width > 0 {
		w = m.width
	}
	if limit == maxLogWidth && m.width == 0 {
		w = maxWidth // no size yet: keep the logs card as wide as the others
	}
	if w > limit {
		w = limit
	}
	if w < minWidth {
		w = minWidth
	}
	return w - 4 // border + padding
}

// card draws a rounded box with a title line.
func (m Model) card(color lipgloss.Color, title, body string) string {
	return m.cardW(m.innerWidth(), color, title, body)
}

func (m Model) cardW(inner int, color lipgloss.Color, title, body string) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Padding(0, 1).
		Width(inner + 2)
	if title == "" {
		return box.Render(body)
	}
	head := lipgloss.NewStyle().Bold(true).Foreground(color).Render(title)
	return box.Render(head + "\n" + body)
}

func podLine(side string, s PodState) string {
	name := side + " Controller"
	switch s {
	case PodConnected:
		return sOK.Render("● " + name + ": connected")
	case PodDetected:
		return sWarn.Render("◐ " + name + ": detected, connecting…")
	default:
		// The prompt sits right after the state so the fix is on the same line.
		return sBad.Render("○ "+name+": not detected") + sWarn.Render(" → please activate the "+name)
	}
}

// View renders the screen. With Bluetooth off nothing else is shown: setup
// halts until the radio is on. Alt-screen and mouse cell-motion are View
// fields in Bubble Tea v2 (the v1 program options are gone).
func (m Model) View() tea.View {
	if m.recovery.Active {
		return m.recoveryView()
	}
	if m.authGate() {
		return m.authView()
	}
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	if a := m.authAlert(); a != "" {
		b.WriteString(a + "\n")
	}

	if !m.bt {
		body := sBad.Render("○ Bluetooth is OFF") + "\n" +
			sWarn.Render("Please turn Bluetooth ON") + sMuted.Render(" (Windows: Settings > Bluetooth & devices)") + "\n" +
			sMuted.Render("Waiting for it — this screen updates by itself.")
		b.WriteString(m.card(cBad, "Bluetooth", body) + "\n")
		b.WriteString(m.logsPanel())
		b.WriteString(m.footer())
		return screen(b.String())
	}

	b.WriteString(m.banner() + "\n")
	if a := m.focusAlert(); a != "" {
		b.WriteString(a + "\n")
	}

	pods := sOK.Render("● Bluetooth: ON") + "\n" + podLine("Left", m.left) + "\n" + podLine("Right", m.right)
	b.WriteString(m.card(cAccent, "Status", pods) + "\n")
	b.WriteString(m.card(cAccent, "Click V2 · key bindings", m.table()) + "\n")
	b.WriteString(m.logsPanel())
	b.WriteString(m.footer())
	return screen(b.String())
}

// screen wraps rendered content in the v2 View, setting the terminal modes
// the program used to receive as options.
func screen(content string) tea.View {
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// podLogo renders the four-dot pod mark: the app icon's dots (cyan, orange,
// green, magenta) packed on one line, in front of the title.
func podLogo() string {
	dot := func(c lipgloss.Color) string {
		return lipgloss.NewStyle().Foreground(c).Render("●")
	}
	return dot(cDotTop) + " " + dot(cDotLeft) + " " + dot(cDotRight) + " " + dot(cDotBottom)
}

func (m Model) header() string {
	title := sTitle.Render("zwiftboard")

	profile := m.cfg.Profile
	if profile != "" && len(m.cfg.Profiles) > 1 {
		// Show the position so a switch is obvious even when names are similar.
		profile += " (" + strconv.Itoa(m.cfg.ProfileIndex+1) + "/" + strconv.Itoa(len(m.cfg.Profiles)) + ")"
	}
	// The mode and the profile share one chip, so the header reads as one unit.
	chip := ""
	if m.cfg.Mode != "" {
		chip = "Mode: " + strings.ToUpper(string(m.cfg.Mode))
	}
	if profile != "" {
		chip = strings.TrimSpace(chip + "  " + profile)
	}
	version := m.cfg.Version
	update := ""
	if m.latest != "" {
		update = sWarn.Render(" → version " + m.latest + " available")
	}

	// Show both chip and version
	if chip != "" && version != "" {
		title += "  " + sChip.Render(chip) + sMuted.Render(" [version "+version) + update + sMuted.Render("]")
	} else if version != "" {
		title += sMuted.Render(" version ") + sMuted.Render("[version "+version) + update + sMuted.Render("]")
	} else if chip != "" {
		// Show only the chip if no version
		title += "  " + sChip.Render(chip)
	}

	return podLogo() + " " + title
}

func (m Model) table() string {
	var b strings.Builder
	if m.cfg.NoMapping {
		b.WriteString(sMuted.Render("no key mapping — button logging only"))
	} else {
		b.WriteString(m.tableRows())
	}
	focus := sMuted.Render("off")
	if m.cfg.Focus != "" {
		focus = sBold.Render(m.cfg.Focus)
		if m.focusMissing {
			focus += sBad.Render("  ✗ not found")
		}
	}
	out := strings.TrimRight(b.String(), "\n") + "\n\n" + sMuted.Render("Auto-focus window on click starting with name: ") + focus
	if m.cfg.StartProgram != "" {
		out += "\n" + sMuted.Render("Start program on activation: ") + sBold.Render(m.cfg.StartProgram)
	}
	return out
}

// buttonRow renders one button row: the name, the keycap and the status
// label. It is the single-column table's row, moved out unchanged: the
// right-pod-only screen must keep rendering exactly the same bytes.
//
// The row's width depends on the button's state — 31 cells unmapped, 33
// idle, 35 when the status carries a label — so the two-column layout
// pads the cell to cellWidth (the widest case).
func (m Model) buttonRow(name string) string {
	key, ok := m.cfg.Bindings[name]
	// Fixed-width keycap cell (keycapW): the status column starts at
	// the same offset whatever the token, in every phase.
	cap := sMuted.Render("—" + strings.Repeat(" ", keycapW-1))
	label := ""
	var style lipgloss.Style
	if ok {
		tok := padRight(key, keycapW)
		cap = sKeycap.Render(tok)
		switch m.keyPhase(name) {
		case PhasePressed:
			cap = sDown.Render(tok)
			label, style = "▶ pressed", sDown
		case PhaseHold:
			cap = sHold.Render(tok)
			label, style = pulse(m.now())+" hold", sHold
		case PhaseReleased:
			label, style = "○ released", sReleased
		}
	}
	out := padRight(label, 12)
	if label != "" {
		out = style.Render(padRight(label, 12))
	}
	return sBold.Render(padRight(name, 6)) + sMuted.Render("→ ") + cap + "  " + out
}

// tableRows renders the button rows for the active mode. Single mode shows the
// right pod alone, as before. Double mode shows both controllers under their own
// headings: side by side when the card is wide enough, stacked otherwise, so
// nothing wraps.
func (m Model) tableRows() string {
	left, right := m.cfg.LeftButtons, m.cfg.RightButtons
	if m.cfg.Mode != config.ModeDouble {
		return m.rows(right)
	}
	if m.innerWidth() >= twoColumnWidth {
		return m.twoColumns(left, right)
	}
	return sBold.Render(leftHeading) + "\n" + m.rows(left) + sBold.Render(rightHeading) + "\n" + m.rows(right)
}

// rows renders one button row per name, stacked.
func (m Model) rows(names []string) string {
	var b strings.Builder
	for _, name := range names {
		b.WriteString(m.buttonRow(name) + "\n")
	}
	return b.String()
}

// twoColumns renders the left pod's rows beside the right pod's rows, each
// under its heading. The left cell is padded to cellWidth so the columns line
// up whatever the token length; the right cell keeps its natural width (the
// row's own trailing spaces are the table's edge).
func (m Model) twoColumns(left, right []string) string {
	var b strings.Builder
	b.WriteString(padRight(sBold.Render(leftHeading), cellWidth) + strings.Repeat(" ", columnGap) + sBold.Render(rightHeading) + "\n")
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	for i := 0; i < n; i++ {
		var l, r string
		if i < len(left) {
			l = padRight(m.buttonRow(left[i]), cellWidth)
		}
		if i < len(right) {
			r = m.buttonRow(right[i])
		}
		line := l
		if r != "" {
			if l != "" {
				line += strings.Repeat(" ", columnGap)
			}
			line += r
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// modeLabel names a controller mode for the screen.
func modeLabel(mode config.Mode) string {
	if mode == config.ModeDouble {
		return "double (left and right controllers)"
	}
	return "single (right controller)"
}

func (m Model) logsPanel() string {
	if !m.showLogs {
		return ""
	}
	var lines []string
	if m.cfg.Logs != nil {
		m.clampLogScroll()
		lines = m.cfg.Logs.Window(logLines, m.logOffset)
	}
	var body string
	if len(lines) == 0 {
		body = sMuted.Render("no log output yet")
		if m.cfg.Logs == nil {
			body = sMuted.Render("no log output available")
		}
	} else {
		if len(lines) > logLines {
			lines = lines[len(lines)-logLines:]
		}
		w := m.innerWidthMax(maxLogWidth)
		for i, l := range lines {
			lines[i] = sMuted.Render(ansi.Truncate(l, w, "…"))
		}
		body = strings.Join(lines, "\n")
	}
	title := "Logs"
	if m.cfg.Logs != nil && m.cfg.Logs.Len() > logLines {
		title += sMuted.Render(" — k/j or mouse wheel to scroll")
	}
	if m.logOffset > 0 {
		title += sWarn.Render("  ▲ scrolled up")
	}
	return m.cardW(m.innerWidthMax(maxLogWidth), cMuted, title, body) + "\n"
}

func (m Model) footer() string {
	status := ""
	if m.status != "" {
		status = sWarn.Render(m.status) + "\n"
	}
	hint := func(k, label string) string { return sKeyHint.Render(k) + sMuted.Render(" "+label) }
	logs := hint("l", "logs")
	if m.showLogs {
		logs = hint("l", "hide logs")
	}
	f := logs
	if m.cfg.OpenConfig != nil {
		f += sMuted.Render("  ·  ") + hint("e", "edit config")
	}
	if m.cfg.Reload != nil {
		f += sMuted.Render("  ·  ") + hint("r", "reload config")
	}
	if m.cfg.LaunchProgram != nil && m.cfg.StartProgram != "" {
		f += sMuted.Render("  ·  ") + hint("s", "start program")
	}
	if m.cfg.SwitchProfile != nil && len(m.cfg.Profiles) > 1 {
		f += sMuted.Render("  ·  ") + hint("ctrl+←/→", "switch profile")
	}
	if m.cfg.Auth.Login != nil {
		f += sMuted.Render("  ·  ") + hint("z", "sign in")
	}
	if m.cfg.ToggleMode != nil {
		f += sMuted.Render("  ·  ") + hint(toggleModeKey, "Toggle mode")
	}
	f += sMuted.Render("  ·  ") + hint("q", "quit")
	if m.cfg.Demo {
		f += sMuted.Render("  ·  demo: ") + hint("b", "bluetooth") + sMuted.Render(" ") +
			hint("[ ]", "left/right pod") + sMuted.Render(" ") + hint("1-0", "click") + sMuted.Render(" ") + hint("f", "focus alert")
	}
	return status + f
}

// padRight pads s with spaces to n display cells (not bytes): labels carry
// multibyte glyphs (▶, ○, the braille pulse), and byte counting misaligned them.
func padRight(s string, n int) string {
	if w := ansi.StringWidth(s); w < n {
		s += strings.Repeat(" ", n-w)
	}
	return s
}

// pulseFrames advance one frame per holdTick: the braille spinner is the
// "key is repeating" motion cue while a key is held.
var pulseFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// pulse picks the current spinner frame. The division and the modulo stay in
// int64 and the result is made non-negative before indexing: converting the
// raw quotient to int first can wrap negative on 32-bit, and a negative frame
// index would panic.
func pulse(now time.Time) string {
	i := now.UnixNano() / int64(holdTick) % int64(len(pulseFrames))
	if i < 0 {
		i += int64(len(pulseFrames))
	}
	return pulseFrames[i]
}

var (
	sChipOK   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(cOK).Padding(0, 1)
	sChipWarn = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(cWarn).Padding(0, 1)
	sChipBad  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(cBad).Padding(0, 1)
)

// banner is the one-glance state: a coloured chip plus a headline, with the
// supporting rule in a muted second line. Colour is never the only signal —
// the chip carries the word (READY / WAITING).
func (m Model) banner() string {
	if m.left == PodConnected && m.right == PodConnected {
		return m.cardW(m.innerWidth(), cOK, "",
			sChipOK.Render("READY")+" "+sBold.Render("Both controllers connected")+"\n"+
				sMuted.Render("Both pods send clicks — the left pod's arrows and minus are mappable too"))
	}
	return m.cardW(m.innerWidth(), cWarn, "",
		sChipWarn.Render("WAITING")+" "+sBold.Render("Both controllers must be ON")+"\n"+
			sMuted.Render("Either pod sends clicks — keep both on so the pair stays linked"))
}

// focusAlert is shown when a focus window is configured but not running:
// keys.Down drops every click in that case, so the user must know why nothing
// happens.
func (m Model) focusAlert() string {
	if m.cfg.Focus == "" || !m.focusMissing {
		return ""
	}
	return m.cardW(m.innerWidth(), cBad, "",
		sChipBad.Render("ALERT")+" "+sBold.Render(`Focus window "`+m.cfg.Focus+`" not found`)+"\n"+
			sMuted.Render("Key clicks are dropped until it is open."))
}

// recoveryView is the startup config-recovery dialog: the config file could
// not produce a usable binding profile, so the screen names the file, says why,
// and offers to back it up and start from the shipped default. Nothing is
// erased without an explicit "y"; "n" keeps the broken file (no key mapping).
func (m Model) recoveryView() tea.View {
	r := m.recovery
	body := sMuted.Render("File: ") + sBold.Render(r.Path) + "\n"
	if r.Err != "" {
		body += sMuted.Render("Reason: ") + sBad.Render(r.Err) + "\n"
	}
	body += "\n" + sMuted.Render("The shipped default has valid profiles and can replace it.") + "\n" +
		sWarn.Render("Nothing is erased unless you say yes — a backup is kept first ("+r.Path+".bak).")
	if m.status != "" {
		body += "\n\n" + sWarn.Render(m.status)
	}
	body += "\n\n" +
		sKeyHint.Render("y") + sMuted.Render(" back up and restore the built-in default") + "\n" +
		sKeyHint.Render("n") + sMuted.Render(" keep the broken file (no key mapping)")
	return screen(m.card(cBad, "Config file failed to load", body))
}

// recoveryKey routes the recovery dialog's keys. "y" runs RecoveryStart (back
// up, restore the shipped default, verify) and swaps in the returned display
// config so the status screen continues with valid bindings; a failed restore
// keeps the broken file and stays on the dialog. "n"/"esc" decline: the file is
// left alone. Ctrl+C quits. No other shortcut fires while the dialog is open.
func (m Model) recoveryKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "y", "Y":
		recfg, ok := m.cfg.RecoveryStart(m.recovery.Path)
		if !ok {
			return m.setStatus("could not restore the default — the file is left as-is (see the log)")
		}
		m.recovery.Active = false
		m.cfg = recfg
		return m.setStatus("restored the built-in config — the broken file is saved as " + m.recovery.Path + ".bak")
	case "n", "N", "esc":
		m.recovery.Active = false
		return m.setStatus("kept the broken config — fix it and press r to reload, or restart to restore the default")
	}
	return m, nil
}
