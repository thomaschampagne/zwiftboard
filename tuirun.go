package main

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zwiftboard/internal/ble"
	"zwiftboard/internal/config"
	"zwiftboard/internal/keys"
	"zwiftboard/internal/tui"
)

// rightButtons are the Click V2 right pod's buttons, the only ones normally
// mapped; they form the key table's rows.
var rightButtons = []string{"Y", "Z", "A", "B", "PLUS"}

// tuiConfig turns the loaded config into what the status screen displays.
func tuiConfig(cfg config.Config, demo bool, logs *tui.LogBuffer, configPath string) tui.Config {
	b := map[string]string{}
	for name, bnd := range cfg.Bindings {
		b[name] = bnd.Token
	}
	return tui.Config{
		Profile:    cfg.Profile,
		Bindings:   b,
		Buttons:    rightButtons,
		Focus:      cfg.FocusProgramNamePrefixOnClick,
		NoMapping:  cfg.Missing,
		Demo:       demo,
		Logs:       logs,
		ConfigPath: filepath.Base(configPath),
		OpenConfig: openConfigFile(configPath),
	}
}

// runTUI runs the screen until the user quits, then exits the process (BLE
// sessions are process-scoped; there is no separate teardown path). start, if
// non-nil, runs in its own goroutine with the program for sending messages.
func runTUI(tcfg tui.Config, start func(p *tea.Program)) {
	p := tea.NewProgram(tui.New(tcfg), tea.WithAltScreen(), tea.WithMouseCellMotion())
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
// OnKey hook, and nothing in a session waits on it.
func liveFeed(p *tea.Program, cfg config.Config) {
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
	p.Send(tui.BTMsg(true))

	// Set before any session exists (no race). Send blocks until the program
	// loop takes the message, and the hooks run on BLE notification /
	// session-goroutines, so hand it off: a busy screen must never stall
	// button handling.
	ble.OnKey = func(button string, down bool) {
		go p.Send(tui.KeyStateMsg{Button: button, Down: down})
	}
	idx := vkIndex(cfg.Bindings)
	ble.OnLifted = func(vk uint16) {
		for _, name := range idx[vk] {
			n := name
			go p.Send(tui.KeyStateMsg{Button: n, Down: false})
		}
	}

	startListening(cfg, &scanFor, &reconnect)
	src := tui.Sources{BT: ble.ScanHealthy, Connected: ble.Connected, Seen: ble.SideSeenRecently}
	if cfg.FocusProgramNamePrefixOnClick != "" {
		// EnumWindows + process lookups are heavier than the other reads: cache
		// for a second instead of running them on every 250ms poll.
		var at time.Time
		var last bool
		src.Focus = func() bool {
			if time.Since(at) > time.Second {
				last, at = keys.TargetWindowPresent(), time.Now()
			}
			return last
		}
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
