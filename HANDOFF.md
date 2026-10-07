# HANDOFF: Zwift Click V2 BLE listener (Go, Windows)

## Goal
Go program that connects to Zwift Click V2 controllers over Bluetooth LE on Windows, logs button press/release events, and (since 2026-10-07) types the mapped keyboard key on every press — Click acts like a Bluetooth keyboard. Mapping lives in `config.yaml` (cwd): named **profiles** per game, plus the log level.

## Status
- Compiles clean for Windows (`GOOS=windows go build ./...`). Ran on hardware: scan + connect worked, but service discovery failed (see Fix 1).
- Only V2 is handled. The V1 `0x37` decode from the first draft was dropped.
- 12 unit tests pass on linux (`go test ./...`); BLE paths are compile-checked only.

### Restructure (2026-10-07 evening)
`main.go` split into `internal/{ble,keys,config,zwift}`; the entry point lives at the repo
root (`main.go`, single-binary layout — `cmd/` was tried and dropped); tests moved next to their packages.
`config.Load` returns errors instead of calling `log.Fatal`.

### Fix 1 — service discovery (2026-10-07)
Errors: `zwift service not found: bluetooth: did not find all requested services` then `async operation failed with status 2` on reconnect.
Cause: `DiscoverServices([]UUID{svc})` on WinRT returns a partial/empty list right after connect; the UUID filter then errors. Fix: enumerate ALL services (retry 3x, 750ms apart) and match characteristics `...0002/0003/0004...` by UUID across every service; log service list at debug (`-v`), and on failure the error lists services seen.

### Fix 2 — handshake (adopted from working Python impl)
Old `RideOn`-only handshake was never actually exercised (died at discovery). Now matches ZwiftBridge exactly:
1. `52 69 64 65 4F 6E 02 03` (`"RideOn" 02 03`), 2. `00 08 00`, 3. `00 08 10` — 100ms apart.
Keepalive = `00 08 10` every 2s (was `RideOn` every 3s). Write tries WriteWithoutResponse, falls back to Write.

### Fix 3 — button bits (decoded from ZwiftBridge's frame table)
Verified: LEFT 0x1, UP 0x2, RIGHT 0x4, DOWN 0x8, A 0x10, B 0x20, Y 0x40, Z 0x80, MIN 0x100 (left module), PLUS 0x1000 (right module). Old Ride-derived map was wrong (had 0x100=Z, 0x1000=ONOFF_L, no 0x80).

New flag: `-addr D4:06:0F:A9:86:04,...` skips scanning (no watch for late controllers in this mode).

