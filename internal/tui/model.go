// Package tui is the Bubble Tea status screen. It is purely observational and
// imports no BLE code: main feeds it messages (live) or the demo keys do
// (Config.Demo), so nothing here can influence a session.
package tui

import (
	"maps"
	"time"

	tea "charm.land/bubbletea/v2"

	"zwiftboard/internal/config"
)

// toggleModeKey switches the active profile between single and double mode.
const toggleModeKey = "ctrl+m"

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

// Config is the screen's configuration. Most fields are fixed for the run, but
// the profile fields (Profile/Bindings/Focus/StartProgram/ProfileIndex) are
// updated live by ProfileMsg on a runtime switch.
type Config struct {
	Profile string
	// Mode is the active controller mode; Bindings holds that mode's map.
	Mode     config.Mode
	Bindings map[string]string // button -> key token
	// LeftButtons and RightButtons are the Click V2 pods' button names, in
	// table order: the left pod's column and the right pod's column.
	LeftButtons  []string
	RightButtons []string
	Focus        string       // focusProgramNamePrefixOnClick; "" = off
	StartProgram string       // program launched on activation; "" = none
	NoMapping    bool         // config file missing
	Demo         bool         // enable mock toggle keys
	Logs         *LogBuffer   // log panel source; nil = none
	ConfigPath   string       // shown in status messages
	Version      string       // shown next to the title; the version stamp (e.g. "1.2.3"); "" hides it
	OpenConfig   func() error // opens the config file in an editor; nil = shortcut off
	Profiles     []string     // profile names in order; nil = switching off
	ProfileIndex int          // active index into Profiles
	// SwitchProfile requests a relative profile switch; main wires it and
	// pushes a ProfileMsg back. nil = switching off.
	SwitchProfile func(delta int)
	// ToggleMode flips single/double for the active profile (the "m" shortcut);
	// main wires it and pushes a ModeMsg back. nil = toggling off.
	ToggleMode func()
	// LaunchProgram starts the active profile's startProgram on demand (the "s"
	// shortcut), so a program the user closed can be reopened. nil = not wired;
	// the shortcut is also hidden while StartProgram is "".
	LaunchProgram func() error
	// Reload re-reads the config from disk and applies it to the active profile
	// without a BLE reconnect (the "r" shortcut). nil = reload off.
	Reload func()
	// Auth is the runtime Zwift sign-in form config. AuthNone = form closed,
	// AuthPrompt = the form owns the keyboard.
	Auth AuthConfig
	// RecoveryPath and RecoveryErr describe a config file that failed to load;
	// a non-empty RecoveryPath makes the recovery dialog the first screen.
	RecoveryPath string
	RecoveryErr  string
	// RecoveryStart is called on a user yes: it backs the broken file up, erases
	// it, rebuilds the shipped default, verifies it loads and returns the display
	// config for the restored profile. nil = no recovery dialog (and -demo).
	RecoveryStart func(path string) (Config, bool)
}

// recoveryState is the startup config-recovery dialog inside the screen model.
// It owns the keys until answered and is never active without RecoveryStart.
type recoveryState struct {
	Active bool   // the yes/no dialog is asking the user to restore the default
	Path   string // the config file that failed to load
	Err    string // why it failed, shown in the dialog
}

