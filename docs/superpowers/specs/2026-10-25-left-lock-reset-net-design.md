# Left-pod lock reset net + unlock facts correction — Design

**Date:** 2026-10-25
**Status:** approved in chat (brainstorming)
**Branch:** `feat/left-lock-reset-net` (worktree), merge target
`feat/left-controller-support`

## Intent and why

Two parts, one commit-series each:

1. **Correct the unlock facts.** The runs of 2026-10-24 (~morning, both
   controllers, pre-sign-in code) and 2026-10-25 (11:54, logged) falsify the
   recorded "~24h unlock": ONE Zwift-app unlock (2026-10-22) still held
   through both sessions, the second after ~26h of complete non-use. The
   2026-10-21/22 lockouts were a NEVER-unlocked pod (the first-ever unlock
   came after those failures), not an "expired unlock". The challenge decode
   was also wrong: the frame is an 85-byte signed protobuf message, and the
   `03`/`02` byte after `0A 21` is the SEC1 compressed-EC-point prefix
   (prefix + 32-byte X coordinate) of *different ephemeral keys* — not a
   countdown. Ecosystem corroboration: clickbridge (multi-day rides, no
   Zwift app installed), BikeControl issue #68 (one app session fixed it
   permanently for several users), BikeControl TROUBLESHOOTING ("connect it
   in the Zwift app once"). The "~24h" figure is folklore.

2. **Reset net for a locked LEFT pod.** A locked pod is invisible in the
   frame stream: status/battery/heartbeat frames keep flowing while the
   button engine is dead, so no activity clock can see it and the existing
   idle reset NEVER fires (the lock is "permanent for the session" —
   AGENTS.md). The ecosystem's mitigation (BikeControl 6.0.0: "the left
   controller needs a quick restart every minute"; clickbridge's RESET
   handling) is a pod RESET (`0x18`): the pod reboots and its engine works
   again for a window. Our trigger is evidence, not a timer: **the challenge
   count within one session**.

## Evidence

Healthy session (2026-10-25, LEFT = `zwift/86:04`, main checkout's
`zwiftboard.log`):

- exactly ONE challenge at connect (11:54:42), full 85-byte frame,
  empty-acked (`ff 04 00`);
- buttons streamed 11:54:41 → 12:00:16 (203 `0x23` frames; the last press is
  ~5.5 min AFTER the challenge);
- status `FF 05 00 FA 05 18 …` every ~3s the whole session (117×); zero
  resets; both pods echoed every keepalive.

Locked sessions (2026-10-21/22, latch-experiment doc): TWO challenges at
connect (+4.15s and +19.6s — two different EC point prefixes), engine dead
~45–65s after the echo while status frames kept flowing.

⇒ **a second challenge in one session is the observable lock precursor**; a
healthy pod challenges exactly once. False-positive cost (resetting a healthy
pod): one ~5–10s reconnect hiccup, fully logged. False-negative cost (no net):
the buttons stay dead for the whole session. The trade favors the net.

clickbridge's `STOPPED_PREFIXES` heuristic (reset on any
`FF 05 00 EA 05`/`FA 05` frame) is NOT adopted: our healthy LEFT pod sends
`FA 05` every 3s while working (117× in 7 min), so that heuristic would
reset-loop constantly. Its *policy* (reset on lock) is right; its *signal* is
wrong for our firmware.

## Goals

1. A pod that re-challenges mid-session is rebooted on our schedule (`0x18`)
   and reconnects, so a factory-locked pod keeps working in windows without
   any Zwift app — BikeControl's restart-every-minute behavior, but
   evidence-triggered instead of blind.
2. A healthy, unlocked pod's behavior is unchanged: one challenge → ack →
   stream; the net never fires.
3. The next real lockout produces the evidence we lack today (challenge
   index and timing per session) at Info level, not buried in Debug.
4. AGENTS.md, protocol comments and README state the corrected facts.

## Non-goals

- No d-lock/API/sign-in work: branch
  `feat/left-controller-support-zwift-sign-in` (35c4d7e) stays parked as the
  optional fallback for unlocking a pod without the app.
- No blind periodic reset option (deferred until evidence asks for it).
- No change to challenge handling: the empty `ff 04 00` ack stays (proven
  today on an unlocked pod).
- No config surface: the threshold is a constant (YAGNI until a user needs
  to tune it).

## Design

Session-scoped challenge counter + net check in the keepalive loop
(`internal/ble/ble.go`):

