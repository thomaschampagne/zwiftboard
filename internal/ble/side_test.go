package ble

import (
	"testing"
	"time"

	"zwiftboard/internal/zwift"
)

// SideSeenRecently is the full pair's gate: initially no side is seen, a fresh
// markSeenSide registers only that side, and a sighting ages out of the window.
// The clock is injected (nowFn) so expiry is deterministic with no sleeps.
func TestSideSeenRecently(t *testing.T) {
	seenMu.Lock()
	sideSeenAt = map[zwift.Pod]time.Time{}
	seenMu.Unlock()

	fake := time.Unix(1_000_000, 0)
	defer func(orig func() time.Time) { nowFn = orig }(nowFn)
	nowFn = func() time.Time { return fake }

	if SideSeenRecently(zwift.PodLeft, 30*time.Second) {
		t.Fatal("unseen left pod must not pass the gate")
	}
	markSeenSide(zwift.PodLeft)
	if !SideSeenRecently(zwift.PodLeft, 30*time.Second) {
		t.Fatal("fresh left sighting must pass the gate")
	}
	if SideSeenRecently(zwift.PodRight, 30*time.Second) {
		t.Fatal("right pod not seen must never pass the gate")
	}
	fake = fake.Add(30*time.Second + time.Millisecond)
	if SideSeenRecently(zwift.PodLeft, 30*time.Second) {
		t.Fatal("aged left sighting must close the gate")
	}
	if SideSeenRecently(zwift.PodUnknown, 30*time.Second) {
		t.Fatal("unknown pod must never pass the gate")
	}
	markSeenSide(zwift.PodLeft) // re-sighting re-opens the gate even after expiry
	if !SideSeenRecently(zwift.PodLeft, 30*time.Second) {
		t.Fatal("re-sighting after expiry must pass the gate")
	}
}

// The scan callback decodes each Zwift controller's manufacturer record; the
// side it reports must feed the side-seen gate exactly like the per-address
// sighting feeds the reconnect gate.
func TestScanRecordsPodSide(t *testing.T) {
	seenMu.Lock()
	sideSeenAt = map[zwift.Pod]time.Time{}
	seenMu.Unlock()

	stubScan(t, func(cb scanCallback) error {
		cb(nil, zwiftPodResult("D4:06:0F:A9:86:04", zwift.PodLeftDeviceID))
		cb(nil, zwiftPodResult("D4:06:0F:A9:86:05", zwift.PodRightDeviceID))
		return nil
	})
	if _, _, err := scanBurst(time.Second, func(string) bool { return false }); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !SideSeenRecently(zwift.PodLeft, time.Minute) {
		t.Fatal("left pod sighting not recorded by the scan callback")
	}
	if !SideSeenRecently(zwift.PodRight, time.Minute) {
		t.Fatal("right pod sighting not recorded by the scan callback")
	}
}

// Discovered targets carry their pod side so the caller can manage the RIGHT
// pod and leave the LEFT anchor unconnected.
func TestScanBurstTargetCarriesSide(t *testing.T) {
	stubScan(t, func(cb scanCallback) error {
		cb(nil, zwiftPodResult("D4:06:0F:A9:86:04", zwift.PodLeftDeviceID))
		cb(nil, zwiftPodResult("D4:06:0F:A9:86:05", zwift.PodRightDeviceID))
		return nil
	})
	pending, _, err := scanBurst(time.Second, func(string) bool { return false })
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	got := map[zwift.Pod]int{}
	for _, tg := range pending {
		got[tg.Side]++
	}
	if got[zwift.PodLeft] != 1 || got[zwift.PodRight] != 1 {
		t.Fatalf("sides = %v, want one left and one right", got)
	}
}

// Connected state is tracked per pod side so the caller can report "ready"
// once BOTH pods hold a live session. It is observational only: nothing is
// gated on it (a connected pod may stop advertising).
func TestConnectedTracking(t *testing.T) {
	connMu.Lock()
	connected = map[zwift.Pod]int{}
	connMu.Unlock()

	if Connected(zwift.PodLeft) || Connected(zwift.PodRight) {
		t.Fatal("nothing connected yet")
	}
	markConnected(zwift.PodRight, true)
	if !Connected(zwift.PodRight) || Connected(zwift.PodLeft) {
		t.Fatal("only right must be connected")
	}
	markConnected(zwift.PodLeft, true)
	markConnected(zwift.PodRight, false)
	if Connected(zwift.PodRight) || !Connected(zwift.PodLeft) {
		t.Fatal("right ended, left still connected")
	}
	markConnected(zwift.PodRight, false) // spurious extra end must not go negative
	markConnected(zwift.PodRight, true)
	if !Connected(zwift.PodRight) {
		t.Fatal("reconnect after spurious end must register")
	}
}
