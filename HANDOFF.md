# HANDOFF

State as of commit `3713786` (branch checked out in `/home/smith/workspace`).

## Goal

Startup flow for zwiftboard, text/logs only (a TUI comes later):

1. Check system Bluetooth; if off, ask the user to turn it on and keep retrying.
2. Ask the user to switch on the LEFT and RIGHT Click V2 controllers.
3. Once both are present, listen to click events and map them to keys.

Hard requirement: stay as stable as commit `0f7872e` (no disconnects after
10+ min idle with both controllers on; confirmed by the user on real hardware).

## What was done

| Commit | Summary |
|---|---|
| `43a153e` | `zwift.Pod`, `PodSide(id)`: decode left (`0x0B`) / right (`0x0A`) from the manufacturer-data first byte |
| `d2758e4` | Per-side sighting tracking, Bluetooth-off retry, pair gate (later found wrong, see below) |
| `a3af2e5` | Restored the idle `0x18` reset, made the gate opt-in |
| `3713786` | **Current.** Reverted to `0f7872e` behavior: both pods connected. Added observational `pairStatus` |

## Key finding (do not regress)

The right pod's ~65s idle drop is NOT fixed by the left pod being *detected*.
Log evidence: drops at ~67s and ~72s after the last button with the left pod
detected nearby. In `0f7872e` the left pod was *connected* (`add()` opened a
session for every controller found); that is what is stable. My detect-only
change was the regression. Also, suppressing the idle reset when the left was
present removed the only working mitigation.

Consequences encoded in the code:
- Every controller found gets a session, left and right (`main.go` `add`).
- Nothing is gated on pod side. A connected pod may stop advertising, so a gate
  on the other side's sighting can deadlock a reconnect (a 10 minute wait was
  observed with the gate).
- The idle reset (`keepaliveTick`) runs regardless of the left pod.

## Current behavior

- Startup: `Enable()` retried every 5s with an Error log while Bluetooth is off,
  then an Info prompt to switch on both controllers.
- `ble.Watch` scans forever; each controller's session goroutine connects only
  when it was seen advertising within `-reconnect` (unchanged from baseline).
- `pairStatus` (main.go, skipped in `-addr` mode) logs on change:
  `left|right` x `not detected | detected | connected`; "ready" when both are
  connected, "waiting" when a side is not detected.
- Backed by `ble.SideSeenRecently` (scan sightings per side) and `ble.Connected`
  (live sessions per side, set in `Session` after the handshake).
- `Target.Side` is set from the advert; `-addr` targets have `PodUnknown`.

## Verification status

- `go test ./...`, `go vet ./...`, `GOOS=windows go vet ./...` and
  `GOOS=windows go build ./...` pass.
- NOT yet run on hardware after `3713786`. Next step: run
  `go run . -p test-notepad` on Windows with both controllers on, idle 10+ min,
  then click the right controller. Expect "ready" after both connect, and no
  "session ended" beyond the periodic reset/reconnect cycle seen in `0f7872e`.
  Also try one pod only and check the "waiting" log.

## Known caveats

- `gofmt -l` lists ~16 files, including untouched ones: the working tree is CRLF
  (git normalizes). It is not a real format issue. Edit with care: a plain
  `sed`/script on LF content will not match CRLF files.
- `_todos.md` has uncommitted changes that are not part of this work; left alone.
- `pairStatus` "detected" uses a fixed 30s window rather than `-reconnect`.
- Left-pod unlock: a locked left pod needs the ~24h unlock from the Zwift app;
  we only answer the `0xFF` challenge with `ff 04 00` (unchanged baseline).

## Possible next steps

- DONE: TUI (default; `-plain` for logs, `-demo` for mock) in `internal/tui`,
  plan `docs/superpowers/plans/2026-10-09-tui-status.md`. Needs a hardware run
  (idle 10+ min, both pods) to confirm no stability change vs `0f7872e`.
- Make the status window follow `-reconnect`.
- If drops reappear, compare against `0f7872e` first (`git diff 0f7872e`).
