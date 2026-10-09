# AGENTS.md

## What this is

`zwiftboard` — Go CLI that turns Zwift Click V2 controllers (BLE) into
keyboard input on Windows. Runs on Windows only; developed cross-platform.

## Commands

```sh
go test ./...                        # must pass (runs on linux via stubs)
go vet ./...
gofmt -l .                           # must print nothing
GOOS=windows go build ./...          # target platform; also required for vet of tap_windows.go
go run .                           # on real Windows + Bluetooth hardware
```

No hardware here: `Watch`/`Session` correctness is reviewed, not tested.
Tests cover config parsing, key resolution and the button-handler dedup logic.

## Layout

- `main.go` (root) — flags, logger setup, target registry, wiring only; a
  single-binary app, so the entry point lives at the repo root (not `cmd/`)
- `internal/ble/` — scan (`Watch`), connect (`Session`), button decode, global tap dedup
- `internal/keys/` — config token → Windows VK code; `Down`/`Up`/`ReleaseAll` (keybd_event on windows, no-op elsewhere)
- `internal/config/` — config.yaml: `loglevel:` + `profiles:` map; returns errors (never exits)
- `internal/zwift/` — protocol facts: GATT UUIDs, button bits, frame decode, label helpers
- `internal/tui/` — Bubble Tea status screen (default UI; `-plain` = logs). OBSERVATIONAL
  only: fed by `ble.Connected`/`SideSeenRecently`/`ble.OnTap`; never gate a session on it.
  `tuirun.go` (root) wires it; in TUI mode slog goes to the log file, not stderr

## Conventions

- Conventional commits, one logical step per commit.
- Logging is `log/slog` only — no `log.Printf`, no `fmt.Println`. Level comes
  from `loglevel:` in config.yaml; `-v` forces debug. Raw/diagnostic lines are
  `Debug`, lifecycle `Info`, recoverable problems `Warn`, startup failures
  `Error` + `os.Exit(1)`.
- Comments explain protocol facts and *why* (there are many device quirks);
  keep them when moving code.
- Tests live next to their package; keep `buttonHandler`-style dependency
  injection (tap func as parameter) so logic stays testable without hardware.

## Hard-won facts (do not regress)

- WinRT returns a partial GATT service list right after connect: enumerate
  ALL services with retry and match characteristics by UUID (`connect`).
- The Click pair MIRRORS button state: one press arrives as the same frame
  from both units → dedup is edge-triggered per handler: only bits that
  changed since the previous frame act, so an identical mirrored bitmap
  changes nothing.
- The Click V2 is a two-pod PAIR, not two independent remotes: the LEFT pod is
  the pair's BLE anchor and the RIGHT pod mirrors state to it over a private RF
  link. Both pods are connected (see BOTH PODS ARE CONNECTED); only the
  RIGHT pod's buttons (Y Z A B PLUS) are normally mapped. A lone RIGHT pod's
  LED keeps blinking (advertising mode) even though its frames decode fine —
  cosmetic, not a fault.
- Handshake trio (`RideOn 02 03`, `00 08 00`, `00 08 10`), then a keepalive
  every 3s re-sending the raw `RideOn` opcode frame — byte-for-byte the
  qdomyos-zwift PR #4743 keepalive. PROVEN NOT TO HOLD A LONE RIGHT POD: on
  Windows the link still dies ~65s after the pod's LAST OWN TRANSMISSION (a
  button frame), deterministically (~64-68s), no matter the payload — plain
  `RideOn`, `RideOn 02 03`, `00 08 10`, `00 08 00`, with and without ±5s keepalive
  jitter all failed identically. The right pod's idle watchdog is reset only by
  its own inbound frames; nothing we write to sync-rx resets it, and there is no
  unlock/firmware hook for the lone right pod. The keepalive's real job is
  LIVENESS: a successful write proves the session is up, a failed write is the
  ONLY disconnect signal (idle 0x23 silence is normal), and a 3s write cadence
  also keeps us inside WinRT's connection timer. The qdomyos keepalive that
  actually holds a link belongs to the LEFT pod (24h-unlocked pair), which this
  right-only build dropped.
  (OpenBikeControl's real keep-awake is closed source in the private `prop`
  package, so qdomyos is the open reference.)
