// Package tui is the Bubble Tea status screen. It is purely observational and
// imports no BLE code: main feeds it messages (live) or the demo keys do
// (Config.Demo), so nothing here can influence a session.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// FlashFor is how long a clicked button's row stays highlighted.
const FlashFor = 300 * time.Millisecond

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
	Focus      string            // focusProgramNameOnClick; "" = off
	NoMapping  bool              // config file missing
	Demo       bool              // enable mock toggle keys
	Logs       *LogBuffer        // log panel source; nil = none
	ConfigPath string            // shown in status messages
	OpenConfig func() error      // opens the config file in an editor; nil = shortcut off
}

// Messages sent by main (live) or the demo handler.
type (
	BTMsg    bool
	PodMsg   struct{ Left, Right PodState }
	ClickMsg string
	// FocusMsg reports whether the configured focus window currently exists.
	FocusMsg bool
	// expireMsg ends a flash; id guards against a stale expiry clearing a
	// newer click of the same button.
	expireMsg struct {
		button string
		id     int
	}
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
	flash        map[string]int
	seq          int
	width        int
	showLogs     bool
	status       string // transient feedback line (empty = none)
	statusID     int
	focusMissing bool // default false: no alert until a FocusMsg says so
}

// New returns a model with Bluetooth off and both pods not detected.
func New(cfg Config) Model {
	return Model{cfg: cfg, flash: map[string]int{}}
}

// Flashing reports whether button's row is currently highlighted.
func (m Model) Flashing(button string) bool {
	_, ok := m.flash[button]
	return ok
}

// refreshEvery re-renders the screen so new log lines appear in the log panel
// even when no other message arrives.
const refreshEvery = 500 * time.Millisecond

type tickMsg struct{}

func tick() tea.Cmd { return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} }) }

func (m Model) Init() tea.Cmd { return tick() }

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
		return m, tick()
	case BTMsg:
		m.bt = bool(msg)
	case FocusMsg:
		m.focusMissing = !bool(msg)
	case PodMsg:
		m.left, m.right = msg.Left, msg.Right
	case ClickMsg:
		return m.click(string(msg))
	case expireMsg:
		if m.flash[msg.button] == msg.id {
			delete(m.flash, msg.button)
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
		if msg.String() == "o" && m.cfg.OpenConfig != nil {
			open := m.cfg.OpenConfig
			return m, func() tea.Msg { return openedMsg{err: open()} }
		}
		if m.cfg.Demo {
			return m.demoKey(msg.String())
		}
	}
	return m, nil
}

func (m Model) click(button string) (tea.Model, tea.Cmd) {
	if _, mapped := m.cfg.Bindings[button]; !mapped {
		return m, nil
	}
	// Maps are shared by value copies of Model; copy before writing.
	f := make(map[string]int, len(m.flash)+1)
	for k, v := range m.flash {
		f[k] = v
	}
	m.seq++
	f[button] = m.seq
	m.flash = f
	id := m.seq
	return m, tea.Tick(FlashFor, func(time.Time) tea.Msg { return expireMsg{button, id} })
}

// statusFor is how long a status message stays on screen.
const statusFor = 5 * time.Second

func (m Model) setStatus(s string) (tea.Model, tea.Cmd) {
	m.statusID++
	m.status = s
	id := m.statusID
	return m, tea.Tick(statusFor, func(time.Time) tea.Msg { return statusExpireMsg{id} })
}
