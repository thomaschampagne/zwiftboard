package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"zwiftboard/internal/zwift"
)

// PodStates derives both pod states with the same precedence:
// connected, else seen advertising recently, else not detected. The lookups
// are injected (ble.Connected, and ble.SideSeenRecently pre-bound to
// ble.ReconnectWindow) so this stays testable and observational — the tui
// package never imports ble.
func PodStates(connected, seen func(zwift.Pod) bool) PodMsg {
	state := func(p zwift.Pod) PodState {
		switch {
		case connected(p):
			return PodConnected
		case seen(p):
			return PodDetected
		default:
			return PodOff
		}
	}
	return PodMsg{Left: state(zwift.PodLeft), Right: state(zwift.PodRight)}
}

// Sources are the read-only lookups Poll samples. Focus may be nil (no window
// target configured). Seen binds its sighting window at the call site so the
// TUI shares ble.ReconnectWindow.
type Sources struct {
	InputWarning func() string
	BT           func() bool // ble.ScanHealthy: a failing scan burst = radio switched off
	Connected    func(zwift.Pod) bool
	Seen         func(zwift.Pod) bool
	Focus        func() bool // configured focus window exists
}

// Poll sends Bluetooth, pod and focus-window state immediately and then
// whenever any of them changes, until ctx is done.
func Poll(ctx context.Context, send func(tea.Msg), every time.Duration, src Sources) {
	t := time.NewTicker(every)
	defer t.Stop()
	var (
		last        *PodMsg
		lastBT      *bool
		lastF       *bool
		lastWarning *string
	)
	for {
		if bt := src.BT(); lastBT == nil || *lastBT != bt {
			lastBT = &bt
			send(BTMsg(bt))
		}
		if src.InputWarning != nil {
			if warning := src.InputWarning(); lastWarning == nil || *lastWarning != warning {
				lastWarning = &warning
				send(InputWarningMsg(warning))
			}
		}
		if src.Focus != nil {
			if f := src.Focus(); lastF == nil || *lastF != f {
				lastF = &f
				send(FocusMsg(f))
			}
		}
		if cur := PodStates(src.Connected, src.Seen); last == nil || *last != cur {
			last = &cur
			send(cur)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
