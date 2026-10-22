# LEFT Unlock Renewal — Debug Session Handoff (2026-10-23)

> **For agentic workers:** This is a **debug/handoff plan**, not a feature plan. The
> hardware steps are executed by the human partner on Windows; the analysis and
> documentation steps can be agent-executed. Steps use checkbox (`- [ ]`) tracking.
> Read `AGENTS.md` first — its "Hard-won facts" section is the ground truth this plan
> argues from. **No code changes happen in this plan**: per `AGENTS.md`, anything that
> changes behavior needs a `brainstorming` session and an approved design first.

**Goal:** Determine, on the first day the LEFT pod's app-granted ~24h unlock is
actually stale, whether `zwiftboard`'s one-time-sign-in path renews it for real — and
capture the evidence (status, timings, exchange hex) that decides the next design step
toward eventually dropping the Zwift API dependency.

**Architecture:** Observation only. The shipped binary already answers the pod's
`FF 03 00 <payload>` challenge by POSTing the payload to the d-lock service and writing
`ff 04 00 <reply>` back (commit `4b33b5c`), with the whole exchange at Debug level and
credentials redacted. This session runs the binary twice (once with no sign-in as a
stale-state baseline, once signed in), compares the LEFT pod's button-engine lifetime,
and records the verdict.

**Tech Stack:** Windows 10/11 + Bluetooth, Zwift Click V2 pair, Go toolchain, the
`notepad` profile, `rg`/`awk` for log analysis (works on Linux after the log is
synced), `zwiftboard.log` at Debug level.

**Spec:** `docs/superpowers/specs/2026-10-22-left-unlock-signin-design.md`.
**Predecessor plan:** `docs/superpowers/plans/2026-10-22-left-unlock-latch-experiment.md`.

## Global Constraints

- **Never paste or copy the token file.** `zwift-token.json` holds live credentials.
  Read only its `expires_at` field if you need it. The log is already redacted
  (`Authorization: <redacted>`) — still skim a log before sharing it.
- **TUI mode only for the sign-in runs.** The gate is TUI-only; `-plain` logs a warning
  and skips the prompt, so a `-plain` run can never test renewal. Plain mode is fine
  for the baseline (Esc is equivalent: no solver).
- **Do NOT have the Zwift app (or Companion) connected during a run.** Each pod accepts
  a single BLE connection.
- **Evidence over claim:** no verdict without the log numbers that support it. Quote
  timestamps and deltas.
- **One variable at a time:** the baseline run must have **no** solver (Esc, no token
  file); the renewal run must have a solver (signed in). Everything else identical
  (same machine, same pods, same profile, same distance from the pods).
- **Never pause longer than ~50s inside a measurement window.** After `IdleReset`
  (55s) of button silence the keepalive loops writes a lone `0x18` and the pod
  reboots, which invalidates the comparison. 30s is safe.
- **Conventional commits**, one logical step per commit; documentation-only in this
  plan's Phase 2.

## State at handoff (2026-10-22)

Branch `feat/left-controller-support`, working tree clean, **16 commits unpushed**
(upstream `origin/feat/left-controller-support` is at `4dcb322`). Feature commits:

```
c1ac085 docs: confirm the expired ~24h unlock locks the LEFT pod's buttons
505d817 docs: design and plan the Zwift sign-in LEFT-unlock flow
6fcc291 feat(unlock): persist the Zwift session beside the config
8febb2a feat(unlock): sign in to Zwift and refresh the session
d62c017 feat(unlock): answer the pod challenge via the d-lock service
5609c11 feat(ble): answer the pod challenge with the signed reply
2953986 feat(tui): ask for the Zwift sign-in that unlocks the LEFT controller
4b33b5c feat: gate Bluetooth on the Zwift sign-in and wire the solver
c15abe2 docs: describe the Zwift sign-in unlock path
143bbc8 style(tui): gofmt the sign-in view
f6b0d05 fix(tui): ignore sign-in results that arrive after the gate closed
d24f754 fix(unlock): keep the stored session on transient token-endpoint failures
7543dad fix(ble): bound challenge solves to their session
23d90a6 test(unlock): pin the 401 refresh budget and the pre-refresh path
6f1bb81 refactor: extract the BLE start gate and the session-check prompt
```

Verified on this tree (`just sanity`, all green): gofmt, `go test -race ./...`, vet on
Linux **and** Windows, Windows amd64 build. Unit-tested: token store, login/refresh
classification, d-lock call and redaction, solve queue, gate state machine, wiring.

