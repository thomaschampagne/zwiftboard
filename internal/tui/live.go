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

// Poll sends the Bluetooth state and the pod states immediately and then
// whenever either changes, until ctx is done. btOK is ble.ScanHealthy: a failing
// scan burst is how a switched-off radio shows up after start-up.
func Poll(ctx context.Context, send func(tea.Msg), every time.Duration, btOK func() bool, connected func(zwift.Pod) bool, seen func(zwift.Pod, time.Duration) bool) {
	t := time.NewTicker(every)
	defer t.Stop()
	var last *PodMsg
	var lastBT *bool
	for {
		if bt := btOK(); lastBT == nil || *lastBT != bt {
			lastBT = &bt
			send(BTMsg(bt))
		}
		cur := PodStates(connected, seen)
		if last == nil || *last != cur {
			c := cur
			last = &c
			send(cur)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
