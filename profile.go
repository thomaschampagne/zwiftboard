package main

import (
	"log/slog"
	"sync"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"zwiftboard/internal/ble"
	"zwiftboard/internal/config"
	"zwiftboard/internal/keys"
	"zwiftboard/internal/launch"
	"zwiftboard/internal/tui"
)

// profileState is the live state shared between the TUI's switch callback and
// the session goroutines. mu guards the reload/swap; it is never held across
// prog.Send or launchProfile (both can block on the render loop / process
// start). vkIdx is read by the BLE OnLifted hook, hence atomic.
type profileState struct {
	mu    sync.Mutex
	path  string
	names []string
	idx   int
	cur   config.Config // active profile, for reload / manual start
	km    *ble.KeyMap
	vkIdx atomic.Pointer[map[uint16][]string]
	prog  *tea.Program // nil in -plain mode
}

// switchBy moves delta steps through the on-disk profile list and applies the
// new profile live. A no-op when there is nothing to switch to.
func (s *profileState) switchBy(delta int) {
	s.mu.Lock()
	if len(s.names) < 2 {
		s.mu.Unlock()
		return
	}
	name := s.names[nextProfileIndex(len(s.names), s.idx, delta)]
	s.mu.Unlock()
	s.apply(name, true)
}

// reload re-reads the active profile from disk and applies it live, keeping the
// same profile (the TUI "r" shortcut). Unlike switchBy it works with a single
// profile.
func (s *profileState) reload() {
	s.mu.Lock()
	name := s.cur.Profile
	if name == "" && len(s.names) > 0 {
		name = s.names[s.idx]
	}
	s.mu.Unlock()
	if name == "" {
		return
	}
	s.apply(name, false)
}

// launchCurrent starts the active profile's startProgram again (the TUI "s"
// shortcut) so a program the user closed can be reopened. Empty spec is a no-op.
func (s *profileState) launchCurrent() error {
	s.mu.Lock()
	spec := s.cur.StartProgram
	s.mu.Unlock()
	if spec == "" {
		return nil
	}
	return launch.Launch(spec)
}

// current returns the active profile's config, under the lock (a startup
// recovery can replace it while the live feed waits for Bluetooth).
func (s *profileState) current() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// reset replaces the live state after a startup config recovery: the file now
// holds the shipped default, so the shared KeyMap, the VK index, the profile
// list and the focus target must match it before any session uses them. It
// returns the new profile list and index for the screen. Never call it while
// holding the lock.
func (s *profileState) reset(next config.Config) ([]string, int) {
	s.mu.Lock()
	if list, _ := config.ProfileList(s.path); len(list) > 0 {
		s.names = list
	}
	s.cur = next
	s.idx = indexOf(s.names, next.Profile)
	names, idx := s.names, s.idx
	s.mu.Unlock()

	ble.ReleaseAllHeld()
	s.km.Swap(next.Bindings)
	keys.SetWindowTarget(next.FocusProgramNamePrefixOnClick)
	vkIdx := vkIndex(next.Bindings)
	s.vkIdx.Store(&vkIdx)
	return names, idx
}

// apply loads profile name from disk and applies it live: swap the shared KeyMap
// in place (no BLE reconnect), force-release held keys, retarget focus and
// rebuild the VK reverse index, then report the new state. launchProgram
// controls whether the profile's startProgram is (re)launched — true for a
// profile switch, false for a same-profile reload. A reload error leaves all
// state intact, logs, and only surfaces on the screen.
func (s *profileState) apply(name string, launchProgram bool) {
	s.mu.Lock()
	next, err := config.Load(s.path, name)
	if err != nil {
		prog := s.prog
		s.mu.Unlock()
		slog.Warn("profile switch failed — keeping current", "profile", name, "error", err)
		if prog != nil {
			prog.Send(tui.ProfileMsg{Err: err.Error()})
		}
		return
	}
	// Re-read the list each time: a profile edited or added on disk since
	// startup must be reachable without a restart.
	if list, _ := config.ProfileList(s.path); len(list) > 0 {
		s.names = list
	}
	s.idx = indexOf(s.names, next.Profile)
	s.cur = next
	ble.ReleaseAllHeld()
	s.km.Swap(next.Bindings)
	keys.SetWindowTarget(next.FocusProgramNamePrefixOnClick)
	vkIdx := vkIndex(next.Bindings)
	s.vkIdx.Store(&vkIdx)
	idx := s.idx
	prog := s.prog
	s.mu.Unlock()

	if launchProgram {
		launchProfile(next)
	}
	if prog != nil {
		prog.Send(tui.ProfileMsg{
			Name:         next.Profile,
			Bindings:     bindingTokens(next.Bindings),
			Focus:        next.FocusProgramNamePrefixOnClick,
			StartProgram: next.StartProgram,
			NoMapping:    next.Missing,
			Index:        idx,
			Reload:       !launchProgram,
		})
	}
}

// launchProfile starts the selected profile's program. Called once Bluetooth
// is up (once per activation); errors are logged, never fatal.
func launchProfile(cfg config.Config) {
	if cfg.StartProgram == "" {
		return
	}
	if err := launch.Launch(cfg.StartProgram); err != nil {
		slog.Warn("startProgram", "program", cfg.StartProgram, "error", err)
	}
}
