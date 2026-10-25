# Left-pod lock reset net + unlock facts correction — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reboot a pod that re-challenges mid-session (`0x18`, evidence-triggered) so a factory-locked LEFT pod keeps working without any Zwift app, and correct the falsified "~24h unlock" facts everywhere they are recorded.

**Architecture:** A per-session challenge counter (atomic) feeds a pure tick-decision function (`resetTick`) in the existing 3s keepalive loop; the net shares the loop's `resetSent`/`resetAt` state so exactly one reset fires per session and `postResetStale` recycling + the scan-gated reconnect work unchanged. Facts fixes are comment/doc edits only.

**Tech Stack:** Go (module `zwiftboard`), `log/slog`, `sync/atomic`, existing test conventions in `internal/ble`.

**Spec:** `docs/superpowers/specs/2026-10-25-left-lock-reset-net-design.md` (read it first — it carries the evidence: one challenge/session = healthy, two = locked precursor).

## Global Constraints

- Logging is `log/slog` only — no `log.Printf`, no `fmt.Println`.
- `internal/ble` loop/`Session` correctness is REVIEWED, not tested (no hardware here); all new *logic* must be extracted as pure functions and unit-tested.
- Keep every protocol-fact comment when moving/editing code; comments explain _why_.
- Conventional commits, one logical step per commit.
- Interval-style constants follow the "0 disables" convention (`IdleReset`, `PostResetGrace`, `AckInterval`).
- Never gate anything on the TUI; never end a session on pre-reset silence (hard-won facts in AGENTS.md).
- Full gate before claiming done: `just sanity` (gofmt, `go test -race ./...`, vet, Windows vet, Windows build).

---

### Task 1: Lock-suspect decision logic (pure, TDD)

**Files:**
- Modify: `internal/ble/ble.go` (add `LockChallenges` to the var block; add `lockSuspect` + `resetTick` next to `keepaliveTick`/`postResetStale`)
- Test: `internal/ble/asyncnotify_test.go` (add `TestLockSuspect`, `TestResetTick` after `TestKeepaliveTick`)

**Interfaces:**
- Consumes: existing `keepaliveTick(resetSent bool, lastActivity, now time.Time) bool` and the var-block constants style ("0 disables").
- Produces: `LockChallenges` (const, `2`), `lockSuspect(challenges int64) bool`, `resetTick(resetSent bool, challenges int64, lastActivity, now time.Time) (net, idle bool)`. Task 2 wires exactly these names.

- [ ] **Step 1: Write the failing tests**

In `internal/ble/asyncnotify_test.go`, immediately after `TestKeepaliveTick`, add:

```go
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
	IdleReset = 0
	if _, idle := resetTick(false, 1, idleOld, now); idle {
		t.Fatal("IdleReset=0 must disable the idle path")
	}
	if net, _ := resetTick(false, 2, fresh, now); !net {
		t.Fatal("IdleReset=0 must NOT disable the net")
	}
}
```

- [ ] **Step 2: Run the tests, verify they fail to compile**

