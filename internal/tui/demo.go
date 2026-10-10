package tui

import tea "github.com/charmbracelet/bubbletea"

// demoKey handles the mock toggles available only with Config.Demo:
// b = Bluetooth, [ and ] = cycle Left/Right pod (l is the log panel), f = toggle "focus window missing", 1..N = click Buttons[N-1].
func (m Model) demoKey(k string) (tea.Model, tea.Cmd) {
	next := func(s PodState) PodState { return (s + 1) % 3 }
	switch k {
	case "b":
		m.bt = !m.bt
	case "f":
		m.focusMissing = !m.focusMissing
	case "[":
		m.left = next(m.left)
	case "]":
		m.right = next(m.right)
	default:
		if len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
			if i := int(k[0] - '1'); i < len(m.cfg.Buttons) {
				// Press through Update now: the caller's cmd must already be
				// the press batch (fast tick + scheduled release), not a
				// demoPressMsg the program loop would have to re-feed.
				return m.Update(demoPressMsg{button: m.cfg.Buttons[i]})
			}
		}
	}
	return m, nil
}
