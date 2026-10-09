package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	panel   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	title   = lipgloss.NewStyle().Bold(true)
	green   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	yellow  = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	red     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dim     = lipgloss.NewStyle().Faint(true)
	flashed = lipgloss.NewStyle().Reverse(true).Bold(true)
)

func podLine(side string, s PodState) string {
	switch s {
	case PodConnected:
		return green.Render("● " + side + " Controller: connected")
	case PodDetected:
		return yellow.Render("◐ " + side + " Controller: detected, connecting…")
	default:
		return red.Render("○ " + side + " Controller: not detected")
	}
}

func podPrompt(side string, s PodState) string {
	if s == PodConnected {
		return ""
	}
	return yellow.Render(side + " controller disconnected: please activate the " + side + " Controller")
}

// View renders the screen. With Bluetooth off nothing else is shown: setup
// halts until the radio is on.
func (m Model) View() string {
	var b strings.Builder
	b.WriteString(title.Render("zwiftboard") + "\n\n")

	if !m.bt {
		b.WriteString(panel.Render(red.Render("○ Bluetooth: OFF")+"\n"+
			yellow.Render("Please turn Bluetooth ON (Windows: Settings > Bluetooth & devices)")+"\n"+
			dim.Render("retrying every 5s…")) + "\n")
		b.WriteString(m.footer())
		return b.String()
	}
	b.WriteString(panel.Render(green.Render("● Bluetooth: ON")) + "\n")

	pods := podLine("Left", m.left) + "\n" + podLine("Right", m.right)
	for _, p := range []string{podPrompt("Left", m.left), podPrompt("Right", m.right)} {
		if p != "" {
			pods += "\n" + p
		}
	}
	pods += "\n" + dim.Render("Both controllers must be ON; only the Right controller sends clicks.")
	b.WriteString(panel.Render(pods) + "\n")

	b.WriteString(panel.Render(m.table()) + "\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m Model) table() string {
	var b strings.Builder
	b.WriteString(title.Render("Profile: ") + m.cfg.Profile + "\n")
	focus := "off"
	if m.cfg.Focus != "" {
		focus = m.cfg.Focus
	}
	b.WriteString(title.Render("Focus window on click: ") + focus + "\n\n")
	b.WriteString(title.Render("Right Click V2 bindings") + "\n")
	if m.cfg.NoMapping {
		b.WriteString(dim.Render("no key mapping — button logging only"))
		return b.String()
	}
	for _, name := range m.cfg.Buttons {
		key, ok := m.cfg.Bindings[name]
		if !ok {
			key = "-"
		}
		row := padRight(name, 6) + " -> " + key
		if m.Flashing(name) {
			row = flashed.Render(row)
		}
		b.WriteString(row + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) footer() string {
	if m.cfg.Demo {
		return "\n" + dim.Render("demo: b bluetooth · l left · r right · 1-5 click · q quit")
	}
	return "\n" + dim.Render("q quit")
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}