Run: `go test ./internal/ble/ -run 'TestLockSuspect|TestResetTick' -v`
Expected: FAIL — `undefined: LockChallenges` (the functions don't exist yet).

- [ ] **Step 3: Implement the minimal logic**

In `internal/ble/ble.go`, add to the var block directly AFTER the `AckInterval = 15 * time.Second` line:

```go

	// LockChallenges is the per-session 0xFF-challenge count at which a pod is
	// considered LOCKED (button engine dead): a healthy, unlocked pod sends
	// exactly ONE challenge at connect (2026-10-25: one challenge, then 5.5
	// minutes of button frames), while the locked runs of 2026-10-21/22 sent
	// TWO ~15s apart before the engine died ~45-65s after the echo. A locked
	// pod keeps streaming status frames, so no activity clock sees the death —
	// the second challenge is the only visible precursor, and it arms the
	// lock-reset net (resetTick). 0 disables the net.
	LockChallenges = 2
```

Then add the two functions directly AFTER `postResetStale` (before the `ackDue` comment block):

```go
// lockSuspect reports that a session's challenge count marks a locked pod —
// see LockChallenges.
func lockSuspect(challenges int64) bool {
	return LockChallenges > 0 && challenges >= LockChallenges
}

// resetTick decides ONE keepalive-tick action: the lock-reset NET fires when
// the pod has re-challenged past LockChallenges in this session and the
// session has not reset yet; the IDLE path fires per keepaliveTick after
// IdleReset of silence. The net check runs FIRST and ignores activity on
// purpose: a locked pod streams status frames every ~3s (activity!) while its
// button engine is dead, which is exactly why the idle path never fires for
// it — the second challenge is the only visible precursor. Both paths share
// the caller's resetSent so exactly ONE reset fires per session no matter
// which armed it; postResetStale then recycles a session left silent after
// the reset and the scan-gated caller reconnects for a fresh handshake.
func resetTick(resetSent bool, challenges int64, lastActivity, now time.Time) (net, idle bool) {
	if lockSuspect(challenges) && !resetSent {
		return true, false
	}
	return false, keepaliveTick(resetSent, lastActivity, now)
}
```

- [ ] **Step 4: Run the tests, verify they pass**

Run: `go test ./internal/ble/ -run 'TestLockSuspect|TestResetTick' -v`
Expected: PASS (both).

- [ ] **Step 5: Package gate + commit**

Run: `gofmt -w internal/ble && go test -race ./internal/ble/`
Expected: ok.

```bash
git add internal/ble/ble.go internal/ble/asyncnotify_test.go
git commit -m "feat: pure lock-suspect decision for the per-session challenge count"
```

---

### Task 2: Wire the counter and the net into the session

**Files:**
- Modify: `internal/ble/ble.go` — `Session` only: the `challengeRearm` closure, the keepalive loop's reset block, the liveness breadcrumb, and a session-start anchor.

**Interfaces:**
- Consumes: `LockChallenges`, `lockSuspect`, `resetTick` (Task 1); `asyncNotify(label, rearm, activity, bh)` UNCHANGED (it calls `rearm()` exactly once per `FF 03` challenge — that call is the counting hook).
- Produces: nothing new exported; behavior only. The liveness log line gains a `challenges` field.

- [ ] **Step 1: Count challenges in the rearm closure**

Replace (inside `Session`, after the `ackAnchor` closure):

```go
	challengeRearm := func() {
		if !SendAck {
			return
		}
		if _, err := writeChar(syncRx, ackSeq); err != nil {
			slog.Debug("challenge re-arm write failed", "controller", t.Label, "error", err)
			return
		}
		anchorAck()
	}
```

with:

```go
	// challenges counts THIS session's 0xFF crypto challenges (asyncNotify
	// calls rearm once per challenge). A healthy, unlocked pod challenges
	// exactly once at connect (2026-10-25: one challenge, then 5.5 minutes of
	// button frames); the locked runs of 2026-10-21/22 challenged twice ~15s
	// apart before the engine died while every other frame kept flowing. The
	// counting deliberately precedes the SendAck check: a challenge is
	// evidence whether or not we ack it.
	var challenges atomic.Int64
	challengeRearm := func() {
		if n := challenges.Add(1); n > 1 {
			slog.Info("crypto challenge again in session — pod unlock suspect",
				"controller", t.Label, "challenge", n)
		}
		if !SendAck {
			return
		}
		if _, err := writeChar(syncRx, ackSeq); err != nil {
			slog.Debug("challenge re-arm write failed", "controller", t.Label, "error", err)
			return
		}
		anchorAck()
	}
```

- [ ] **Step 2: Add the session-start anchor**

Replace:

```go
	resetSent := false
	var resetAt time.Time    // armed when the scheduled 0x18 reset was sent
	lastReport := time.Now() // liveness breadcrumb anchor (session start)
```

with:

```go
	resetSent := false
	var resetAt time.Time   // armed when the scheduled 0x18 reset was sent
	started := time.Now()   // session-age anchor for the lock-net log
	lastReport := started   // liveness breadcrumb anchor (session start)
```

- [ ] **Step 3: Route the tick through resetTick**

Replace the idle-reset block inside the `for range tick.C` loop:

```go
		if keepaliveTick(resetSent, activity.last(), now) {
			resetSent = true
			resetAt = now
			slog.Debug("idle — sending pod reset (0x18) to reboot before the ~65s sleep watchdog", "controller", t.Label, "idle", IdleReset)
			if _, err := writeChar(syncRx, resetPod); err != nil {
				return fmt.Errorf("reset: %w", err)
			}
			continue
		}
```

with:

```go
		if net, idle := resetTick(resetSent, challenges.Load(), activity.last(), now); net || idle {
			resetSent = true
			resetAt = now
			if net {
				slog.Info("pod re-challenged — unlock suspect; sending pod reset (0x18)",
					"controller", t.Label, "challenges", challenges.Load(),
					"sessionAge", now.Sub(started).Round(time.Second))
			} else {
				slog.Debug("idle — sending pod reset (0x18) to reboot before the ~65s sleep watchdog", "controller", t.Label, "idle", IdleReset)
			}
			if _, err := writeChar(syncRx, resetPod); err != nil {
				return fmt.Errorf("reset: %w", err)
			}
			continue
		}
```

- [ ] **Step 4: Add the challenge count to the liveness breadcrumb**

In the `slog.Info("session liveness", ...)` call, add one field after `"resetStale"`:

```go
				"resetStale", postResetStale(resetAt, activity.last(), now),
				"challenges", challenges.Load())
```

(the original ends with `"resetStale", postResetStale(resetAt, activity.last(), now))` — swap the closing paren for a comma and append the field).

- [ ] **Step 5: Verify build + all tests + full sanity gate**

Run: `gofmt -w internal/ble && go test -race ./... && just sanity`
Expected: all packages ok; vet clean; Windows build ok. (`asyncNotify` and its tests are untouched — they must pass as-is.)

Loop correctness itself is REVIEWED, not tested (no hardware here): the reviewer must confirm the net block sits BEFORE the postResetStale/ack/ping blocks and shares `resetSent`/`resetAt` exactly as the idle path did.

- [ ] **Step 6: Commit**

```bash
git add internal/ble/ble.go
git commit -m "feat: reboot a pod that re-challenges mid-session (lock-reset net)"
```

---

### Task 3: Facts correction (docs + comments only, no behavior)

**Files:**
- Modify: `AGENTS.md` (the LEFT unlock bullet)
- Modify: `internal/zwift/protocol.go:14-15` (package comment)
- Modify: `internal/ble/ble.go` (AckInterval comment, ackDue comment, challengeRearm comment block)
- Modify: `README.md:25`

**Interfaces:**
- Consumes: the corrected model from the spec (persistence timeline, never-unlocked vs expired, 85-byte signed challenge, EC-point prefix, reset net).
- Produces: documentation only.

- [ ] **Step 1: Rewrite the AGENTS.md unlock bullet**

Replace the bullet beginning `- LEFT controller needs the ~24h unlock from the Zwift app` (through `...otherwise-healthy frames.`) with:

```markdown
- LEFT controller unlock: ONE Zwift-app handshake GRANTS a persistent unlock
  (the bare `ff 04 00` only keeps an already-unlocked pod happy). NOT a ~24h
  lease: one unlock (2026-10-22) still held through the 2026-10-24 (~morning)
  and 2026-10-25 (11:54) sessions, the second after ~26h of complete non-use
  — the "~24h" figure was folklore (clickbridge: multi-day rides with no app;
  BikeControl #68: one app session fixed it permanently for several users).
  The 2026-10-21/22 lockouts were a NEVER-unlocked pod (the first-ever unlock
  came AFTER those failures), not an expired one. A LOCKED pod's engine dies
  ~45-65s after the unlock echo while status `2A`/battery `FF 05`/heartbeat
  `19 10 50`/RideOn echo frames keep flowing — invisible to any
  frame-activity clock, writes still succeed, nothing logs; the lock is
  permanent for the session (status frames keep the idle clock fresh, so the
  scheduled `0x18` reset never fires). The lock PRECURSOR is the challenge
  count: a healthy pod sends exactly ONE `0xFF` challenge per session, the
  locked runs sent TWO ~15s apart — so the lock-reset NET reboots the pod
  (`0x18`) on the second challenge of a session (`resetTick`/
  `LockChallenges`): the pod reboots on our schedule, reconnects scan-gated,
  and a factory-locked pod keeps working in windows without any app
  (BikeControl's restart-every-minute, evidence-triggered instead of blind).
  The `AckInterval` (15s) ladder does NOT renew the unlock (disproven
  2026-10-22: gaps ≤18s still lost the engine; Run A's "last-ack + 30.03s"
  was coincidence). The challenge frame is an 85-byte SIGNED message:
  `FF 03 00` + protobuf {field1: 33-byte SEC1 compressed EC point, field2:
  varint 33751040, field3: 40-byte signature} — the old "03→02 countdown" was
  a misread of the EC point prefix across different challenges (the old docs
  quoted 6-byte prefixes of full 85-byte frames). Open: whether the unlock
  survives battery removal or a `0x18` reboot (never exercised on an
  unlocked pod); re-run the Zwift-app unlock only if a pod factory-locks.
```

- [ ] **Step 2: Fix the protocol.go package comment**

Replace:

```go
//   - LEFT needs a ~24h hardware unlock done by the Zwift app; "ff 04 00" keeps an
//     already-unlocked device unlocked
```

with:

```go
//   - LEFT ships LOCKED; ONE Zwift-app handshake grants a persistent unlock
//     (2026-10-25: one unlock held 3 days through a ~26h non-use gap — "~24h"
//     is folklore); "ff 04 00" only keeps an already-unlocked device happy.
//     A locked pod re-challenges (2 challenges/session vs 1 healthy) and the
//     session reboots it with a lone 0x18 so it works in windows — see
//     ble.resetTick / ble.LockChallenges
```

- [ ] **Step 3: Fix the ble.go comments that assert disproven mechanics**

3a. Replace the `AckInterval` comment's middle sentences (keep the first sentence and the last two):

Old middle:
```go
	// interval, anchored at the last ack actually written (unlock echo,
	// challenge re-arm, or a prior periodic ack). The LEFT pod's unlock latch
	// expires ~30s after the last ack (seen 2026-10-21: the button stream died
	// exactly 30.03s after ack #3), and the pod sends only ~2 challenges at
	// connect — challenge-driven acking alone lets the latch expire while the
	// pod's status/heartbeat/echo frames keep flowing, so the death is
	// invisible to any frame-activity clock and looks like a zombie (writes
	// still succeed, nothing logs). A failed ack write is a REAL disconnect
```

New middle:
```go
	// interval, anchored at the last ack actually written (unlock echo,
	// challenge re-arm, or a prior periodic ack). The ladder is kept from the
	// proven-stable build; it does NOT renew the unlock (disproven
	// 2026-10-22: gaps ≤18s still lost the engine on a locked pod) and
	// whether an unlocked pod needs it at all is untested (2026-10-25 held
	// with the ladder active). A failed ack write is a REAL disconnect
```

3b. Replace the `ackDue` comment's last sentence:

Old:
```go
// AckInterval has elapsed since the last ack was actually WRITTEN (a zero
// lastAck — never acked — and AckInterval=0 both disable the periodic ack).
// See AckInterval for why the latch must be fed on our own clock.
```

New:
```go
// AckInterval has elapsed since the last ack was actually WRITTEN (a zero
// lastAck — never acked — and AckInterval=0 both disable the periodic ack).
// See AckInterval for why the ladder is kept from the proven-stable build.
```

3c. Replace the `challengeRearm` comment block:

Old:
```go
	// challengeRearm answers the 0xFF crypto challenge that arrives on async
	// (not sync-tx): a short ff 04 00 ack keeps an already-unlocked pod's button
	// stream from going silent.
	//
	// ackAnchorN holds the UnixNano of the last SUCCESSFUL ff 04 00 write
	// (0 = never) — the anchor for the periodic AckInterval ack, without
	// which the LEFT pod's ~30s unlock latch expires mid-session and locks
	// the button engine while every other frame keeps flowing (see
	// AckInterval). The anchor is written from the notification goroutines
```

New:
```go
	// challengeRearm answers the 0xFF crypto challenge that arrives on async
	// (not sync-tx): a short ff 04 00 ack keeps an already-unlocked pod's button
	// stream from going silent, and the call also counts the session's
	// challenges for the lock-reset net (a second challenge in one session is
	// the only visible precursor of a locked pod — see resetTick).
	//
	// ackAnchorN holds the UnixNano of the last SUCCESSFUL ff 04 00 write
	// (0 = never) — the anchor for the periodic AckInterval ack (kept from
	// the proven-stable build; it does not renew the unlock, see
	// AckInterval). The anchor is written from the notification goroutines
```

- [ ] **Step 4: Fix the README claim**

Replace:

```markdown
- **No tricks** - no Zwift account, no 24h controller unlock. Works out of the box.
```

with:

```markdown
- **No tricks** - no Zwift account, no subscription. Unlock the pair once with
  the Zwift app; the unlock persists for days (not a 24h lease). Even a
  factory-locked LEFT pod keeps working: zwiftboard reboots it automatically
  when it re-challenges.
```

- [ ] **Step 5: Verify + commit**

Run: `just sanity`
Expected: green (comments only, but gofmt/vet run over them).

```bash
git add AGENTS.md README.md internal/zwift/protocol.go internal/ble/ble.go
git commit -m "docs: correct the unlock facts (persistent, not ~24h) and describe the lock-reset net"
```

---

### Task 4: Final verification

**Files:** none (verification only).

- [ ] **Step 1: Full local gate**

Run: `just sanity`
Expected: gofmt clean, `go test -race ./...` all ok, vet clean (native + Windows), Windows exe builds.

- [ ] **Step 2: CI check**

Run: `just ci`
Expected: green.

- [ ] **Step 3: Report evidence**

Paste the tail of both runs (the `ok ...` lines and the build line) into the completion report — evidence over claim.

---

## Self-review notes (filled at plan time)

- Spec coverage: net trigger (T1+T2), one-reset-per-session shared state (T1 test + T2 wiring), recycling unchanged (no code touched on that path), breadcrumb (T2 step 4), facts (T3), parked sign-in branch (untouched), README (T3 step 4), open experiments live in the spec, not code. ✅
- No placeholders; every code step has full code. ✅
- Names consistent: `LockChallenges`, `lockSuspect`, `resetTick(resetSent bool, challenges int64, lastActivity, now time.Time) (net, idle bool)` used identically in T1/T2. ✅