// Messages sent by main (live) or the demo handler.
type (
	BTMsg  bool
	PodMsg struct{ Left, Right PodState }
	// FocusMsg reports whether the configured focus window currently exists.
	FocusMsg bool
	// UpdateMsg reports a newer release found by the startup check. Sent at
	// most once; empty Latest clears the chip.
	UpdateMsg struct{ Latest string }
	// ProfileMsg reports the profile now active after a switch request. Err,
	// when set, is shown on the status line and changes nothing else. Reload is
	// set when the same profile was re-read from disk, so the screen can say so.
	ProfileMsg struct {
		Name         string
		Mode         config.Mode
		Bindings     map[string]string
		Focus        string
		StartProgram string
		NoMapping    bool
		Index        int
		Err          string
		Reload       bool
	}
	// ModeMsg reports the controller mode after a toggle, with the active map.
	ModeMsg struct {
		Mode     config.Mode
		Bindings map[string]string
	}
	// openedMsg is the result of the editor launch.
	openedMsg struct{ err error }
	// launchedMsg is the result of the manual "s" start.
	launchedMsg struct{ err error }
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
	logOffset    int    // lines scrolled up from the newest; 0 = following the bottom
	focusMissing bool   // default false: no alert until a FocusMsg says so
	latest       string // newer release tag from UpdateMsg; "" = none/dev
	auth         authModel
	recovery     recoveryState // startup config-recovery prompt
}

