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

func TestLockSuspect(t *testing.T) {
	// A healthy, unlocked pod challenges exactly ONCE per session
	// (2026-10-25: one challenge, then 5.5 minutes of button frames); the
	// locked runs of 2026-10-21/22 challenged TWICE ~15s apart before the
	// engine died while status frames kept flowing. LockChallenges=0
	// disables the net, like every other interval constant.
	saved := LockChallenges
	defer func() { LockChallenges = saved }()

	LockChallenges = 2
	for _, n := range []int64{0, 1} {
		if lockSuspect(n) {
			t.Fatalf("lockSuspect(%d)=true, want false (healthy pod: one challenge)", n)
		}
	}
	if !lockSuspect(2) || !lockSuspect(5) {
		t.Fatal("lockSuspect(>=2) must be true (the locked pods re-challenged)")
	}
	LockChallenges = 0
	if lockSuspect(2) {
		t.Fatal("LockChallenges=0 must disable the net")
	}
}

func TestResetTick(t *testing.T) {
	// The NET fires on the challenge count ALONE — even with fresh activity,
	// because a locked pod streams status frames every ~3s while its button
	// engine is dead (2026-10-21/22), so the idle clock never expires for it.
	// Both paths are one-shot per session via resetSent, and IdleReset=0 must
	// not disable the net.
	savedIdle, savedLock := IdleReset, LockChallenges
	defer func() { IdleReset, LockChallenges = savedIdle, savedLock }()
	now := time.Unix(1_000_000, 0)
	IdleReset, LockChallenges = 55*time.Second, 2

	fresh := now.Add(-1 * time.Second) // the locked-pod signature: frames keep flowing
	if net, idle := resetTick(false, 1, fresh, now); net || idle {
		t.Fatalf("one challenge + fresh activity: net=%v idle=%v, want false/false (healthy pod)", net, idle)
	}
	if net, idle := resetTick(false, 2, fresh, now); !net || idle {
		t.Fatalf("two challenges + fresh activity: net=%v idle=%v, want true/false — the net must not depend on the idle clock", net, idle)
	}
	if net, _ := resetTick(true, 5, fresh, now); net {
		t.Fatal("resetSent must make the net one-shot per session")
	}
	idleOld := now.Add(-56 * time.Second)
	if net, idle := resetTick(false, 1, idleOld, now); net || !idle {
		t.Fatalf("healthy pod, idle past IdleReset: net=%v idle=%v, want false/true", net, idle)
	}
	if net, idle := resetTick(false, 2, idleOld, now); !net || idle {
		t.Fatal("both conditions: the net must win over the idle path (the Info log depends on it)")
	}
	IdleReset = 0
	if _, idle := resetTick(false, 1, idleOld, now); idle {
		t.Fatal("IdleReset=0 must disable the idle path")
	}
	if net, _ := resetTick(false, 2, fresh, now); !net {
		t.Fatal("IdleReset=0 must NOT disable the net")
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
	// The periodic ack ladder is kept from the proven-stable build; it does
	// NOT renew the unlock (disproven 2026-10-22: gaps <=18s still lost the
	// engine on a locked pod). ackDue is pure clock arithmetic: anchored at
	// the last WRITTEN ack, AckInterval later, 0/never disables.
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
