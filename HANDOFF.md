# HANDOFF

State: working tree on top of `3713786` (branch checked out in
`/home/smith/workspace`). Uncommitted: TUI + hold-keys work (see below).

## Goal

zwiftboard — Zwift Click V2 (BLE) → keyboard input on Windows. Current focus:
hold-key support (steering in MyWhoosh) on top of the stable both-pods
baseline `0f7872e`.

## What was done

| Commit | Summary |
|---|---|
| `43a153e` | `zwift.Pod`, `PodSide(id)`: decode left (`0x0B`) / right (`0x0A`) from the manufacturer-data first byte |
| `d2758e4` | Per-side sighting tracking, Bluetooth-off retry, pair gate (later found wrong, see below) |
| `a3af2e5` | Restored the idle `0x18` reset, made the gate opt-in |
| `3713786` | Reverted to `0f7872e` behavior: both pods connected. Added observational `pairStatus` |
| (uncommitted) | TUI (`internal/tui`, plan `docs/superpowers/plans/2026-10-09-tui-status.md`) |
| (uncommitted) | **Hold keys** — key down while button held, up on release. Plan `docs/superpowers/plans/2026-10-10-hold-keys.md` |

## Key findings (do not regress)

- The right pod's ~65s idle drop is NOT fixed by the left pod being
  *detected*; `0f7872e` connects BOTH pods and is stable (10+ min idle).
  Nothing is gated on pod side; every controller found gets a session.
- The idle reset (`keepaliveTick`, `-idle-reset` default 55s) runs regardless
  of the left pod.
- Reconnect is scan-gated (`markSeen`/`SeenRecently`, `-reconnect` 30s);
  `ClearSighting` on session end. Never address-hammer a sleeping pod.
- Keepalive (3s `RideOn` write) is a liveness probe only — a failed write is
  the ONLY disconnect signal; idle `0x23` silence never ends a session.

## Current behavior (uncommitted hold-keys change)

- Mapped keys HOLD: `ButtonHandler` downs on the pressed transition, ups on
  the released one. Steering = hold left/right in MyWhoosh.
- Held keys AUTO-REPEAT like a real keyboard (250ms delay, ~33ms interval):
  a synthetic `keybd_event` down has no OS auto-repeat, so without the
  re-send letters would not repeat in text apps (Notepad, chat). Games see
  the hold either way.
- Dedup for the mirrored pair is two-layer: edge-triggered diff per handler +
  per-VK idempotency (`ble.holdKeys`, package-level `heldVK` shared across
  handlers). `claimTap`/`TapDebounce`/`-debounce` removed.
- `ble.ReleaseAll()` on session end — a dropped controller can never leave a
  key stuck down.
- `OnTap` (TUI highlight) fires per pressed transition; mirrored press fires
  twice (idempotent highlight), key pressed once.

## Verification status

- `go test ./...` (99), `go vet ./...`, `GOOS=windows go vet`,
  `GOOS=windows go build` — all green.
- NOT run on hardware since `3713786` (TUI + hold-keys untested on Windows).

## Known caveats

- Repo working tree is CRLF; `gofmt -l` lists untouched files too (pre-existing
  noise, not a format issue). Keep edits CRLF.
- `_todos.md` has uncommitted changes that are not part of this work; left alone.
- Two different buttons mapped to the SAME VK token: hold tracking is per-VK,
  so releasing the first button releases the key while the second is still
  physically pressed (pre-existing config weirdness; not handled).
- Auto-repeat values are fixed at real-keyboard rates (250ms delay, 33ms
  interval); no flag to tune them yet.

## Possible next steps

- Hardware run (needs Windows + BT): both pods, idle 10+ min, then hold
  LEFT/RIGHT in MyWhoosh — confirm steering works and no stuck key after a
  pod drop (`ReleaseAll` fires, session reconnects via scan gate).
- If drops reappear, compare against `0f7872e` first (`git diff 0f7872e`).
- Make the TUI status window follow `-reconnect` (uses fixed 30s today).
