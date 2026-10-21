# LEFT Pod Unlock-Latch Experiment — Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to run
> this plan task-by-task (the hardware tasks are executed by the human partner on
> Windows; the analysis and doc tasks can be agent-executed). Steps use checkbox
> (`- [ ]`) syntax for tracking.

**Goal:** Determine whether the LEFT pod's silent button-engine lockout (~45–65s
after each connect's unlock echo) is caused by an **expired / unrenewed ~24h
Zwift unlock** — by repeating the failure repro after a FRESH unlock — and
record the concluded facts in `AGENTS.md`, correcting the (now disproven)
claim that a bare `ff 04 00` ack ladder feeds the latch.

**Architecture:** Zero code behavior changes. The experiment reuses the shipped
binary and its `-v` debug logging. One human-driven Windows test session, one
log-analysis pass against a fixed checklist, then one documentation-only task
whose exact text depends on which branch of the result was observed. Protocol
research (if the fresh unlock does NOT help) is explicitly **out of scope** —
it needs a separate `brainstorming` session per AGENTS.md.

**Tech Stack:** Zwift phone app (unlock), `zwiftboard` on Windows run with
`-v`, `rg` for log analysis (runs fine on Linux after the log is synced).

**Spec:** This is a bug-diagnosis plan, not a feature plan. The "spec" is the
evidence below plus the AGENTS.md "Hard-won facts" entry added by commit
`f43cf84` (which the experiment will confirm or correct).

## Global Constraints

- **No behavior changes.** Nothing in `internal/ble` is touched until a
  separate brainstorm produces an approved design. The only file edits in this
  plan are `AGENTS.md` and, on branch B, the `AckInterval` comment in
  `internal/ble/ble.go` — documentation only, no logic.
- **Evidence over claim:** no conclusion is written down without quoting the
  log numbers that support it (AGENTS.md + `verification-before-completion`).
- The current `AckInterval` code (commit `f43cf84`) is **not reverted** in
  either branch: it is harmless (3 bytes/15s), the failed `ff 04 00` write is
  still a valid liveness probe, and the keep-unlocked semantic for a *fresh*
  unlock remains untested (branch A decides).
- Conventional commit style, one logical step per commit.

## Evidence Base (from runs A and B — do not re-derive, compare against this)

- **Run A** (2026-10-21 23:35, no `AckInterval` code yet): LEFT session #2
  unlock echo at `23:35:35.16`, challenges at `+4.15s` and `+19.6s`, button
  stream died at `23:36:25.1` = last challenge ack **+30.03s** = echo **+49.7s**.
  No session ended, no reconnect logged, writes kept succeeding.
- **Run B** (2026-10-22 00:10–00:13, `AckInterval=15s` active, commit
  `f43cf84`): connect `00:10:50.4`, unlock echo ack `00:10:54.35`, challenges
  `00:10:59.10` (byte pattern `FF 03 00 0A 21 03`) and `00:11:14.52`
  (`FF 03 00 0A 21 02` — the byte after `21` DESCENDS 03→02), periodic acks at
  ~`00:11:30.35` and ~`00:11:48.35` (proven by `session liveness`
  `lastAck="6ss ago"` at `00:11:54.35`). Last LEFT button frame (mirror or own)
  **`00:11:34.95`**; at `00:11:59.65` the RIGHT pod streamed three `A` presses
  (`~20 × 0x23` on `zwift/10:21`) and **not one was mirrored to 86:04**.
  So the engine locked between 00:11:35 and 00:11:59 = interval **11.3–25s
  after a confirmed written ack**, i.e. the bare `ff 04 00` does NOT hold the
  engine, and the lockout timing (~echo+40.6…65.3s) matches Run A anyway.
- Both pods kept streaming status (`2A…`/`FF 05…`), heartbeat (`19 10 50`) and
  echo frames after the lockout — the lock is **invisible to any frame-activity
  clock and to Info-level logging** (the signature documented in AGENTS.md).
- The RIGHT pod (`zwift/10:21`) never challenges and never locks.