// New returns a model with Bluetooth off, both pods not detected and no key
// state. A broken config file (RecoveryPath + RecoveryStart) opens on the
// recovery dialog instead of the status screen.
func New(cfg Config) Model {
	m := Model{
		cfg:  cfg,
		keys: map[string]keyState{},
		auth: newAuthModel(cfg.Auth),
	}
	if cfg.RecoveryPath != "" && cfg.RecoveryStart != nil {
		m.recovery = recoveryState{Active: true, Path: cfg.RecoveryPath, Err: cfg.RecoveryErr}
	}
	return m
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

func (m Model) Init() tea.Cmd {
	return m.tickCmd()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case authLoginDoneMsg:
		if !m.authGate() {
			return m, nil
		}
		m.auth.busy = false
		if msg.err != nil {
			m.auth.err = "Sign-in failed: " + msg.err.Error()
			return m, nil
		}
		m.auth.password = ""
		m.auth.passwordCaret = 0 // the password never survives a submit/re-open
		m.auth.state = AuthNone
		m.auth.notice = "" // signed in: the no-token notice is no longer relevant
		return m.setStatus("Zwift sign-in succeeded — the LEFT controller's unlock will renew on the next connect")
	case tea.PasteMsg:
		if m.authGate() {
			return m.pasteAuth(msg.Content), nil
		}
	case openedMsg:
		if msg.err != nil {
			return m.setStatus("Could not open " + m.cfg.ConfigPath + ": " + msg.err.Error())
		}
		return m.setStatus("Opened " + m.cfg.ConfigPath + " in your editor. Restart zwiftboard to apply changes.")
	case launchedMsg:
		if msg.err != nil {
			return m.setStatus("Could not start " + m.cfg.StartProgram + ": " + msg.err.Error())
		}
		return m.setStatus("Started " + m.cfg.StartProgram + ".")
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
	case UpdateMsg:
		m.latest = msg.Latest
	case PodMsg:
		m.left, m.right = msg.Left, msg.Right
	case ProfileMsg:
		if msg.Err != "" {
			return m.setStatus(msg.Err)
		}
		m.cfg.Profile = msg.Name
		m.cfg.Mode = msg.Mode
		m.cfg.Bindings = msg.Bindings
		m.cfg.Focus = msg.Focus
		m.cfg.StartProgram = msg.StartProgram
		m.cfg.NoMapping = msg.NoMapping
		m.cfg.ProfileIndex = msg.Index
		// The focus window and key state belonged to the old profile: clear
		// both so stale rows/alert do not leak across the switch.
		m.focusMissing = false
		m.keys = map[string]keyState{}
		if msg.Reload {
			return m.setStatus("Reloaded profile " + msg.Name + ".")
		}
	case ModeMsg:
		m.cfg.Mode = msg.Mode
		m.cfg.Bindings = msg.Bindings
		// Toggling releases every held key: clear the rows so none stays lit.
		m.keys = map[string]keyState{}
		return m.setStatus("Mode: " + modeLabel(msg.Mode) + ".")
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
			k := m.cloneKeys()
			delete(k, msg.button)
			m.keys = k
		}
	case tea.MouseWheelMsg:
		// Wheel over the log panel scrolls it; only while the panel is shown.
		if m.showLogs && m.cfg.Logs != nil {
			switch msg.Button {
			case tea.MouseWheelUp:
				m.logOffset++
			case tea.MouseWheelDown:
				if m.logOffset > 0 {
					m.logOffset--
				}
			}
			m.clampLogScroll()
		}
	case tea.MouseClickMsg:
		// A click inside the login form focuses the row it landed on. Only
		// while the form owns the keyboard (AuthPrompt); when it isn't open
		// the form isn't rendered anyway, so the click does nothing.
		if m.auth.state == AuthPrompt {
			if f := m.authClickField(msg.Y); f >= 0 {
				m.auth.focus = f
			}
		}
	case tea.KeyPressMsg:
		// The startup config-recovery dialog owns the keys only while it is open
		// (startup gate): until answered, no other shortcut fires — typing a
		// profile name or pressing ctrl+l/ctrl+z during the dialog must not reopen it.
		if m.recovery.Active {
			return m.recoveryKey(msg)
		}
		if m.authGate() {
			return m.updateAuth(msg)
		}
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}
		if msg.String() == "ctrl+l" {
			m.showLogs = !m.showLogs
			return m, nil
		}
		// Open the runtime sign-in form on demand. Gated on Login being
		// wired: without a solver there is nothing to sign in with. While the
		// form is open, updateAuth owns every key, so typing "z" can't re-open.
		if msg.String() == "ctrl+z" && m.cfg.Auth.Login != nil {
			m.auth.state = AuthPrompt
			m.auth.err = ""
			m.auth.email = ""     // fresh form per open
			m.auth.password = ""  // password never survives a submit/re-open
			m.auth.emailCaret = 0 // caret follows the fresh (empty) field text
			m.auth.passwordCaret = 0
			m.auth.focus = 0
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
		if msg.String() == "ctrl+e" && m.cfg.OpenConfig != nil {
			open := m.cfg.OpenConfig
			return m, func() tea.Msg { return openedMsg{err: open()} }
		}
		// Manually (re)start the profile's program (e.g. the user closed it).
		// Gated on StartProgram too: a profile without one has nothing to start.
		if msg.String() == "ctrl+s" && m.cfg.LaunchProgram != nil && m.cfg.StartProgram != "" {
			launch := m.cfg.LaunchProgram
			return m, func() tea.Msg { return launchedMsg{err: launch()} }
		}
		// Re-read config.yaml and apply it to the current profile live.
		if msg.String() == "ctrl+r" && m.cfg.Reload != nil {
			reload := m.cfg.Reload
			return m, func() tea.Msg { reload(); return nil }
		}
		// Toggle and profile switch run in the returned Cmd, never inline: the
		// callbacks may do work, and the render loop must not block. Arrow keys
		// are not matched by the demo handler, so a plain arrow stays inert there.
		if m.cfg.ToggleMode != nil && msg.String() == toggleModeKey {
			toggle := m.cfg.ToggleMode
			return m, func() tea.Msg { toggle(); return nil }
		}
		if m.cfg.SwitchProfile != nil {
			switchProfile := m.cfg.SwitchProfile
			switch msg.String() {
			case "ctrl+right", "ctrl+n":
				return m, func() tea.Msg { switchProfile(1); return nil }
			case "ctrl+left", "ctrl+p":
				return m, func() tea.Msg { switchProfile(-1); return nil }
			}
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

// cloneKeys returns a copy-on-write copy of the emulated-key state, never nil,
// so a caller can add or delete an entry without touching m.keys in place.
func (m Model) cloneKeys() map[string]keyState {
	if m.keys == nil {
		return map[string]keyState{}
	}
	return maps.Clone(m.keys)
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
	k := m.cloneKeys()
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
	k := m.cloneKeys()
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
