package tui

import tea "github.com/charmbracelet/bubbletea"

// demoKey handles the mock toggles available only with Config.Demo:
// b = Bluetooth, [ and ] = cycle Left/Right pod (l is the log panel), 1..N = click Buttons[N-1].
func (m Model) demoKey(k string) (tea.Model, tea.Cmd) {
	next := func(s PodState) PodState { return (s + 1) % 3 }
	switch k {
	case "b":
		m.bt = !m.bt
	case "[":
		m.left = next(m.left)
	case "]":
		m.right = next(m.right)
	default:
		if len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
			if i := int(k[0] - '1'); i < len(m.cfg.Buttons) {
				name := m.cfg.Buttons[i]
				return m, func() tea.Msg { return ClickMsg(name) }
			}
		}
	}
	return m, nil
}
