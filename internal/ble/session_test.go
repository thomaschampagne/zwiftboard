package ble

import (
	"errors"
	"testing"
	"time"
)

func TestEndSessionSkipsDisconnectWhenProbeFails(t *testing.T) {
	probes, disconnects := 0, 0
	endSession("t", func() error { probes++; return errors.New("device gone") },
		func() { disconnects++ })
	if probes != 1 || disconnects != 0 {
		t.Fatalf("probes=%d disconnects=%d, want 1/0", probes, disconnects)
	}
}

func TestEndSessionDisconnectsWhenProbeOK(t *testing.T) {
	disconnects := 0
	endSession("t", func() error { return nil }, func() { disconnects++ })
	if disconnects != 1 {
		t.Fatalf("disconnects=%d, want 1", disconnects)
	}
}

// TestSeenRecently verifies the reconnect gate: a sighting within the window
// passes, an aged sighting and an unknown address don't. The clock is injected
// (nowFn) so expiry is deterministic with no sleeps.
func TestSeenRecently(t *testing.T) {
	seenMu.Lock()
	lastSeenAt = map[string]time.Time{}
	seenMu.Unlock()

	fake := time.Unix(1_000_000, 0)
	defer func(orig func() time.Time) { nowFn = orig }(nowFn)
	nowFn = func() time.Time { return fake }

	const key = "D4:06:0F:A9:86:04"
	markSeen(key)

	if !SeenRecently(key, 30*time.Second) {
		t.Fatal("fresh sighting must pass the gate")
	}
	fake = fake.Add(30*time.Second + time.Millisecond)
	if SeenRecently(key, 30*time.Second) {
		t.Fatal("aged sighting must close the gate")
	}
	if SeenRecently("AA:00:00:00:00:00", 30*time.Second) {
		t.Fatal("unknown address must never pass the gate")
	}
	markSeen(key) // re-sighting re-opens the gate even after expiry
	if !SeenRecently(key, 30*time.Second) {
		t.Fatal("re-sighting after expiry must pass the gate")
	}
	ClearSighting(key) // a session end invalidates the sighting
	if SeenRecently(key, 30*time.Second) {
		t.Fatal("cleared sighting must close the gate")
	}
}
