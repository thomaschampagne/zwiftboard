// Package tui is the Bubble Tea status screen. It is purely observational and
// imports no BLE code: main feeds it messages (live) or the demo keys do
// (Config.Demo), so nothing here can influence a session.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// KeyStateMsg reports one emulated key's transition: Down true on press,
// false on release. The mirrored pair fires it twice; Update dedups.
type KeyStateMsg struct {
	Button string
	Down   bool
}

// demoPressMsg starts a demo press: down now, a scheduled KeyStateMsg up
// after demoHoldFor, so the screen walks pressed → hold → released.
type demoPressMsg struct{ button string }

// keyExpireMsg clears a released key's fade-out row.
type keyExpireMsg struct{ button string }

// KeyPhase is what a key row shows. Derived from keyState + clock.
type KeyPhase int

const (
	PhaseIdle KeyPhase = iota
	PhasePressed
	PhaseHold
	PhaseReleased
)

const (
	// holdAfter mirrors keys.keyRepeatDelay: pressed flips to hold when the
	// emulated auto-repeat would have started.
	holdAfter = 250 * time.Millisecond
	// releaseFor is how long the released row lingers before vanishing.
	releaseFor = 900 * time.Millisecond
	// holdTick is the redraw cadence while a key is down (pulse animation).
	holdTick = 100 * time.Millisecond
	// demoHoldFor: the demo key stays down long enough to pass through hold.
	demoHoldFor = 1200 * time.Millisecond
)

// PodState is the observed state of one Click pod.
type PodState int

const (
	PodOff      PodState = iota // not detected
	PodDetected                 // advertising, session pending
	PodConnected
)

// Config is everything the screen shows that does not change at runtime.
type Config struct {
	Profile    string
	Bindings   map[string]string // button -> key token
	Buttons    []string          // ordered table rows (right pod)
	Focus      string            // focusProgramNamePrefixOnClick; "" = off
	NoMapping  bool              // config file missing
	Demo       bool              // enable mock toggle keys
	Logs       *LogBuffer        // log panel source; nil = none
	ConfigPath string            // shown in status messages
	OpenConfig func() error      // opens the config file in an editor; nil = shortcut off
}

// Messages sent by main (live) or the demo handler.
type (
	BTMsg  bool
	PodMsg struct{ Left, Right PodState }
	// FocusMsg reports whether the configured focus window currently exists.
	FocusMsg bool
	// openedMsg is the result of the editor launch.
	openedMsg struct{ err error }
	// statusExpireMsg clears the status line; id guards against a stale expiry.
	statusExpireMsg struct{ id int }
)

// Model is the Bubble Tea model.
type Model struct {
	cfg          Config
	bt           bool
	left, right  PodState
	keys         map[string]keyState // live emulated-key state, copy-on-write
	clock        func() time.Time    // test hook; nil = time.Now
	width        int
	showLogs     bool
	status       string // transient feedback line (empty = none)
	statusID     int
	logOffset    int  // lines scrolled up from the newest; 0 = following the bottom
	focusMissing bool // default false: no alert until a FocusMsg says so
}

// New returns a model with Bluetooth off, both pods not detected and no key
// state.
func New(cfg Config) Model {
	return Model{cfg: cfg, keys: map[string]keyState{}}
}

// keyState is one emulated key's state: at is the press time while down and
// the release time while up.
type keyState struct {
	down bool
	at   time.Time
}

func (m Model) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

// keyPhase derives the row's phase: pressed < holdAfter, hold until release,
// released for releaseFor, then idle (the entry is removed by keyExpireMsg).
func (m Model) keyPhase(button string) KeyPhase {
	s, ok := m.keys[button]
	if !ok {
		return PhaseIdle
	}
	age := m.now().Sub(s.at)
	switch {
	case s.down && age < holdAfter:
		return PhasePressed
	case s.down:
		return PhaseHold
	case age < releaseFor:
		return PhaseReleased
	default:
		return PhaseIdle
	}
}

// anyKeyDown reports whether any emulated key is held (faster redraw tick).
func (m Model) anyKeyDown() bool {
	for _, s := range m.keys {
		if s.down {
			return true
		}
	}
	return false
}

// refreshEvery re-renders the screen so new log lines appear in the log panel
// even when no other message arrives.
const refreshEvery = 500 * time.Millisecond

// repaintMsg is the ONE-SHOT redraw a press schedules: bubbletea rebuilds
// the View on any message, so a press needs no chain — a recurring tick per
// press would add one permanent timer chain to the loop with every click.
type repaintMsg struct{}

type tickMsg struct{}

