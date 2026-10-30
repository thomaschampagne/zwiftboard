package tui

import tea "charm.land/bubbletea/v2"

// demoButtons is the flat list the demo's digit shortcuts walk: the left
// pod's buttons first, then the right pod's — the hardware's own order.
// 1-9 select the first nine, 0 the tenth.
func (m Model) demoButtons() []string {
	return append(append([]string{}, m.cfg.LeftButtons...), m.cfg.RightButtons...)
}

// demoKey handles the mock toggles available only with Config.Demo:
// b = Bluetooth, [ and ] = cycle Left/Right pod (l is the log panel),
// f = toggle "focus window missing", 1..9 and 0 = click the n-th button.
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
		if len(k) == 1 {
			btns := m.demoButtons()
			i := -1
			switch {
			case k[0] >= '1' && k[0] <= '9':
				i = int(k[0] - '1')
			case k[0] == '0':
				i = 9
			}
			if i >= 0 && i < len(btns) {
				// Press through Update now: the caller's cmd must already be
				// the press batch (fast tick + scheduled release), not a
				// demoPressMsg the program loop would have to re-feed.
				return m.Update(demoPressMsg{button: btns[i]})
			}
		}
	}
	return m, nil
}