**Corroborating observation:** a fresh `0x23`-engine lockout ~45–65s after echo
correlates suspiciously well with the expired ~24h unlock state, because the
pod echoes `RideOn` (reads "unlocked") yet stops maintaining the unlock after
two unanswered-challenge cycles. Only a fresh app unlock can disprove the
blame — that is this experiment.

---

### Task 1: Fresh ~24h unlock via the Zwift app (human, Windows/phone)

**Files:** none (device state only).

- [ ] **Step 1: Unlock the Click pair in the official Zwift app**

Open the Zwift app on the phone, go through the Click pairing/connect flow
until the app reports the controllers connected. This performs the ~24h unlock
of the LEFT pod with the app's real crypto handshake. Note the wall-clock time
of the unlock (roughly is enough — write it next to the planned run below).

- [ ] **Step 2: Disconnect the app from the accessory**

Close (or background-and-forget) the Zwift app so the BLE connection is free.
Wake the pods by pressing a button on each.

### Task 2: Immediate repro attempt (human, Windows)

**Files:** create `zwiftboard.log` next to the app's working directory
(defaults there; `-log` overrides — do NOT override).

- [ ] **Step 1: Run zwiftboard with the notepad profile and debug logging,
      ideally within ~2 minutes of the fresh unlock**

```powershell
cd <repo checkout on Windows>
go run . -config config.yaml -p notepad -v
```

- [ ] **Step 2: Exercise the LEFT pod for 3+ minutes with a pause inside the
      window**

  1. From the moment the TUI shows both pods connected, press LEFT pod `DOWN`
     in Notepad — confirm `d` appears, keep typing with the LEFT pod for at
     least 3 minutes (the old failure was always ~45–65s after connect).
  2. At ~2.5 minutes: stop pressing for 30s, then press `DOWN` again. The
     30-then-press moment is the critical observation window
     (echo+45…65s).
  3. Also press RIGHT pod `B` once around minute 2 — its mirror frames on the
     LEFT pod are a second, clock-independent lockout probe.
  4. If `d` **stops** appearing, do NOT restart the app: keep pressing
     `DOWN` once every ~10s for another full minute so the log records the
     locked silence, then quit (`q`).
  5. If `d` keeps appearing past **echo + 70s** (i.e. the full window), quit
     (`q`).

- [ ] **Step 3: Sync the log into the repo**

Replace `zwiftboard.log` in the repo root with this run's file (overwrite is
fine — Run B's numbers are captured above). Do not commit it.

### Task 3: Log analysis against the checklist (agent or human, anywhere)

**Files:** read `zwiftboard.log` (repo root). Run all commands from the repo
root. All regexes below assume slog text lines with `msg=...`.

- [ ] **Step 1: Unlock echo and challenge cadence**

```bash
rg -n 'level=INFO msg="unlocked' zwiftboard.log
rg -n 'crypto challenge' zwiftboard.log | cut -c1-60
```

Record: echo time **E**; the number of challenges AND the byte after
`FF 03 00 0A 21` in each (`03` / `02` / a higher value past a fresh unlock?).

- [ ] **Step 2: First/last LEFT button frame and the ack ladder**

```bash
rg 'msg="raw frame" controller=zwift/86:04 frame="23' zwiftboard.log |
  awk '{print substr($1,6,16)}' | sed -n '1p;$p'
rg -n 'msg="session liveness" controller=zwift/86:04' zwiftboard.log
```

Record: first/last `0x23` timestamp; every `lastAck` value (must never exceed
~18s if the ladder still runs).

- [ ] **Step 3: Mirror probe (right pod burst never reaching the left pod)**

```bash
rg 'msg="raw frame" controller=zwift/(86:04|10:21) frame="23' zwiftboard.log |
  awk -F'T|\\+' '{print $2, $4}' | sort | awk '{info[$2]+=$1}'
```

(or simply read the lines around the RIGHT pod `B` press burst and check
whether `zwift/86:04` lines follow within ~1s.)