// tickCmd schedules the next redraw: fast while a key is down (the hold
// pulse), slow otherwise (log lines). This is the only recurring chain: it is
// armed by Init and re-armed by tickMsg alone.
func (m Model) tickCmd() tea.Cmd {
	d := refreshEvery
	if m.anyKeyDown() {
		d = holdTick
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) Init() tea.Cmd { return m.tickCmd() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case openedMsg:
		if msg.err != nil {
			return m.setStatus("Could not open " + m.cfg.ConfigPath + ": " + msg.err.Error())
		}
		return m.setStatus("Opened " + m.cfg.ConfigPath + " in your editor. Restart zwiftboard to apply changes.")
	case statusExpireMsg:
		if msg.id == m.statusID {
			m.status = ""
		}
	case tickMsg:
		return m, m.tickCmd()
	case repaintMsg:
		// One-shot redraw, nothing to re-arm: the View rebuilds because a
		// message arrived. tickMsg (Init-driven) owns the timer chain.
		return m, nil
	case BTMsg:
		m.bt = bool(msg)
	case FocusMsg:
		m.focusMissing = !bool(msg)
	case PodMsg:
		m.left, m.right = msg.Left, msg.Right
	case KeyStateMsg:
		if _, mapped := m.cfg.Bindings[msg.Button]; !mapped {
			return m, nil
		}
		if msg.Down {
			return m.keyDown(msg.Button)
		}
		return m.keyUp(msg.Button)
	case demoPressMsg:
		if _, mapped := m.cfg.Bindings[msg.button]; !mapped {
			return m, nil
		}
		next, fast := m.keyDown(msg.button)
		return next, tea.Batch(fast, tea.Tick(demoHoldFor, func(time.Time) tea.Msg {
			return KeyStateMsg{Button: msg.button, Down: false}
		}))
	case keyExpireMsg:
		if s, ok := m.keys[msg.button]; ok && !s.down {
			k := make(map[string]keyState, len(m.keys))
			for n, v := range m.keys {
				k[n] = v
			}
			delete(k, msg.button)
			m.keys = k
		}
	case tea.MouseMsg:
		// Wheel over the log panel scrolls it; only while the panel is shown.
		if m.showLogs && m.cfg.Logs != nil {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.logOffset++
			case tea.MouseButtonWheelDown:
				if m.logOffset > 0 {
					m.logOffset--
				}
			}
			m.clampLogScroll()
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
		if msg.String() == "l" {
			m.showLogs = !m.showLogs
			return m, nil
		}
		if m.showLogs && m.cfg.Logs != nil {
			switch msg.String() {
			case "k":
				m.logOffset++
				m.clampLogScroll()
				return m, nil
			case "j":
				if m.logOffset > 0 {
					m.logOffset--
				}
				return m, nil
			}
		}
		if msg.String() == "e" && m.cfg.OpenConfig != nil {
			open := m.cfg.OpenConfig
			return m, func() tea.Msg { return openedMsg{err: open()} }
		}
		if m.cfg.Demo {
			return m.demoKey(msg.String())
		}
	}
	return m, nil
}

// clampLogScroll keeps logOffset between 0 (newest lines) and the oldest
// reachable position; a log shorter than the panel never scrolls.
func (m *Model) clampLogScroll() {
	max := 0
	if m.cfg.Logs != nil {
		max = m.cfg.Logs.Len() - logLines
	}
	if max < 0 {
		max = 0
	}
	if m.logOffset > max {
		m.logOffset = max
	}
	if m.logOffset < 0 {
		m.logOffset = 0
	}
}

// keyDown records a press (copy-on-write, like the old flash map) and asks
// for ONE repaint at holdAfter so pressed → hold flips on time. It must stay
// one-shot: returning a recurring tick here would arm a new permanent timer
// chain with every press (unbounded View rebuilds). A duplicate down — the
// pair's mirrored frame — is ignored, keeping the first press's stamp.
func (m Model) keyDown(button string) (tea.Model, tea.Cmd) {
	if s, ok := m.keys[button]; ok && s.down {
		return m, nil
	}
	k := make(map[string]keyState, len(m.keys)+1)
	for n, v := range m.keys {
		k[n] = v
	}
	k[button] = keyState{down: true, at: m.now()}
	m.keys = k
	return m, tea.Tick(holdAfter, func(time.Time) tea.Msg { return repaintMsg{} })
}

// keyUp records a release and schedules the fade-out expiry. A duplicate up
// (mirrored frame) or an up for a key never seen down is ignored.
func (m Model) keyUp(button string) (tea.Model, tea.Cmd) {
	s, ok := m.keys[button]
	if !ok || !s.down {
		return m, nil
	}
	k := make(map[string]keyState, len(m.keys))
	for n, v := range m.keys {
		k[n] = v
	}
	k[button] = keyState{down: false, at: m.now()}
	m.keys = k
	return m, tea.Tick(releaseFor, func(time.Time) tea.Msg { return keyExpireMsg{button} })
}

// statusFor is how long a status message stays on screen.
const statusFor = 5 * time.Second

func (m Model) setStatus(s string) (tea.Model, tea.Cmd) {
	m.statusID++
	m.status = s
	id := m.statusID
	return m, tea.Tick(statusFor, func(time.Time) tea.Msg { return statusExpireMsg{id} })
}
