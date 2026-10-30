package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"

	"zwiftboard/internal/ble"
	"zwiftboard/internal/config"
	"zwiftboard/internal/keys"
	"zwiftboard/internal/tui"
	"zwiftboard/internal/unlock"
	"zwiftboard/internal/update"
	"zwiftboard/internal/zwift"
)

// bindingTokens flattens resolved bindings to the token map the TUI shows.
func bindingTokens(m map[string]keys.Binding) map[string]string {
	out := make(map[string]string, len(m))
	for name, b := range m {
		out[name] = b.Token
	}
	return out
}

// indexOf returns the position of name in names, or 0 when absent.
func indexOf(names []string, name string) int {
	for i, n := range names {
		if n == name {
			return i
		}
	}
	return 0
}

// nextProfileIndex returns the index delta steps from idx in a ring of n names,
// wrapping both ways. Go's % keeps the sign, so normalize into [0,n).
func nextProfileIndex(n, idx, delta int) int {
	return ((idx+delta)%n + n) % n
}

// tuiConfig turns the loaded config into what the status screen displays.
func tuiConfig(cfg config.Config, demo bool, logs *tui.LogBuffer, configPath string) tui.Config {
	return tui.Config{
		Profile:      cfg.Profile,
		Bindings:     bindingTokens(cfg.Bindings),
		LeftButtons:  zwift.LeftButtons,
		RightButtons: zwift.RightButtons,
		Focus:        cfg.FocusProgramNamePrefixOnClick,
		StartProgram: cfg.StartProgram,
		NoMapping:    cfg.Missing,
		Demo:         demo,
		Logs:         logs,
		ConfigPath:   filepath.Base(configPath),
		Version:      version,
		OpenConfig:   openConfigFile(configPath),
	}
}

// solverFor hands the unlock client to BLE only when a usable session is
// loaded. A nil solver keeps the old empty-ack behaviour: with no sign-in,
// nothing we can send renews the LEFT pod's unlock anyway.
func solverFor(c *unlock.Client) ble.ChallengeSolver {
	if c == nil || !c.Ready() {
		return nil
	}
	return c
}

// loginClosure builds the Login callback: sign in with the injected login
// function, map unlock.ErrAuth to "wrong email or password", and on success
// call onSignedIn — main wires it to ble.EndSessionsForSide(zwift.PodLeft),
// so the LEFT pod's live session is recycled straight away: it tears down,
// clears its sighting, waits for a fresh advertisement, and reconnects with
// the fresh-token solver.
// The injected function is c.Login in production; tests inject a stub so the
// ErrAuth mapping and the onSignedIn arming are unit-tested without the
// network. login must be non-nil.
func loginClosure(login func(ctx context.Context, email, password string) error, onSignedIn func()) func(email, password string) error {
	return func(email, password string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := login(ctx, email, password)
		if errors.Is(err, unlock.ErrAuth) {
			return errors.New("wrong email or password")
		}
		if err == nil && onSignedIn != nil {
			onSignedIn()
		}
		return err
	}
}

// tuiAuth builds the status-screen sign-in config. There is no start-up gate:
// the screen opens immediately and "z" opens the form when Login is wired.
// A stored session starts silently (AuthNone, no notice) and its solver
// answers the next challenge with the server-signed reply; no token shows the
// honest notice; an unreadable session shows its own notice.
// Login signs in with c.Login, maps unlock.ErrAuth to "wrong email or
// password", and on success calls onSignedIn — main wires it to
// ble.EndSessionsForSide(zwift.PodLeft), so the LEFT pod's live session is
// recycled straight away: it tears down, clears its sighting, waits for a
// fresh advertisement, and reconnects with the fresh-token solver.
// Login is nil when c is nil: "sign-in not available" per the AuthConfig
// contract. Never call c.Login on a nil client.
func tuiAuth(c *unlock.Client, loadErr error, onSignedIn func()) tui.AuthConfig {
	auth := tui.AuthConfig{}
	if c != nil {
		auth.Login = loginClosure(func(ctx context.Context, email, password string) error {
			return c.Login(ctx, email, password)
		}, onSignedIn)
	}
	switch {
	case loadErr == nil:
		// A stored session exists: nothing to show on screen. The next
		// challenge is answered with the stored session's signed reply.
		return auth
	case errors.Is(loadErr, unlock.ErrNoToken):
		// No token is not a failure: the LEFT may still be unlocked from the
		// last sign-in. Start BLE with the empty ack instead of blocking the
		// RIGHT pod behind a form, and point at the "z" shortcut.
		auth.State = tui.AuthNone
		auth.Notice = "The LEFT controller works only while its current unlock lasts (a few days)."
	default:
		auth.State = tui.AuthNone
		auth.Notice = "The stored Zwift session could not be read — press z to sign in again."
	}
	return auth
}

