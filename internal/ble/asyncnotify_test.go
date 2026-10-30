package ble

import (
	"bytes"
	"testing"
	"time"
)

func TestAsyncNotifyPassesChallengeAndFrames(t *testing.T) {
	// Only the 0xFF 03 crypto challenge is passed to the session's challenge
	// handler (which writes either ff 04 00 or the signed reply). Every frame,
	// including 0x23 buttons and the routine 0xFF 05 status, still reaches the
	// decode handler unchanged; no frame ends a session. Every non-empty frame
	// also counts as pod activity (the idle-reset gate).
	challenges, got, handled, activity := 0, []byte(nil), 0, 0
	h := asyncNotify(func(f []byte) { challenges++; got = f }, func() { activity++ }, func([]byte) { handled++ })

	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}
	challenge := []byte{0xFF, 0x03, 0x00, 0x0A, 0x21, 0x02, 0xAB}
	status := []byte{0xFF, 0x05, 0x00, 0xFA, 0x05, 0x18} // battery/status, routine
	heartbeat := []byte{0x19, 0x10, 0x5A}

	h(idle)
	h(challenge)
	h(status)
	h(heartbeat)

	if challenges != 1 {
		t.Fatalf("challenges=%d, want 1 (only the 0xFF 03 challenge)", challenges)
	}
	if !bytes.Equal(got, challenge) {
		t.Fatalf("challenge frame = % X, want % X", got, challenge)
	}
	if handled != 4 {
		t.Fatalf("handled=%d, want 4 (every frame reaches the decode handler)", handled)
	}
	if activity != 4 {
		t.Fatalf("activity=%d, want 4 (every non-empty frame counts as pod activity)", activity)
	}
}

func TestAsyncNotifySkipsEmpty(t *testing.T) {
	challenges, handled, activity := 0, 0, 0
	h := asyncNotify(func([]byte) { challenges++ }, func() { activity++ }, func([]byte) { handled++ })

	h(nil)
	if handled != 0 || challenges != 0 || activity != 0 {
		t.Fatalf("empty frame handled=%d challenges=%d activity=%d, want 0/0/0", handled, challenges, activity)
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
	if keepaliveTick(false, active, now) {
		t.Fatal("active session: want a liveness ping, not a reset")
	}
	idle := now.Add(-56 * time.Second)
	if !keepaliveTick(false, idle, now) {
		t.Fatal("idle pod: want a reset")
	}
	if keepaliveTick(true, idle, now) {
		t.Fatal("after the reset was sent: want a liveness ping again")
	}

	IdleReset = 0
	if keepaliveTick(false, idle, now) {
		t.Fatal("IdleReset=0: want a liveness ping, reset path disabled")
	}
}

func TestPostResetStale(t *testing.T) {
	// After the scheduled 0x18 pod reset the session must end itself when the
	// pod emits NO frame within the grace window: a pod whose app dies while
	// its BLE stack still ACKs writes would otherwise be a zombie forever —
	// writes "succeed", no frame ever arrives, no session end, no reconnect.
	// Pre-reset idle silence must NEVER end a session (hard-won fact), and a
	// single frame after the reset (challenge/status/button) proves the pod
	// revived unless the grace expires first.
	saved := PostResetGrace
	defer func() { PostResetGrace = saved }()
	PostResetGrace = 15 * time.Second

	resetAt := time.Unix(1_000_000, 0)
	e := resetAt.Add(-100 * time.Second)  // stale from long before the reset
	frame := resetAt.Add(2 * time.Second) // a revival frame two seconds in

	// Never reset armed: idle silence never ends a session.
	if postResetStale(time.Time{}, e, resetAt.Add(60*time.Second)) {
		t.Fatal("no reset armed: want a liveness ping, never a recycle")
	}
	// Within the grace window: wait for the pod to reboot and speak.
	if postResetStale(resetAt, e, resetAt.Add(14*time.Second)) {
		t.Fatal("within the grace window: want the session to wait")
	}
	// Grace elapsed with no frame since the reset: the pod is a zombie — recycle.
	if !postResetStale(resetAt, e, resetAt.Add(15*time.Second)) {
		t.Fatal("silent past the grace: want the session recycled")
	}
	// One frame after the reset: the pod revived, keep the session.
	if postResetStale(resetAt, frame, resetAt.Add(60*time.Second)) {
		t.Fatal("frame arrived after the reset: keep the session")
	}

	// PostResetGrace=0 disables the recycle entirely (liveness-only behavior).
	PostResetGrace = 0
	if postResetStale(resetAt, e, resetAt.Add(time.Hour)) {
		t.Fatal("PostResetGrace=0: recycle disabled, want a liveness ping")
	}
}

func TestAckDue(t *testing.T) {
	// The LEFT pod's unlock latch expires ~30s after the last ff 04 00 ack
	// (seen 2026-10-21: button stream died exactly 30.03s after ack #3), and
	// the pod sends only ~2 challenges at connect, so challenge-driven acking
	// stops and the pod's button engine locks while its status/heartbeat/echo
	// frames keep flowing. The session therefore re-sends ff 04 00 every
	// AckInterval ON OUR OWN CLOCK, anchored at the last ack actually sent
	// (unlock echo, challenge re-arm, or a prior periodic ack). Never acked
	// (no anchor) or AckInterval=0: nothing periodic.
	saved := AckInterval
	defer func() { AckInterval = saved }()

	anchor := time.Unix(1_000_000, 0)

	AckInterval = 15 * time.Second
	if ackDue(time.Time{}, anchor) {
		t.Fatal("never acked (no anchor): want no periodic ack")
	}
	if ackDue(anchor, anchor.Add(14*time.Second)) {
		t.Fatal("14s since the last ack: not due yet")
	}
	if !ackDue(anchor, anchor.Add(15*time.Second)) {
		t.Fatal("15s since the last ack: due")
	}

	AckInterval = 0
	if ackDue(anchor, anchor.Add(time.Hour)) {
		t.Fatal("AckInterval=0: periodic ack disabled")
	}
}

func TestLivenessReportDue(t *testing.T) {
	// The liveness report is an Info-level breadcrumb that makes an Info-only
	// log answer the question a Debug log would: how stale are the session's
	// received frames right now. It is due every 60s per session, never
	// before the first interval has passed.
	now := time.Unix(1_000_000, 0)
	if livenessReportDue(now, now, time.Minute) {
		t.Fatal("fresh session: no report yet")
	}
	if livenessReportDue(now.Add(-59*time.Second), now, time.Minute) {
		t.Fatal("59s since the last report: not due yet")
	}
	if !livenessReportDue(now.Add(-60*time.Second), now, time.Minute) {
		t.Fatal("60s since the last report: due")
	}
}
