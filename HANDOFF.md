# HANDOFF: Zwift Click V2 BLE listener (Go, Windows)

## Goal
Go program that connects to Zwift Click V2 controllers over Bluetooth LE on Windows, logs button press/release events, and (since 2026-10-07) types the mapped keyboard key on every press — Click acts like a Bluetooth keyboard. Mapping lives in `config.yaml` (cwd).

## Status
- Compiles clean for Windows (`GOOS=windows go vet .` / `go build`). Ran on hardware: scan + connect worked, but service discovery failed (see Fix 1).
- Only V2 is handled. The V1 `0x37` decode from the first draft was dropped.

### Fix 1 — service discovery (2026-10-07)
Errors: `zwift service not found: bluetooth: did not find all requested services` then `async operation failed with status 2` on reconnect.
Cause: `DiscoverServices([]UUID{svc})` on WinRT returns a partial/empty list right after connect; the UUID filter then errors. Fix: enumerate ALL services (retry 3x, 750ms apart) and match characteristics `...0002/0003/0004...` by UUID across every service; log service list with `-v`, and on failure the error lists services seen.

### Fix 2 — handshake (adopted from working Python impl)
Old `RideOn`-only handshake was never actually exercised (died at discovery). Now matches ZwiftBridge exactly:
1. `52 69 64 65 4F 6E 02 03` (`"RideOn" 02 03`), 2. `00 08 00`, 3. `00 08 10` — 100ms apart.
Keepalive = `00 08 10` every 2s (was `RideOn` every 3s). Write tries WriteWithoutResponse, falls back to Write.

### Fix 3 — button bits (decoded from ZwiftBridge's frame table)
Verified: LEFT 0x1, UP 0x2, RIGHT 0x4, DOWN 0x8, A 0x10, B 0x20, Y 0x40, Z 0x80, MIN 0x100 (left module), PLUS 0x1000 (right module). Old Ride-derived map was wrong (had 0x100=Z, 0x1000=ONOFF_L, no 0x80).

New flag: `-addr D4:06:0F:A9:86:04,...` skips scanning.