// runTUI runs the screen until the user quits, then exits the process (BLE
// sessions are process-scoped; there is no separate teardown path). start, if
// non-nil, runs in its own goroutine with the program for sending messages.
func runTUI(tcfg tui.Config, start func(p *tea.Program)) {
	p := tea.NewProgram(tui.New(tcfg))
	if start != nil {
		go start(p)
	}
	if _, err := p.Run(); err != nil {
		slog.Error("tui", "error", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// liveFeed is the TUI counterpart of main's -plain startup: the same Enable
// retry (every 5s while Bluetooth is off), then the same startListening. It
// only OBSERVES: the screen reads ble.Connected / ble.SideSeenRecently and the
// OnKey hook, and nothing in a session waits on it. The solver is re-read per
// session attempt (solver() at each connect), so a runtime sign-in's fresh
// token is in force for the next LEFT-pod connection.
func liveFeed(p *tea.Program, state *profileState, km *ble.KeyMap, solver func() ble.ChallengeSolver) {
	// Update check: fire once, never block or warn. A failure (offline,
	// rate-limited, dev stamp) is simply "no update".
	go func() {
		latest, err := update.Latest(context.Background())
		if err != nil {
			slog.Debug("update check failed", "error", err)
			return
		}
		if update.Newer(version, latest) {
			p.Send(tui.UpdateMsg{Latest: latest})
		}
	}()

	for enabled := false; !enabled; {
		ble.Guarded("enable BLE adapter", func() {
			if err := ble.Enable(); err != nil {
				slog.Error("Bluetooth is OFF or unavailable — turn it ON (Windows: Settings > Bluetooth & devices); retrying in 5s", "error", err)
			} else {
				enabled = true
			}
		})
		if !enabled {
			time.Sleep(5 * time.Second)
		}
	}
	slog.Info("Bluetooth is on — switch on BOTH Click controllers (LEFT and RIGHT); both are connected and kept alive together")
	// current() not cfg: a startup config recovery may have replaced the
	// profile while Bluetooth was still off.
	launchProfile(state.current())
	p.Send(tui.BTMsg(true))

	// Set before any session exists (no race). Send blocks until the program
	// loop takes the message, and the hooks run on BLE notification /
	// session-goroutines, so hand it off: a busy screen must never stall
	// button handling.
	ble.OnKey = func(button string, down bool) {
		go p.Send(tui.KeyStateMsg{Button: button, Down: down})
	}
	ble.OnLifted = func(vk uint16) {
		idx := state.vkIdx.Load()
		if idx == nil {
			return
		}
		for _, name := range (*idx)[vk] {
			n := name
			go p.Send(tui.KeyStateMsg{Button: n, Down: false})
		}
	}

	startListening(km, scanFor, ble.ReconnectWindow, solver)
	// The "detected" window is the reconnect window: the screen and the scan
	// gate must agree on what "recently seen" means.
	src := tui.Sources{
		BT:        ble.ScanHealthy,
		Connected: ble.Connected,
		Seen:      func(p zwift.Pod) bool { return ble.SideSeenRecently(p, ble.ReconnectWindow) },
	}
	// Wired unconditionally: a runtime switch can turn focus on, so the alert
	// must be able to appear for the rest of the session. EnumWindows + process
	// lookups are heavier than the other reads: cache for a second instead of
	// running them on every 250ms poll.
	var at time.Time
	var last bool
	src.Focus = func() bool {
		if time.Since(at) > time.Second {
			last, at = keys.TargetWindowPresent(), time.Now()
		}
		return last
	}
	tui.Poll(context.Background(), p.Send, 250*time.Millisecond, src)
}

// editorCommand picks a text editor launcher per OS. Windows gets notepad:
// .yaml usually has no file association, so "start" would pop an "open with"
// dialog instead of an editor.
func editorCommand(goos, path string) *exec.Cmd {
	switch goos {
	case "windows":
		return exec.Command("notepad.exe", path)
	case "darwin":
		return exec.Command("open", "-t", path)
	default:
		return exec.Command("xdg-open", path)
	}
}

// startEditor launches the editor without waiting for it (it is a separate
// window); injectable for tests.
var startEditor = func(path string) error {
	cmd := editorCommand(runtime.GOOS, path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap the child, ignore its exit status
	return nil
}

// openConfigFile returns the TUI's "e" action for the config file at path.
func openConfigFile(path string) func() error {
	return func() error {
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		return startEditor(abs)
	}
}

// vkIndex maps a VK back to every button mapped to it: ReleaseAll force-lifts
// by VK (the button name is not in hand), so the UI needs the reverse lookup.
func vkIndex(b map[string]keys.Binding) map[uint16][]string {
	m := map[uint16][]string{}
	for name, bnd := range b {
		m[bnd.VK] = append(m[bnd.VK], name)
	}
	return m
}
