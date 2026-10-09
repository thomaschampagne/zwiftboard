package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zwiftboard/internal/ble"
	"zwiftboard/internal/config"
	"zwiftboard/internal/tui"
)

// rightButtons are the Click V2 right pod's buttons, the only ones normally
// mapped; they form the key table's rows.
var rightButtons = []string{"Y", "Z", "A", "B", "PLUS"}

// tuiConfig turns the loaded config into what the status screen displays.
func tuiConfig(cfg config.Config, demo bool, logs *tui.LogBuffer) tui.Config {
	b := map[string]string{}
	for name, bnd := range cfg.Bindings {
		b[name] = bnd.Token
	}
	return tui.Config{
		Profile:   cfg.Profile,
		Bindings:  b,
		Buttons:   rightButtons,
		Focus:     cfg.FocusProgramNameOnClick,
		NoMapping: cfg.Missing,
		Demo:      demo,
		Logs:      logs,
	}
}

// runTUI runs the screen until the user quits, then exits the process (BLE
// sessions are process-scoped; there is no separate teardown path). start, if
// non-nil, runs in its own goroutine with the program for sending messages.
func runTUI(tcfg tui.Config, start func(p *tea.Program)) {
	p := tea.NewProgram(tui.New(tcfg), tea.WithAltScreen())
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
// OnTap hook, and nothing in a session waits on it.
func liveFeed(p *tea.Program, cfg config.Config, scanFor, reconnect *time.Duration, addrList *string) {
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
	// loop takes the message, and OnTap runs on the BLE notification goroutine,
	// so hand it off: a busy screen must never stall button handling.
	ble.OnTap = func(button string) { go p.Send(tui.ClickMsg(button)) }

	startListening(cfg, scanFor, reconnect, addrList)
	tui.Poll(context.Background(), p.Send, 250*time.Millisecond, ble.ScanHealthy, ble.Connected, ble.SideSeenRecently)
}
