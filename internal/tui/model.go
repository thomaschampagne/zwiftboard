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
	Profile   string
	Bindings  map[string]string // button -> key token
	Buttons   []string          // ordered table rows (right pod)
	Focus     string            // focusProgramNameOnClick; "" = off
	NoMapping bool              // config file missing
	Demo      bool              // enable mock toggle keys
}

// Messages sent by main (live) or the demo handler.
type (
	BTMsg    bool
	PodMsg   struct{ Left, Right PodState }
	ClickMsg string
	// expireMsg ends a flash; id guards against a stale expiry clearing a
	// newer click of the same button.
	expireMsg struct {
		button string
		id     int
	}
)

// Model is the Bubble Tea model.
type Model struct {
	cfg         Config
	bt          bool
	left, right PodState
	flash       map[string]int
	seq         int
	width       int
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

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case BTMsg:
		m.bt = bool(msg)
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