- [ ] **Step 4: Verdict**

  - **Verdict A — 24h unlock is the lockout mechanism** when the LEFT pod
    streams `0x23` frames PAST `echo + 70s` (in particular through and after
    the 30s pause), and ideally the challenge pattern changed (more than 2
    challenges, or the countdown byte starts higher than `03`).
  - **Verdict B — the latch is per-session and the 24h unlock is
    irrelevant** when the stream still dies in the `echo + 45–65s` window
    with the same silent lockout signature, regardless of the fresh unlock.
  - Optional middle case (died but later than +65s, or challenges behave
    differently): bring the numbers to the human partner before concluding.

### Task 4: Record the concluded facts (agent; commit after the verdict)

**Files:**
- Modify: `AGENTS.md` (the "LEFT controller needs the ~24h unlock…" bullet)
- Branch B only: Modify: `internal/ble/ble.go` — the `AckInterval` var
  comment ONLY (no code change)

**Interfaces:**
- Consumes: the verdict numbers from Task 3.
- Produces: corrected hard-won facts, so no future session re-derives this.

- [ ] **Step 1 (Verdict A): rewrite the unlock bullet with concrete text**

Replace the AGENTS.md "THE UNLOCK LATCH EXPIRES" bullet's final claim with:

```markdown
  The bare `ff 04 00` ack does NOT renew the latch (disproven 2026-10-22:
  an AckInterval ladder with gaps ≤18s still lost the button engine
  ~45–65s after the unlock echo), but with a FRESH ~24h unlock from the
  Zwift app the LEFT pod streams for the full test window. Operational
  rule: re-run the Zwift-app unlock when the LEFT pod stops typing after
  ~1 minute of otherwise-healthy frames.
```

Commit: `docs: confirm the expired ~24h unlock locks the LEFT pod's buttons`

- [ ] **Step 2 (Verdict B): rewrite the unlock bullet and the AckInterval
      comment, then STOP for a separate brainstorm**

Replace the AGENTS.md bullet's latch claim with:

```markdown
  The latch is PER-SESSION (disproven 2026-10-22 even against a fresh 24h
  unlock): ~45–65s after the unlock echo the LEFT pod's button engine locks
  silently, no matter the `ff 04 00` cadence. The `AckInterval` ack is kept
  only as a liveness probe. Escalate: protocol research needed before any
  further fix — a new `brainstorming` session is required by AGENTS.md.
```

And replace `internal/ble/ble.go`'s `AckInterval` var comment (lines ~76–85)
with:

```go
	// AckInterval re-sends the unlock ack (ff 04 00) every interval, anchored
	// at the last ack actually written. NOTE (2026-10-22): this does NOT hold
	// the LEFT pod's button engine — it locked ~45–65s after the unlock echo
	// with an ack ladder ≤18s apart (AGENTS.md). Kept solely because a failed
	// ack write is a valid liveness signal; removing it would also remove that
	// probe, so it stays (harmless, 3 bytes/15s).
	AckInterval = 15 * time.Second
```

Commit: `docs: record the LEFT latch is per-session, not the ~24h unlock`

- [ ] **Step 3 (both branches): verify docs-only change**

```bash
just sanity
```

Expected: all green with zero code-behavior diff (`git diff --stat` shows only
`AGENTS.md`, plus the comment-only `internal/ble/ble.go` on branch B).

## Self-Review

- Spec coverage: the experiment's purpose (discriminate 24h-unlock vs
  per-session latch) maps to Tasks 1–3; conclusion recording maps to Task 4 —
  both outcomes have exact replacement text. No gap.
- Placeholder scan: all inputs are concrete (commands, log regexes, exact doc
  text, exact commit messages). The one "human only" step (Task 1) is
  operational, not a placeholder.
- Type consistency: n/a (no code interfaces beyond the comment edit).

## Execution Handoff — chosen mode

The plan mixes human-only and agent-only tasks, so it runs **inline, in
order**: the human performs Tasks 1–2 on Windows, then either the human or the
agent executes Task 3 and the agent finishes Task 4. Resume is designed for a
fresh session: everything needed to interpret a new `zwiftboard.log` is in
this file's Evidence Base.