### Feature — keyboard output from config.yaml (2026-10-07)
- `config.yaml` in cwd maps button name (LEFT/UP/RIGHT/DOWN/A/B/Y/Z/MIN/PLUS) → key token; loaded once at startup (`-config` to override). Missing file = log-only mode, bad YAML / unknown button / unresolvable key = fatal at startup.
- Tokens: `a-z`, `0-9`, `f1`-`f12`, named keys (up/down/left/right/enter/space/tab/esc/backspace/delete/insert/home/end/pageup/pagedown/shift/ctrl/alt/capslock), and single punctuation characters ( - = , . / ; ' [ ] \ and backtick).
- `keymap.go`: YAML load + token → Windows VK code (`resolveKey`). `keyboard_windows.go`: `keyTap` via `user32 keybd_event` (down+up), `keyboard_other.go`: no-op stub so `go test`/`go vet` run on linux. Press edge only; log line gets ` -> <key>` suffix. `-v` logs each tap.
- Tests: `keymap_test.go` + `button_test.go` (`go test ./...` — 6 pass).

### Fix 4 — duplicate key taps (2026-10-07, revised after log analysis)
Symptom: one physical press typed the key 2x, mainly right-module buttons (Y/Z/A/B).
Root cause (from user logs): the pair MIRRORS button state — pressing B produced the identical frame `23 08 DF FF FF FF 0F` from BOTH units (`10:21` and `86:04`) within the same second. First attempt used a per-device debounce, useless here: each unit's frame was the first for its own handler.
Fix: `claimTap(name)` in main.go — one tap window per button NAME, shared across all controllers (mutex-guarded). A second claim inside `-debounce` (default 200ms) logs `(duplicate)` and skips the key. Also covers single-device retransmit bursts. `buttonHandler` takes the tap func as a param (testable); `button_test.go` has `TestCrossControllerDuplicateTap`.

## Setup
```
go run . -v            # flags: -v raw frames/taps, -scan 10s, -addr MAC,..., -config config.yaml, -debounce 200ms, -ack=true
```
Edit `config.yaml` in cwd for key mapping. Close the Zwift / Companion app first (each controller accepts one BLE connection). Press a button on each controller during the scan to wake it.

## Library
`tinygo.org/x/bluetooth` (WinRT backend, works with standard Go on Windows). Verified from its source (`dev` branch):
- `EnableNotifications` prefers Notify and falls back to Indicate (needed for SyncTX).
- `ScanResult.ManufacturerData()` returns elements with `CompanyID` and `Data`.
- `Scan` blocks until `StopScan`.

## Design (main.go)
1. `discover`: scan for N seconds; keep devices with manufacturer company ID `0x094A` or a name starting with "zwift". Zwift device ID = first byte of manufacturer data (logged). `-addr` bypasses scan.
2. One goroutine per device with a reconnect loop (`session`, 5s backoff). Connects are serialized by a mutex.
3. `connect`: connect, enumerate all GATT services (with retry), find Async/SyncRX/SyncTX chars by UUID across any service; async + syncRx required, syncTx optional.
4. `session`: subscribe Async (notify) + SyncTX (indicate), send activation trio, then `00 08 10` every 2s. A failed write is treated as a disconnect.
5. SyncTX handler: reply starting with `'R'` (0x52) means unlocked, so send `ff 04 00` once (if `-ack`). Reply starting with `0xFF` means locked (crypto challenge); this is only logged.
6. Async handler: frames starting `0x23` are protobuf; field 1 is a uint32 bitmap where **0 = pressed**. Edges are logged by button name; on press `claimTap` gates `keyTap` — global per-button window across BOTH controllers (mirrored pair), `-debounce` (default 200ms).

## GATT UUIDs (Zwift custom service)
- Service `00000001-19ca-4651-86e5-fa29dcdd09d1`
- Async (notify) `...0002...`, SyncRX (write) `...0003...`, SyncTX (indicate) `...0004...` (same suffix `-19ca-4651-86e5-fa29dcdd09d1`)

## Button bits (Click V2, decoded from ZwiftBridge's working frame table)
LEFT 0x1, UP 0x2, RIGHT 0x4, DOWN 0x8, A 0x10, B 0x20, Y 0x40, Z 0x80, MIN 0x100, PLUS 0x1000.
Click V2 hardware: left module has 4 arrows plus minus; right has Y/Z/A/B plus plus.

## Known risks / unverified
- Button bits now from ZwiftBridge frame table (verified working there), not from this program's own hardware run. Confirm with `-v` on real presses; unknown bits log as `BITn`.
- LEFT controller needs a hardware unlock set by the Zwift app (~24h). Without it, expect the `0xFF` challenge and silent buttons. The right controller needs no unlock.
- Right controller deep-sleeps ~56s idle without keepalive; keepalive now `00 08 10` every 2s (per ZwiftBridge), still untested here.
- ZwiftBridge does NOT use SyncTX (`...0004...`) nor `ff 04 00`; both kept from Ride/qdomyos work, may be unnecessary or wrong for Click V2. Disable with `-ack=false`.
- Connecting to an asleep/non-advertising device on Windows may hang; no connect timeout is implemented.
- `async operation failed with status 2` seen once on fast reconnect — transient; loop retries after 5s.
- Keyboard output uses legacy `keybd_event` (works everywhere, target window must have focus; not a real HID keyboard — Zwift itself will not see Click as a BLE keyboard, only the Windows foreground app will).
- If a key still duplicates beyond the window: log line shows `(duplicate)` — raise `-debounce` (mirrored frames should arrive within tens of ms; if they lag, e.g. 400ms, raise the flag). Two `pressed` lines BOTH WITHOUT `(duplicate)` = mirror gap wider than window.
- Pair mirrors button state (both units report every press) — dedup relies on frames arriving inside `-debounce`.
- `GOOS=windows` is required at build time on non-Windows hosts; BLE needs a real Windows machine with Bluetooth.

## Sources
- ZwiftBridge (working Python Click V2 bridge — handshake, keepalive, button frames): https://github.com/jimhoefnagels/ZwiftBridge
- Makinolo, Zwift Ride protocol (0x23 frame, bitmap, RideOn echo): https://www.makinolo.com/blog/2024/07/26/zwift-ride-protocol/
- qdomyos-zwift PR #4743 (Click V2 keepalive, unlock detection, `ff0400`): https://github.com/cagnulein/qdomyos-zwift/pull/4743
- ajchellew/zwiftplay (older encrypted Play/Click protocol background): https://github.com/ajchellew/zwiftplay
- p3dda/RideToWoosh (Click V2 handshake picks characteristics by UUID): https://github.com/p3dda/RideToWoosh
- OpenBikeControl commit `2cb079fc` (referenced by the PR for the `ff0400` ack; not read directly)

## Suggested next steps
1. Run `go run . -v` on real hardware; confirm connect past discovery, handshake log line, per-button names, and ` -> <key>` taps for both controllers.
2. If still "characteristics missing", the `-v` service list / error line shows actual UUIDs — adjust match.
3. If LEFT stays silent, try `-ack=false` or open Zwift once (24h unlock).
4. Optional: tune `config.yaml` per game; reload-on-change or JSON lines output.
5. Optional: battery level (frame type `0x19`) decoding; unverified.
6. Optional: real HID keyboard emulation (ViGEm/HID API) if target app ignores simulated input.
