package ble

import (
	"testing"
	"time"
)

func TestAsyncNotifyRearmsOnChallengeAndPassesFrames(t *testing.T) {
	// Only the 0xFF 03 crypto challenge re-arms the pod (ff 04 00). Every
	// frame, including 0x23 buttons and the routine 0xFF 05 status, still
	// reaches the decode handler unchanged; no frame ends a session. Every
	// non-empty frame also counts as pod activity (the idle-reset gate).
	rearmed, handled, activity := 0, 0, 0
	h := asyncNotify("zwift/00:00", func() { rearmed++ }, func() { activity++ }, func([]byte) { handled++ })

	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}
	challenge := []byte{0xFF, 0x03, 0x00, 0x0A, 0x21, 0x02, 0xAB}
	status := []byte{0xFF, 0x05, 0x00, 0xFA, 0x05, 0x18} // battery/status, routine
	heartbeat := []byte{0x19, 0x10, 0x5A}

	h(idle)
	h(challenge)
	h(status)
	h(heartbeat)

	if rearmed != 1 {
		t.Fatalf("rearmed=%d, want 1 (only the 0xFF 03 challenge re-arms)", rearmed)
	}
	if handled != 4 {
		t.Fatalf("handled=%d, want 4 (every frame reaches the decode handler)", handled)
	}
	if activity != 4 {
		t.Fatalf("activity=%d, want 4 (every non-empty frame counts as pod activity)", activity)
	}
}

func TestAsyncNotifySkipsEmpty(t *testing.T) {
	rearmed, handled, activity := 0, 0, 0
	h := asyncNotify("zwift/00:00", func() { rearmed++ }, func() { activity++ }, func([]byte) { handled++ })

	h(nil)
	if handled != 0 || rearmed != 0 || activity != 0 {
		t.Fatalf("empty frame handled=%d rearmed=%d activity=%d, want 0/0/0", handled, rearmed, activity)
	}
}

func TestKeepalivePayloads(t *testing.T) {
	// The keepalive re-sends the raw "RideOn" opcode frame to sync-rx. The
	// activation trio still uses "RideOn" 02 03 / 00 08 00 / 00 08 10,
	// 00 08 10 doubles as the teardown probe, and the idle pod-reset is a
	// lone 0x18 (Opcode.RESET=24).
	if got := string(rideOn); got != "RideOn" {
		t.Fatalf("rideOn=% X (%q), want 52 69 64 65 4F 6E", rideOn, got)
	}
	if got := string(handshakeStart); got != "RideOn\x02\x03" {
		t.Fatalf("handshakeStart=% X (%q), want 52 69 64 65 4F 6E 02 03", handshakeStart, got)
	}
	if len(keep) != 3 || keep[0] != 0x00 || keep[1] != 0x08 || keep[2] != 0x10 {
		t.Fatalf("keep=% X, want 00 08 10", keep)
	}
	if len(resetPod) != 1 || resetPod[0] != 0x18 {
		t.Fatalf("resetPod=% X, want 18", resetPod)
	}
}

func TestKeepaliveTick(t *testing.T) {
	// Active session: liveness pings only. Idle past IdleReset: one reset
	// (ping suppressed), then liveness resumes until the reset is actually
	// sent. IdleReset=0 disables the reset path entirely.
	saved := IdleReset
	defer func() { IdleReset = saved }()
	now := time.Unix(1_000_000, 0)

	IdleReset = 55 * time.Second
	active := now.Add(-1 * time.Second)
	if reset, ping := keepaliveTick(false, active, now); reset || !ping {
		t.Fatalf("active session: got reset=%v ping=%v, want false/true", reset, ping)
	}
	idle := now.Add(-56 * time.Second)
	if reset, ping := keepaliveTick(false, idle, now); !reset || ping {
		t.Fatalf("idle pod: got reset=%v ping=%v, want true/false", reset, ping)
	}
	if reset, ping := keepaliveTick(true, idle, now); reset || !ping {
		t.Fatalf("after reset sent: got reset=%v ping=%v, want false/true", reset, ping)
	}

	IdleReset = 0
	if reset, ping := keepaliveTick(false, idle, now); reset || !ping {
		t.Fatalf("IdleReset=0: got reset=%v ping=%v, want false/true", reset, ping)
	}
}