**NOT verified (this session's job):**

| # | Question | Why it is open |
|---|----------|----------------|
| Q1 | Does the d-lock endpoint accept a plain Keycloak `Zwift Game Client` token (no extra headers, no device binding)? | Only a 403-without-auth was observed; no signed call has ever been made. |
| Q2 | Does the server-signed reply actually RENEW a stale ~24h unlock? | The reply path has never been exercised against a stale pod. |
| Q3 | Does the renewal survive a 24h gap and later reconnects? | Needs a T+24h re-check. |

Q1 can be answered **today**, even with a fresh unlock: the LEFT pod sends an `FF 03`
challenge at every connect (with a fresh unlock it is a single one, with no `03→02`
countdown), so the d-lock call is exercised either way — that is Task 0. Only Q2/Q3
need the stale state.

## Evidence base (do not re-derive; compare against this)

- **Route exists:** `POST https://us-or-rly101.zwift.com/api/d-lock-service/device/authenticate`
  with `Content-Type: application/x-protobuf-lite` answers **403 without an
  `Authorization` header**; body is the challenge payload.
- **Sign-in:** Keycloak `POST https://secure.zwift.com/auth/realms/zwift/protocol/openid-connect/token`,
  `grant_type=password|refresh_token`, `client_id=Zwift Game Client` (public client;
  other ids answer `invalid_client`).
- **Challenge frames seen:** `FF 03 00 0A 21 03` then `FF 03 00 0A 21 02` (the byte
  after `21` **descends**), arriving ~4s and ~19s after the connect echo. The payload
  is everything after `FF 03 00`; frames under 4 bytes are ignored.
- **A stale unlock locks the LEFT button engine silently ~45–65s after the connect
  echo**, while status (`2A…`, `FF 05…`), heartbeat (`19 10 50`) and `RideOn` echo
  frames keep flowing and writes keep succeeding. With a **fresh app unlock** the pod
  streamed **3m48s / 1894 frames** through an explicit 30s pause (2026-10-22).
- **A bare `ff 04 00` does not renew it:** an `AckInterval` ladder with gaps ≤18s still
  lost the engine (Run B, 2026-10-22). That is why the solver exists.
- **`RideOn` echo is NOT proof of a live engine** — it appears in locked runs too. Only
  LEFT-pod `0x23` button frames past the window count.
- The keepalive every 3s keeps the link alive; idle `0x23` silence is normal and never
  ends a session. Scheduled `0x18` reset at 55s of button silence.

---

## Phase 1 — Hardware session (human on Windows)

Run from a Windows checkout of this branch. `go run .` is fine; the TUI writes
`zwiftboard.log` in the **current directory**, truncated at startup.

### Task 0 (optional, can run any time): API acceptance smoke test

**Files:** none.

- [ ] **Step 1: Sign in and connect, look only for the d-lock exchange**

```powershell
cd <repo checkout on Windows>
go run . -p notepad -v
```

At the gate: type the Zwift email, `Tab`, password, `Enter`. Wait for the status
screen, wake both pods. No key-mapping assertion needed — watch the log instead.

- [ ] **Step 2: Inspect the exchange**

```powershell
Select-String -Path zwiftboard.log -Pattern 'crypto challenge|dlock request|dlock response|zwift sign-in'
```

Expected on success:

```
level=DEBUG msg="crypto challenge from click — solving for the server-signed unlock" controller=zwift/<side> frame="FF 03 00 …" payload="…"
level=DEBUG msg="dlock request" method=POST url=https://us-or-rly101.zwift.com/api/d-lock-service/device/authenticate body_len=… body="…" headers="Accept: application/x-protobuf-lite; Authorization: <redacted>; Content-Type: application/x-protobuf-lite"
level=DEBUG msg="dlock response" status=200 duration_ms=… body_len=… body="…" headers="…"
```

With a fresh app unlock, exactly **one** challenge (and one dlock pair) is normal; a
stale unlock typically shows the `03→02` pair. Absence of any challenge means the pod
considers itself unlocked and the API path was not exercised — note that and rely on
Tasks 1–2.

- [ ] **Step 3: Record Q1's answer**

`status=200` with a non-empty `body_len` ⇒ Q1 = **accepted**. `401` ⇒ token audience/
scope problem (refresh happens once automatically; a second 401 is logged as
`pod unlock solve failed`). `403` ⇒ likely needs more than a bearer token (note the
response `body`/`headers` hex; do **not** retry with credentials in a URL). `400` ⇒
body/encoding problem. Any of these is a valid, useful result — paste the redacted
lines into the handback (Task 5 bundle).

### Task 1: Stale baseline — reproduce the lock WITHOUT sign-in

Purpose: prove the ~24h unlock is actually stale and capture the reference timing on
today's machine. If the LEFT pod does **not** lock, the unlock is still fresh: stop and
reschedule (or run only Task 0).

- [ ] **Step 1: Park the stored session**

```powershell
$t = "$env:LOCALAPPDATA\zwiftboard\zwift-token.json"
if (Test-Path $t) { Move-Item $t "$t.bak" -Force }
```

(If `config.yml` was passed explicitly with `-config`, the token lives next to that
file instead.)

- [ ] **Step 2: Run with Esc at the gate**

```powershell
go run . -p notepad -v
```

Press `Esc` on the sign-in screen. Expected: the form closes and the status screen
shows the persistent "LEFT controller locked" notice. Wake both pods by pressing a
button on each; Notepad opens via `startProgram` (press `s` on the status screen if it
does not).

- [ ] **Step 3: Exercise the LEFT pod for 3+ minutes**

1. The moment both pods show connected, hold/press LEFT `DOWN` in Notepad until `d`
   appears, then keep typing `DOWN` every ~5–10s for 2.5 minutes.
2. At ~2.5 minutes stop for **30s**, then press `DOWN` again (the critical window is
   echo+45…65s; the pause makes the lockout obvious without hitting the 55s pod reset).
3. Press RIGHT `B` at ~minute 2: its mirror frames on the LEFT pod are a second,
   clock-independent probe (`b` vs `d`).
4. If `d` stops while the RIGHT pod still types: keep pressing `DOWN` once every ~10s
   for one more minute so the locked silence is in the log, then quit with `q`.
5. If `d` still appears past echo+70s, the unlock was still fresh — note it, quit, and
   reschedule; do not draw a renewal conclusion today.

- [ ] **Step 4: Save the log**

```powershell
Copy-Item zwiftboard.log baseline.log
```

Expected markers: `crypto challenge … acking to keep the button stream alive` (no
solver), **no** `dlock` lines, one `unlocked (RideOn echoed)` per pod at connect.

### Task 2: Renewal run — sign in, then the same exercise

- [ ] **Step 1: Start with no stored session and sign in through the gate**

```powershell
go run . -p notepad -v
```

Type email, `Tab`, password, `Enter` (do **not** press `Esc`). The gate must close on
its own and the status screen must appear. If the form shows `Sign-in failed: wrong
email or password`, fix it and retry; if it shows a transient network error, retry.

- [ ] **Step 2: Confirm the session was persisted**

```powershell
$t = "$env:LOCALAPPDATA\zwiftboard\zwift-token.json"
(Get-Content $t | ConvertFrom-Json).expires_at
```

Expected: the file exists and `expires_at` is minutes in the future. **Do not print the
tokens.** If the log has `could not persist the session — it will be lost on restart`,
note it (the run still works, but the next launch prompts again).

- [ ] **Step 3: Same LEFT-pod exercise as Task 1, 5 minutes total**

Same profile, same order, same 30s pause at ~2.5 minutes, RIGHT `B` probe at minute 2.
Then keep typing `DOWN` until minute 5. Quit with `q`.

- [ ] **Step 4: Save the log**

```powershell
Copy-Item zwiftboard.log renewal.log
```

### Task 3: Analysis checklist (agent, anywhere)

Run from the directory holding the two logs. Fill the table in Step 5.

- [ ] **Step 1: Confirm the baseline was actually stale**

```bash
rg -n 'msg="unlocked' baseline.log
rg -n 'msg="raw frame" controller=zwift/86:04 frame="23' baseline.log | sed -n '1p;$p' | cut -c1-40
rg -c 'msg="dlock' baseline.log
```

Expected: LEFT-pod `0x23` frames stop ~45–65s after the echo, `0` dlock lines. Use
`zwift/<side>` labels from `found controller` lines if the MACs differ on this machine.

- [ ] **Step 2: Confirm the renewal run actually solved the challenge**

```bash
rg -n 'msg="crypto challenge|msg="dlock request|msg="dlock response|msg="unlocked' renewal.log
```

Record, per connect: challenge timestamp, dlock `status`, `body_len` of the response,
and the solve duration. Expected: `status=200`, `body_len > 0`, and exactly one dlock
pair per challenge (a second challenge gets its own solve).

- [ ] **Step 3: Measure the LEFT engine lifetime**

```bash
rg 'msg="raw frame" controller=zwift/86:04 frame="23' renewal.log |
  awk '{print $1}' | sed -n '1p;$p'
rg -n 'msg="session liveness" controller=zwift/86:04' renewal.log
```

Then compare with the challenge timestamp. Record whether frames continue past
echo+70s and across the 30s pause. Note any `0x18` reset line
(`idle — sending pod reset`) inside the window — if present, the pause was too long and
the run is inconclusive for that segment.

- [ ] **Step 4: Sanity-check the mirror probe**

In the lines around the RIGHT `B` presses, verify LEFT-pod `0x23` frames appear within
~1s. If they do, the LEFT engine was alive at that moment (clock-independent proof).

- [ ] **Step 5: Fill the recording table**

| Measurement | baseline.log | renewal.log |
|---|---|---|
| last app unlock (wall clock, from the human) | | |
| echo time `E` (INFO `unlocked (RideOn echoed)`) | | |
| challenge frame + time | n/a (no solver) | |
| dlock status / response `body_len` / `duration_ms` | n/a | |
| first LEFT `0x23` | | |
| last LEFT `0x23` | | |
| `E` → last `0x23` delta | | |
| survived echo+70s? | | |
| survived the 30s pause? | | |
| RIGHT `B` mirrored at minute 2? | | |

### Task 4: Reconnect / durability probe (same session, ~5 minutes)

Purpose: show that renewal is per-connect and repeatable, not a one-shot fluke.

- [ ] **Step 1: Force a fresh connect**

Quit with `q`, wait ~15s, run `go run . -p notepad -v` again (the stored session is
reused silently — the gate should not appear, or should flash `Checking your Zwift
session` and close). If the gate opens the form, the stored session was rejected:
note it, sign in again, and continue.

- [ ] **Step 2: Repeat the LEFT typing test for 2 minutes**

Expect a **new** `crypto challenge … solving` + dlock pair on this connect, and `d`
keystrokes staying alive past echo+70s.

- [ ] **Step 3: Save the log and note the per-connect dlock count**

```powershell
Copy-Item zwiftboard.log reconnect.log
```

### Task 5: Verdict and record (agent; commit after)

- [ ] **Step 1: Pick exactly one verdict from the evidence**

| Verdict | Evidence | Meaning / next step |
|---|---|---|
| **A — renewal works** | dlock `200` (non-empty body) **and** LEFT `0x23` frames past echo+70s and across the pause, on both connects | The design's core claim holds. Record it, then schedule the T+24h re-check (Task 6) and start Phase 3 (API-removal questions) under a new brainstorm. |
| **B — API rejects** | dlock `401`/`403`/`400` (see `dlock response` status/body) | The route/token/headers differ from the game's. Bring the redacted exchange lines; iterating on headers is a **new brainstorm** (no code in this plan). |
| **C — API accepts, pod locks anyway** | dlock `200` non-empty, LEFT still dies at ~45–65s | The reply is wrong for the pod (pairing, prefix, ordering, or the pod expects the raw reply without `ff 04 00`). Next experiment: compare `payload` (request) with `body` (response) across the two challenges and against the game's `ff 04 00` reply if a capture exists. |
| **D — no dlock line at all** | challenge logged, no request | Solver not wired: sign-in skipped/failed, `Ready()` false, or the gate never released. Check `zwift sign-in: token stored` and the gate screen; this is a bug — report it, fix under a separate task. |
| **E — solve failed / timeout** | `pod unlock solve failed — answering with the empty ack` with a network error | Transient network/API outage. Repeat Task 2 when the network is healthy; no conclusion. |

- [ ] **Step 2: Update `AGENTS.md` with the verdict (concrete, dated, with numbers)**

Branch A (renewal works):

```markdown
- The one-time sign-in DOES renew the LEFT unlock (verified 2026-10-23: a stale
  app-granted ~24h unlock, dlock 200, LEFT `0x23` frames alive across the full
  test window and a 30s pause, on two consecutive connects). The reply is written
  as `ff 04 00 <reply>`; the empty `ff 04 00` still does not renew. Renewal is per
  connect (the pod challenges each time), so the sign-in only has to cover the
  account's token lifetime, not the pod's.
```

Branch B/C/D/E: write the observed dlock status/timing and the one-line implication,
e.g. `dlock 403 with a game-client bearer token — the endpoint wants more than the
token; no renewal yet`. Do **not** claim a fix.

Commit: `docs: record the sign-in unlock renewal result (2026-10-23)`

- [ ] **Step 3: Verification**

```bash
just sanity
```

Expected: green, zero code diff (`git diff --stat` shows only `AGENTS.md`, and the
plan file if you edit it).

### Task 6: T+24h durability re-check (human; 24h after Task 2)

- [ ] **Step 1: Do not touch the pods with the Zwift app in between.**
- [ ] **Step 2: Next day, run `go run . -p notepad -v` and repeat the 2-minute LEFT test.**

Expected if renewal is truly durable: it works again with the **stored** session (no
gate), a fresh dlock solve, and no app unlock needed. If it fails, capture
`renewal-24h.log` and record the negative result — that is the single most valuable
data point for the "drop the API" question, because it says whether the server keeps
issuing valid signatures or binds them to a session/account state.

---

## Phase 2 — What NOT to do tomorrow

- Do not change BLE behavior to "work around" a lock: the whole point is to learn
  whether the existing path works. Any behavior change needs `brainstorming` →
  design → plan → TDD (AGENTS.md), and it would contaminate the measurement.
- Do not add retry loops around the dlock call before Q1 is answered: a 401 already
  refreshes exactly once, and a 403 is not a transient error.
- Do not re-unlock with the phone app during Tasks 1–4: that removes the only stale
  state we have. (Task 6 depends on NOT doing it either.)
- Do not use `-plain` for the sign-in runs; the gate is TUI-only.

## Phase 3 — Optional, gated: what it takes to drop the API

Only after a verdict (and only with an approved design). These are the questions, in
cheapest-first order; each is an experiment, not a feature:

1. **Is the reply challenge-bound?** Replay a captured `ff 04 00 <reply>` to a *later*
   challenge (same pod, new connect). If the pod accepts it, the reply is (effectively)
   a bearer unlock and a one-time capture — or a local derivation — could replace the
   API call. Needs a debug-only way to inject a canned reply; do not ship it.
2. **Is it pod- or account-bound?** Replay the LEFT pod's reply on a reconnect, then
   after a pod reboot, then (if you have a second pair) on another pair.
3. **Is it a signature over the payload?** Capture two replies for two different
   challenges (the `03`/`02` pair already gives you this): same length? same prefix?
   The reply shape tells you whether a local forge is even plausible.
4. **APK evidence:** `libzwiftjni.so` (from the Zwift Android APK) contains the
   original d-lock code — look for the endpoint string, protobuf-lite call sites, and
   any *local* verification of the pod's challenge. The earlier extraction lived in
   `/tmp/opencode/apk/lib/arm64-v8a/libzwiftjni.so` (temp; re-extract from the APK if
   gone). Treat any private key found there as untrusted/rotatable, and never commit
   it.
5. Only if 1–4 show a local path: `brainstorming` → design for an offline solver
   behind the existing `ble.ChallengeSolver` seam (the interface is one method, so the
   design can swap implementations without touching BLE).

## Handback bundle (what the next AI needs)

- This file, `AGENTS.md`, the design doc, `git log --oneline -20`.
- `baseline.log`, `renewal.log`, `reconnect.log` (and `renewal-24h.log` later), raw,
  not pasted into chat if large.
- The filled recording table from Task 3, plus: Windows version, repo commit hash,
  whether the stored session existed at each run, and the wall-clock of the last
  Zwift-app unlock.
- The verdict letter (A–E) and any redacted `dlock` lines.
- **Never** the token file, the email, or the password.

**Resume prompt for a fresh session:**

> Read `AGENTS.md` and `docs/superpowers/plans/2026-10-23-left-unlock-renewal-debug.md`.
> Branch `feat/left-controller-support` (16 commits ahead of upstream). We are at
> Phase 1 Task <N>; the human is at the Windows machine. Logs are in <dir>. Help me
> run the next step and analyze the result against the verdict table. No code changes
> until the verdict is recorded.

## Self-Review

- **Spec coverage:** Q1 → Task 0; Q2 → Tasks 1–2; the operational record → Task 5;
  durability → Tasks 4 and 6; the eventual API removal → Phase 3 (explicitly gated).
  Every open question in the handoff state has a task.
- **Placeholder scan:** every command, log matcher, code block and doc text is
  concrete. The only human-only inputs are physical actions (typing, pods) and the
  wall-clock unlock time, which the table asks for explicitly.
- **Type/name consistency:** `dlock`, `challengePayload`, `ff 04 00`, `0x23`,
  `notepad` profile, `IdleReset`/`0x18`, `zwift-token.json` match the code and
  AGENTS.md as of `6f1bb81`.