- `challengeCount` (atomic int64) bumps on every `FF 03` challenge. The
  `rearm` closure in `Session` already runs once per challenge, so the
  counting lives there; `asyncNotify` and its tests stay unchanged.
- `lockSuspect(count) bool`: `count >= LockChallengeThreshold` (2).
- Keepalive tick order: the NET check runs FIRST, before the idle path, and
  is independent of activity — a locked pod streams status frames every ~3s,
  which is exactly why the idle path never fires for it. If suspect and the
  session has not reset yet: Info log ("pod re-challenged — unlock suspect;
  sending pod reset (0x18)" with controller, challenge count, session age),
  write `resetPod`, set `resetSent`/`resetAt`, continue. `postResetStale`
  then recycles the session exactly as today; the scan gate reconnects; the
  new session starts with count 0. A still-locked pod therefore loops:
  challenge ×2 → reset → works a window → challenge ×2 → reset —
  BikeControl/clickbridge behavior with a smarter trigger.
- `resetSent`/`resetAt` are shared with the idle path, so exactly ONE reset
  fires per session regardless of which path armed it.
- Logging: challenge #1 stays Debug. Challenge #N≥2 logs Info
  ("crypto challenge #N in session — pod unlock suspect"). The liveness
  breadcrumb gains a `challenges` field. With this, the next real lockout
  yields the EA/FA-05 cadence (Debug frames already there) plus challenge
  timing (Info) — the discriminating data for future threshold tuning.

Threshold 2 with no time window: the only observed healthy pattern is one
challenge; a hypothetical mid-session lease-renewal re-challenge would cost
one logged hiccup and teach us something. A time window is YAGNI.

### Facts correction (docs only, no behavior)

- AGENTS.md unlock bullet: rewritten — persistence timeline (one unlock held
  2026-10-22 → 2026-10-25 through a ~26h non-use gap), never-unlocked vs
  expired, the 85-byte signed challenge structure with the EC-point-prefix
  decode, the reset net, and that "~24h" was folklore.
- `internal/zwift/protocol.go` header comment: same correction (it still
  says "LEFT needs a ~24h hardware unlock").
- `internal/ble/ble.go` comments: the AckInterval comment claims the ladder
  feeds a "~30s unlock latch" that "expires mid-session" — disproven on
  2026-10-22 (gaps ≤18s still lost the engine on a locked pod) and moot on an
  unlocked pod (untested whether the ladder matters at all). Keep the
  behavior (harmless, part of the proven-stable build), fix the rationale;
  same for the `challengeRearm` comment.
- README: "no 24h controller unlock" reworded to the corrected model
  (unlock once with the Zwift app; it persists for days).

## Test plan (TDD)

- `lockSuspect`: 0 and 1 challenges → false; 2 → true.
- Net-vs-idle ordering: with challenge count ≥2 AND fresh activity (the
  locked-pod signature), the tick writes `0x18` — proving the net does not
  depend on the idle clock.
- One reset per session: after the net fires, later ticks do not re-fire it.
- Recycling unchanged: a suspect session that goes silent for `PostResetGrace`
  after the net's reset still ends (existing `postResetStale` path).
- Liveness breadcrumb includes the challenge count.
- Existing asyncNotify tests pass unchanged (seam untouched).

## Risks / open

- If a healthy pod ever re-challenges (never observed), we cost one hiccup
  and log it. If a `0x18` reboot were to CLEAR a valid unlock (unknown: the
  unlock survived 3 days of pod sleeps, but a rebooted unlocked pod was
  never exercised), the net could cascade resets — the logs would show the
  2× challenge pattern returning after every reset, and we gate the net off.
  Accepted: reset-on-lock is the ecosystem's standard answer and our trigger
  is strictly more informed than a blind timer.
- Threshold evidence: two locked runs (2026-10-21/22, prefixes only — full
  frames lost with the old log) vs two healthy runs (2026-10-24 without log,
  2026-10-25 with log). The net's Info logging exists precisely to collect
  the missing evidence on the next real lockout.

## Open experiments (not blocking; data collection is automatic now)

- E1: idle a week+ with zero connections, then connect — bounds the unlock
  TTL from above.
- E2: battery pull / full power loss after an unlock — does the unlock
  survive a real reboot?
- E3: overnight CONNECTED session — does a live session re-arm anything?
- E4: next real lockout log — challenge timing + EA/FA-05 counts (automatic
  once this change ships).
