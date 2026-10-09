package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette. ANSI-256 numbers so it degrades on 256-colour terminals.
var (
	cAccent = lipgloss.Color("75")  // blue
	cOK     = lipgloss.Color("42")  // green
	cWarn   = lipgloss.Color("214") // amber
	cBad    = lipgloss.Color("203") // red
	cMuted  = lipgloss.Color("244")
	cCap    = lipgloss.Color("238") // keycap background

	sTitle   = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sMuted   = lipgloss.NewStyle().Foreground(cMuted)
	sOK      = lipgloss.NewStyle().Foreground(cOK)
	sWarn    = lipgloss.NewStyle().Foreground(cWarn)
	sBad     = lipgloss.NewStyle().Foreground(cBad)
	sBold    = lipgloss.NewStyle().Bold(true)
	sKeycap  = lipgloss.NewStyle().Background(cCap).Padding(0, 1)
	sFlash   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(cOK).Padding(0, 1)
	sChip    = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(cAccent).Padding(0, 1)
	sKeyHint = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
)

const (
	minWidth     = 40
	maxWidth     = 100
	defaultWidth = 80 // before the first WindowSizeMsg
	logLines     = 10
)

// innerWidth is the usable width inside a card, following the terminal.
func (m Model) innerWidth() int {
	w := defaultWidth
	if m.width > 0 {
		w = m.width
	}
	if w > maxWidth {
		w = maxWidth
	}
	if w < minWidth {
		w = minWidth
	}
	return w - 4 // border + padding
}

// card draws a rounded box with a title line.
func (m Model) card(color lipgloss.Color, title, body string) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Padding(0, 1).
		Width(m.innerWidth() + 2)
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
// halts until the radio is on.
func (m Model) View() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")

	if !m.bt {
		body := sBad.Render("○ Bluetooth is OFF") + "\n" +
			sWarn.Render("Please turn Bluetooth ON") + sMuted.Render(" (Windows: Settings > Bluetooth & devices)") + "\n" +
			sMuted.Render("Waiting for it — this screen updates by itself.")
		b.WriteString(m.card(cBad, "Bluetooth", body) + "\n")
		b.WriteString(m.logsPanel())
		b.WriteString(m.footer())
		return b.String()
	}

	if m.left == PodConnected && m.right == PodConnected {
		b.WriteString(sOK.Render("✔ Both controllers connected. Only the Right controller is supported as a virtual keyboard") + "\n")
	} else {
		b.WriteString(sWarn.Render("Both controllers must be ON") + sMuted.Render(" (only the Right one types keys)") + "\n")
	}

	pods := sOK.Render("● Bluetooth: ON") + "\n" + podLine("Left", m.left) + "\n" + podLine("Right", m.right)
	b.WriteString(m.card(cAccent, "Status", pods) + "\n")
	b.WriteString(m.card(cAccent, "Right Click V2 · key bindings", m.table()) + "\n")
	b.WriteString(m.logsPanel())
	b.WriteString(m.footer())
	return b.String()
}

func (m Model) header() string {
	left := sTitle.Render("◆ zwiftboard")
	profile := m.cfg.Profile
	if profile == "" {
		return left + "\n"
	}
	return left + "  " + sChip.Render(profile)
}

func (m Model) table() string {
	var b strings.Builder
	if m.cfg.NoMapping {
		b.WriteString(sMuted.Render("no key mapping — button logging only"))
	} else {
		for _, name := range m.cfg.Buttons {
			key, ok := m.cfg.Bindings[name]
			cap := sMuted.Render("—")
			if ok {
				cap = sKeycap.Render(key)
				if m.Flashing(name) {
					cap = sFlash.Render(key) + sOK.Render(" ◀ click")
				}
			}
			b.WriteString(sBold.Render(padRight(name, 6)) + sMuted.Render("→ ") + cap + "\n")
		}
	}
	focus := sMuted.Render("off")
	if m.cfg.Focus != "" {
		focus = sBold.Render(m.cfg.Focus)
	}
	return strings.TrimRight(b.String(), "\n") + "\n\n" + sMuted.Render("Focus window on click: ") + focus
}

func (m Model) logsPanel() string {
	if !m.showLogs {
		return ""
	}
	var lines []string
	if m.cfg.Logs != nil {
		lines = m.cfg.Logs.Lines()
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
		line := lipgloss.NewStyle().Foreground(cMuted).MaxWidth(m.innerWidth())
		for i, l := range lines {
			lines[i] = line.Render(l)
		}
		body = strings.Join(lines, "\n")
	}
	return m.card(cMuted, "Logs", body) + "\n"
}

func (m Model) footer() string {
	hint := func(k, label string) string { return sKeyHint.Render(k) + sMuted.Render(" "+label) }
	logs := hint("l", "logs")
	if m.showLogs {
		logs = hint("l", "hide logs")
	}
	f := logs + sMuted.Render("  ·  ") + hint("q", "quit")
	if m.cfg.Demo {
		f += sMuted.Render("  ·  demo: ") + hint("b", "bluetooth") + sMuted.Render(" ") +
			hint("[ ]", "left/right pod") + sMuted.Render(" ") + hint("1-5", "click")
	}
	return f
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}