- RECONNECT IS SCAN-GATED, never address-hammered. `Connect(addr)` on a sleeping
  pod leaks one Windows `GattSession` with `SetMaintainConnection(true)` per
  attempt (tinygo gap_windows.go creates it unconditionally and only
  `Disconnect()` releases it, which never runs for a device that didn't
  physically connect); hundreds of leaked sessions over a long outage wedged the
  Windows BLE stack and stalled recovery ~54 minutes. The scanner records every
  sighting (`markSeen`), and a controller's session goroutine only calls
  `Session` when it was seen advertising within `-reconnect` (default 30s); a
  fresh sighting means the pod is awake, the connect succeeds, and nothing
  leaks. A session END also clears the sighting (`ClearSighting`): the pod may
  advertise all session long (keeping the gate open), so without the clear a
  drop would be followed by retries of the now-sleeping pod by address.
- PERIODIC POD RESET defeats the ~65s idle sleep (`-idle-reset`, default 55s):
  after that much button silence the keepalive tick writes a lone `0x18` to
  sync-rx instead of the RideOn ping — OpenBikeControl's "periodic RESET
  recovery" (Opcode.RESET=24, per their protocol enum; the dbg Reset pill in
  zwift_unlock.dart writes `[opcode.value]` to sync-rx withoutResponse, and
  connection.dart notes "reconnections after an automatic reset happen every
  minute"). The pod reboots ON OUR SCHEDULE, before its ~65s watchdog strands
  it asleep for 30-40s; it re-advertises within seconds (`ClickLogic` is
  otherwise closed source in the `prop` submodule) and the scan gate reconnects.
  Activity (any received frame) restarts the idle clock and suppresses the
  reset, so a session in use is never rebooted.
- Button frame `0x23` + protobuf field 1 = bitmap, **0 = pressed**; bits are
  the Click V2 set in `internal/zwift.Buttons`, not the Zwift Ride layout.
- LEFT controller needs the ~24h unlock from the Zwift app (`0xFF` challenge =
  locked; `ff 04 00` keeps an unlocked device unlocked, disable with `-ack=false`).
- Right-only liveness: the keepalive write is the ONLY signal. The old
  LEFT-anchor silence watchdog (`podWatcher`) was removed with LEFT support — an
  idle pod legitimately stops streaming `0x23` frames, and idle silence must
  NEVER end a session (the right controller must stay usable after a long idle
  for virtual shifting). A session ends only when a keepalive write actually
  fails (a real disconnect); the caller reconnects. The `0xFF` challenge still
  arrives on the ASYNC characteristic, not sync-tx, and the `0xFF` 03 challenge
  is answered immediately (`ff 04 00`) so an unlocked pod keeps streaming.
- Scanning runs in bursts forever (`Watch`): never Fatal when nothing is
  found, never stop looking for a controller that appears 10 minutes later.
- `connect` is serialized (`connectMu`); `onFound` fires after the scan burst
  so connects do not overlap an active scan.
- Teardown must PROBE before `Device.Disconnect()`: when BT goes off, tinygo's
  `ConnectionStatusChanged` handler already ran `Disconnect()` and freed the
  GATT session; calling it again is an unrecoverable AV (0xc0000005 —
  `crash-after-disable-bt-windows.log`). Only WRITES fail gracefully on a dead
  session — service discovery does NOT (DiscoverServices AVs on the freed COM
  object, observed on Windows) — so the probe (`endSession`) is always a
  keepalive write, never a discover; skip `Disconnect` when the device no
  longer answers, and when no characteristic is in hand skip `Disconnect` too
  (the device releases its slot on its own link timeout). Residual accepted: a
  device dying in the microseconds between probe and Disconnect can still AV
  (needs a tinygo fix).
- BOTH PODS ARE CONNECTED (commit 0f7872e is the proven-stable baseline: no
  drops after 10+ min idle with both controllers on). Every controller found —
  left AND right — gets its own session, handshake and keepalive. DISPROVEN
  twice on Windows: connecting only the right pod, and merely DETECTING the
  left one (the pair gate, -wait-left), both still dropped the right pod
  ~67-72s after its last button. Do not stop connecting the left pod. The pod
  side is in the advertisement (Zwift manufacturer record 0x094A, first byte:
  `0x0B` LEFT, `0x0A` RIGHT — `zwift.PodSide`); it is used for the
  OBSERVATIONAL pair status only (`pairStatus` in main.go: per side
  not detected / detected / connected, "ready" when both are connected).
  Never gate a session on it: a connected pod may stop advertising, so a gate
  on the other side's sighting can deadlock a reconnect.
- Startup: Bluetooth off is not fatal — `Enable()` is retried every 5s with a
  "turn it ON" message until the radio is on.
