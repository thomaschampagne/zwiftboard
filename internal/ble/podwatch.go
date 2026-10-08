package ble

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"zwiftboard/internal/zwift"
)

// podAction is what a controller's silence watchdog wants the session to do.
type podAction int

const (
	// actNone keeps listening.
	actNone podAction = iota
	// actRearm pokes the controller in place (fresh activation + ack) without
	// dropping the link, so the RIGHT pod stays untouched.
	actRearm
	// actReconnect ends the session so the caller reconnects — a fresh
	// connection re-runs the handshake and the pod re-enters its healthy
	// button-streaming window.
	actReconnect
)

// Silence thresholds, driven by the 2s keepalive ticker. A user pressing
// nothing is not silent: the pods stream all-released 0x23 frames every
// ~100ms, so a multi-second gap is an unambiguous fault.
const (
	silenceRearmAfter     = 5 * time.Second
	silenceReconnectAfter = 15 * time.Second
	silenceRearmGap       = 8 * time.Second
)

// podWatcher tracks the last 0x23 button frame seen from one connected
// controller. The Click V2 LEFT pod can STOP sending button frames while the
// BLE link stays up (Zwift's crypto watchdog when the ~24h hardware unlock
// lapses): it still answers keepalives, so without this a session would never
// notice and that pod's keys would stay dead until the app restarts. decide,
// driven by the keepalive ticker, escalates silence: re-arm in place first,
// then drop the session for a reconnect. Timing is injected so the policy is
// testable without hardware (same DI pattern as ButtonHandler's tap param).
type podWatcher struct {
	mu         sync.Mutex
	lastButton time.Time // the connect time until a button frame arrives
	lastRearm  time.Time
}

func newPodWatcher(connect time.Time) *podWatcher {
	return &podWatcher{lastButton: connect}
}

// sawButton records a button frame, resetting the silence timer.
func (w *podWatcher) sawButton(now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastButton = now
}

// decide maps the current silence length to an action: actNone while the
// stream is healthy, actRearm once silence passes rearmAfter (spaced out by
// minRearmGap so we do not spam the device), actReconnect once silence passes
// reconnectAfter.
func (w *podWatcher) decide(now time.Time, rearmAfter, reconnectAfter, minRearmGap time.Duration) podAction {
	w.mu.Lock()
	defer w.mu.Unlock()
	silence := now.Sub(w.lastButton)
	switch {
	case silence < rearmAfter:
		return actNone
	case silence >= reconnectAfter:
		return actReconnect
	case now.Sub(w.lastRearm) >= minRearmGap:
		w.lastRearm = now
		return actRearm
	default:
		return actNone
	}
}

// asyncNotify wraps one controller's async (keypad) notification stream —
// the characteristic that carries 0x23 button frames AND the Zwift 0xFF crypto
// challenge (delivered here, NOT on sync-tx). 0x23 frames feed the silence
// watchdog and go to bh; 0xFF vendor frames trigger a re-arm; every other
// frame still reaches bh unchanged for decode/logging. clk is injectable so
// tests can assert the watchdog feed without sleeping.
func asyncNotify(clk func() time.Time, w *podWatcher, rearm func(), bh func([]byte)) func([]byte) {
	return func(b []byte) {
		defer recoverLog("async frame")
		if len(b) == 0 {
			return
		}
		switch b[0] {
		case zwift.MsgKeyPad:
			w.sawButton(clk())
		case 0xFF:
			slog.Warn("vendor/challenge frame from click — re-arming (button stream may otherwise go silent)", "frame", fmt.Sprintf("% X", b))
			rearm()
		}
		bh(b)
	}
}