### Feature — profiles + leveled logging (2026-10-07 evening)
- `config.yaml`: top-level `loglevel:` (debug|info|warn|error, default info) and `profiles:` map of profile → button→key bindings. Flat (old) format errors with guidance. Loaded once at startup (`-config` overrides path).
- `-p` / `--profile` selects the profile; **default `mywhoosh`**; unknown profile errors with the available list. Built-in `mywhoosh` (Click buttons → MyWhoosh HD shortcuts: MIN/PLUS = K/I gears, A = space power-up, B = esc pause, Y = u U-turn/UI, Z = tab camera) and `zwift` (old identity mapping).
- Tokens: `a-z`, `0-9`, `f1`-`f12`, named keys (up/down/left/right/enter/space/tab/esc/backspace/delete/insert/home/end/pageup/pagedown/shift/ctrl/alt/capslock), and single punctuation characters ( - = , . / ; ' [ ] \ and backtick).
- Logging is `log/slog` only; level from `loglevel:`, `-v` forces debug. Raw frames/service lists/sync-tx = debug, edges/lifecycle = info, recoverable issues = warn, startup failures = error+exit.
- `internal/keys`: token → VK (`Resolve`) + `Tap` (`user32 keybd_event` on windows, no-op elsewhere so `go test`/`vet` run on linux).

### Fix 4 — duplicate key taps (2026-10-07, revised after log analysis)
Symptom: one physical press typed the key 2x, mainly right-module buttons (Y/Z/A/B).
Root cause (from user logs): the pair MIRRORS button state — pressing B produced the identical frame `23 08 DF FF FF FF 0F` from BOTH units (`10:21` and `86:04`) within the same second. First attempt used a per-device debounce, useless here: each unit's frame was the first for its own handler.
Fix: `claimTap(name)` in `internal/ble` — one tap window per button NAME, shared across all controllers (mutex-guarded). A second claim inside `-debounce` (default 200ms) logs `duplicate=true` and skips the key. Also covers single-device retransmit bursts. `ButtonHandler` takes the tap func as a param (testable); `internal/ble/buttons_test.go` has `TestCrossControllerDuplicateTap`.

### Feature — continuous scan (2026-10-07 evening)
- `Watch` replaces the one-shot discover: endless scan bursts (`-scan` = burst length, 3s gap), never gives up when nothing is found, and picks up a controller turned on 10 minutes later (its own session goroutine, registry dedups).
- `onFound` fires after the burst so connects never overlap an active scan. `-addr` mode stays fixed-list (no watch).

## Setup
```
go run . -v                # flags: -v (log level debug), -scan 10s (burst), -addr MAC,...,
                                  # -config config.yaml, -p mywhoosh/--profile, -debounce 200ms, -ack=true
```
Edit `config.yaml` in cwd (profiles + loglevel). Close the Zwift / Companion app first (each controller accepts one BLE connection). Press a button on each controller during a scan burst to wake it.

## Library
`tinygo.org/x/bluetooth` (WinRT backend, works with standard Go on Windows). Verified from its source (`dev` branch):
- `EnableNotifications` prefers Notify and falls back to Indicate (needed for SyncTX).
- `ScanResult.ManufacturerData()` returns elements with `CompanyID` and `Data`.
- `Scan` blocks until `StopScan`.

## Design (root `main.go` + `internal/*`)
1. `ble.Watch`: scan bursts forever; keep devices with manufacturer company ID `0x094A` or a name starting with "zwift". Zwift device ID = first byte of manufacturer data (logged). `-addr` bypasses the watch.
2. Main keeps an address→registered registry; each new controller gets one goroutine with a reconnect loop (`ble.Session`, 5s backoff). Connects are serialized by `connectMu`.
3. `connect`: connect, enumerate all GATT services (with retry), find Async/SyncRX/SyncTX chars by UUID across any service; async + syncRx required, syncTx optional.
4. `Session`: subscribe Async (notify) + SyncTX (indicate), send activation trio, then `00 08 10` every 2s. A failed write is treated as a disconnect.
5. SyncTX handler: reply starting with `'R'` (0x52) means unlocked, so send `ff 04 00` once (if `-ack`). Reply starting with `0xFF` means locked (crypto challenge); logged as warn.
6. Async handler: frames starting `0x23` are protobuf; field 1 is a uint32 bitmap where **0 = pressed**. Edges are logged; on press `claimTap` gates `Tap` — global per-button window across BOTH controllers (mirrored pair), `-debounce` (default 200ms).

## GATT UUIDs (Zwift custom service)
- Service `00000001-19ca-4651-86e5-fa29dcdd09d1`
- Async (notify) `...0002...`, SyncRX (write) `...0003...`, SyncTX (indicate) `...0004...` (same suffix `-19ca-4651-86e5-fa29dcdd09d1`)

## Button bits (Click V2, decoded from ZwiftBridge's working frame table)
LEFT 0x1, UP 0x2, RIGHT 0x4, DOWN 0x8, A 0x10, B 0x20, Y 0x40, Z 0x80, MIN 0x100, PLUS 0x1000.
Click V2 hardware: left module has 4 arrows plus minus; right has Y/Z/A/B plus plus.

## Known risks / unverified
- Button bits now from ZwiftBridge frame table (verified working there), not from this program's own hardware run. Confirm with `-v` on real presses; unknown bits log as `BITn` (only at debug level).
- LEFT controller needs a hardware unlock set by the Zwift app (~24h). Without it, expect the `0xFF` challenge and silent buttons. The right controller needs no unlock.
- Right controller deep-sleeps ~56s idle without keepalive; keepalive now `00 08 10` every 2s (per ZwiftBridge), still untested here.
- ZwiftBridge does NOT use SyncTX (`...0004...`) nor `ff 04 00`; both kept from Ride/qdomyos work, may be unnecessary or wrong for Click V2. Disable with `-ack=false`.
- Connecting to an asleep/non-advertising device on Windows may hang; no connect timeout is implemented.
- `async operation failed with status 2` seen once on fast reconnect — transient; loop retries after 5s.
- Continuous scan (repeated `adapter.Scan` while sessions hold connections) is untested on hardware — if connects degrade, add a longer gap (`scanGap`) or pause watching while any session is up.
- Keyboard output uses legacy `keybd_event` (works everywhere, target window must have focus; not a real HID keyboard — Zwift itself will not see Click as a BLE keyboard, only the Windows foreground app will).
- If a key still duplicates beyond the window: log line shows `duplicate=true` — raise `-debounce` (mirrored frames should arrive within tens of ms; if they lag, e.g. 400ms, raise the flag). Two `state=pressed` lines BOTH WITHOUT `duplicate=true` = mirror gap wider than window.
- Pair mirrors button state (both units report every press) — dedup relies on frames arriving inside `-debounce`.
- `GOOS=windows` is required at build time on non-Windows hosts; BLE needs a real Windows machine with Bluetooth.

## Sources
- ZwiftBridge (working Python Click V2 bridge — handshake, keepalive, button frames): https://github.com/jimhoefnagels/ZwiftBridge
- Makinolo, Zwift Ride protocol (0x23 frame, bitmap, RideOn echo): https://www.makinolo.com/blog/2024/07/26/zwift-ride-protocol/
- qdomyos-zwift PR #4743 (Click V2 keepalive, unlock detection, `ff0400`): https://github.com/cagnulein/qdomyos-zwift/pull/4743
- ajchellew/zwiftplay (older encrypted Play/Click protocol background): https://github.com/ajchellew/zwiftplay
- p3dda/RideToWoosh (Click V2 handshake picks characteristics by UUID): https://github.com/p3dda/RideToWoosh
- OpenBikeControl commit `2cb079fc` (referenced by the PR for the `ff0400` ack; not read directly)
- MyWhoosh shortcuts (for the default profile): https://mywhooshinfo.com/blog/mywhoosh-keyboard-shortcuts, https://www.keyboardista.com/en/shortcuts/mywhoosh-desktop/

## Suggested next steps
1. Run `go run . -v` on real hardware; confirm scan bursts, connect past discovery, handshake log line, per-button names, and `key=...` taps for both controllers.
2. If still "characteristics missing", the `-v` service list / error line shows actual UUIDs — adjust match.
3. If LEFT stays silent, try `-ack=false` or open Zwift once (24h unlock).
4. Verify continuous scan on hardware: kill nothing, turn the second controller on 10 min later; watch `found controller`.
5. Tune `mywhoosh` profile bindings in `config.yaml` to taste (per-game profiles now exist).
6. Optional: hot-reload config on change; battery level (frame type `0x19`) decoding; real HID keyboard emulation (ViGEm) if target app ignores simulated input.
