package ble

import (
	"testing"
	"time"
)

func TestPodWatcherSilenceRearmThenReconnect(t *testing.T) {
	start := time.Now()
	w := newPodWatcher(start)

	rearmAfter, reconnectAfter, gap := 5*time.Second, 15*time.Second, 8*time.Second

	if a := w.decide(start.Add(4*time.Second), rearmAfter, reconnectAfter, gap); a != actNone {
		t.Fatalf("at 4s action=%v, want none", a)
	}
	if a := w.decide(start.Add(6*time.Second), rearmAfter, reconnectAfter, gap); a != actRearm {
		t.Fatalf("at 6s action=%v, want rearm", a)
	}
	// Second re-arm attempt is gated by minRearmGap until 14s.
	if a := w.decide(start.Add(10*time.Second), rearmAfter, reconnectAfter, gap); a != actNone {
		t.Fatalf("at 10s action=%v, want none (inside rearm gap)", a)
	}
	if a := w.decide(start.Add(14*time.Second), rearmAfter, reconnectAfter, gap); a != actRearm {
		t.Fatalf("at 14s action=%v, want rearm", a)
	}
	if a := w.decide(start.Add(16*time.Second), rearmAfter, reconnectAfter, gap); a != actReconnect {
		t.Fatalf("at 16s action=%v, want reconnect", a)
	}
}

func TestPodWatcherButtonResetsSilence(t *testing.T) {
	start := time.Now()
	w := newPodWatcher(start)

	rearmAfter, reconnectAfter, gap := 5*time.Second, 15*time.Second, 8*time.Second

	w.sawButton(start.Add(10 * time.Second))
	if a := w.decide(start.Add(11*time.Second), rearmAfter, reconnectAfter, gap); a != actNone {
		t.Fatalf("action=%v, want none after a button frame", a)
	}
	if a := w.decide(start.Add(26*time.Second), rearmAfter, reconnectAfter, gap); a != actReconnect {
		t.Fatalf("action=%v, want reconnect when silence resumes", a)
	}
}

func TestPodWatcherNeverSeenEscalatesToReconnect(t *testing.T) {
	// A pod that never sends a button frame is treated as silent from connect.
	w := newPodWatcher(time.Now())
	rearmAfter, reconnectAfter, gap := 5*time.Second, 15*time.Second, 8*time.Second

	w.decide(time.Now().Add(6*time.Second), rearmAfter, reconnectAfter, gap)
	if a := w.decide(time.Now().Add(16*time.Second), rearmAfter, reconnectAfter, gap); a != actReconnect {
		t.Fatalf("action=%v, want reconnect", a)
	}
}

func TestAsyncNotifyFeedsWatchdogAndRearmsOnChallenge(t *testing.T) {
	now := time.Now()
	w := newPodWatcher(now.Add(-time.Hour)) // stale until a 0x23 frame arrives

	rearmed, handled := 0, 0
	var received [][]byte
	h := asyncNotify(func() time.Time { return now }, w, func() { rearmed++ },
		func(b []byte) { handled++; received = append(received, b) })

	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}
	challenge := []byte{0xFF, 0x03, 0x00, 0x0A, 0x21, 0x02, 0xAB}
	status := []byte{0x19, 0x10, 0x5A}

	h(idle)
	if handled != 1 {
		t.Fatalf("0x23 frames must still reach the decode handler, handled=%d", handled)
	}
	if a := w.decide(now.Add(4*time.Second), 5*time.Second, 15*time.Second, 8*time.Second); a != actNone {
		t.Fatalf("0x23 frame should feed the watchdog, action=%v", a)
	}
	if rearmed != 0 {
		t.Fatalf("0x23 frame must not re-arm, rearmed=%d", rearmed)
	}

	h(challenge)
	if handled != 2 {
		t.Fatalf("0xFF frame must still reach the decode handler, handled=%d", handled)
	}
	if rearmed != 1 {
		t.Fatalf("0xFF challenge should re-arm once, rearmed=%d", rearmed)
	}

	h(status)
	if handled != 3 {
		t.Fatalf("status frame must pass through, handled=%d", handled)
	}
	if rearmed != 1 {
		t.Fatalf("status frame must not re-arm, rearmed=%d", rearmed)
	}
	if len(received) != 3 {
		t.Fatalf("decode handler saw %d frames, want 3", len(received))
	}
}

func TestAsyncNotifySkipsEmpty(t *testing.T) {
	now := time.Now()
	w := newPodWatcher(now)
	rearmed, handled := 0, 0
	h := asyncNotify(func() time.Time { return now }, w, func() { rearmed++ }, func([]byte) { handled++ })

	h(nil)
	if handled != 0 || rearmed != 0 {
		t.Fatalf("empty frame handled=%d rearmed=%d, want 0/0", handled, rearmed)
	}
}
