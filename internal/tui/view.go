package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	sDown    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(cOK).Padding(0, 1)
	sHold    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(cWarn).Padding(0, 1)
	sChip    = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(cAccent).Padding(0, 1)
	sKeyHint = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
)

const (
	minWidth     = 40
	maxWidth     = 100 // cards
	maxLogWidth  = 320 // the log panel may use the whole terminal (double the old 160 cap)
	defaultWidth = 80  // before the first WindowSizeMsg
	logLines     = 10
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

	b.WriteString(m.banner() + "\n")
	if a := m.focusAlert(); a != "" {
		b.WriteString(a + "\n")
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
			label := ""
			var style lipgloss.Style
			if ok {
				cap = sKeycap.Render(key)
				switch m.keyPhase(name) {
				case PhasePressed:
					cap = sDown.Render(key)
					label, style = "▶ pressed", sDown
				case PhaseHold:
					cap = sHold.Render(key)
					label, style = pulse(m.now())+" hold", sHold
				case PhaseReleased:
					label, style = "○ released", sMuted
				}
			}
			out := padRight(label, 12)
			if label != "" {
				out = style.Render(padRight(label, 12))
			}
			b.WriteString(sBold.Render(padRight(name, 6)) + sMuted.Render("→ ") + cap + "  " + out + "\n")
		}
	}
	focus := sMuted.Render("off")
	if m.cfg.Focus != "" {
		focus = sBold.Render(m.cfg.Focus)
		if m.focusMissing {
			focus += sBad.Render("  ✗ not found")
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n\n" + sMuted.Render("Auto-focus window on click starting with name: ") + focus
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
	f += sMuted.Render("  ·  ") + hint("q", "quit")
	if m.cfg.Demo {
		f += sMuted.Render("  ·  demo: ") + hint("b", "bluetooth") + sMuted.Render(" ") +
			hint("[ ]", "left/right pod") + sMuted.Render(" ") + hint("1-5", "click") + sMuted.Render(" ") + hint("f", "focus alert")
	}
	return status + f
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

// pulseFrames advance one frame per holdTick: the braille spinner is the
// "key is repeating" motion cue while a key is held.
var pulseFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func pulse(now time.Time) string {
	return pulseFrames[int(now.UnixNano()/int64(holdTick))%len(pulseFrames)]
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
				sMuted.Render("Only the Right controller is supported as a virtual keyboard"))
	}
	return m.cardW(m.innerWidth(), cWarn, "",
		sChipWarn.Render("WAITING")+" "+sBold.Render("Both controllers must be ON")+"\n"+
			sMuted.Render("Only the Right one sends clicks; the Left one just has to stay on."))
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
