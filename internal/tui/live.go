package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zwiftboard/internal/zwift"
)

// detectWindow matches main.go's pairStatus: a pod advertising within this
// window counts as "detected".
const detectWindow = 30 * time.Second

// PodStates derives both pod states with pairStatus's precedence:
// connected, else seen advertising recently, else not detected. The lookups
// are injected (ble.Connected / ble.SideSeenRecently) so this stays testable
// and observational.
func PodStates(connected func(zwift.Pod) bool, seen func(zwift.Pod, time.Duration) bool) PodMsg {
	state := func(p zwift.Pod) PodState {
		switch {
		case connected(p):
			return PodConnected
		case seen(p, detectWindow):
			return PodDetected
		default:
			return PodOff
		}
	}
	return PodMsg{Left: state(zwift.PodLeft), Right: state(zwift.PodRight)}
}

// Sources are the read-only lookups Poll samples. Focus may be nil (no window
// target configured).
type Sources struct {
	BT        func() bool // ble.ScanHealthy: a failing scan burst = radio switched off
	Connected func(zwift.Pod) bool
	Seen      func(zwift.Pod, time.Duration) bool
	Focus     func() bool // configured focus window exists
}

// Poll sends Bluetooth, pod and focus-window state immediately and then
// whenever any of them changes, until ctx is done.
func Poll(ctx context.Context, send func(tea.Msg), every time.Duration, src Sources) {
	t := time.NewTicker(every)
	defer t.Stop()
	var (
		last   *PodMsg
		lastBT *bool
		lastF  *bool
	)
	for {
		if bt := src.BT(); lastBT == nil || *lastBT != bt {
			lastBT = &bt
			send(BTMsg(bt))
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
