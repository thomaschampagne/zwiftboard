# AGENTS.md

## What this is

Go CLI that turns Zwift Click V2 controllers (BLE) into keyboard input on
Windows. Runs on Windows only; developed cross-platform.

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
- `internal/keys/` — config token → Windows VK code; `Tap` (keybd_event on windows, no-op elsewhere)
- `internal/config/` — config.yaml: `loglevel:` + `profiles:` map; returns errors (never exits)
- `internal/zwift/` — protocol facts: GATT UUIDs, button bits, frame decode, label helpers

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
  from both units → `claimTap` dedup is global per button name, not per device.
- Handshake trio (`RideOn 02 03`, `00 08 00`, `00 08 10`) then `00 08 10`
  keepalive every 2s; without it the device sleeps (~56s) and writes fail
  (that failure is also the disconnect signal).
- Button frame `0x23` + protobuf field 1 = bitmap, **0 = pressed**; bits are
  the Click V2 set in `internal/zwift.Buttons`, not the Zwift Ride layout.
- LEFT controller needs the ~24h unlock from the Zwift app (`0xFF` challenge =
  locked; `ff 04 00` keeps an unlocked device unlocked, disable with `-ack=false`).
- Scanning runs in bursts forever (`Watch`): never Fatal when nothing is
  found, never stop looking for a controller that appears 10 minutes later.
- `connect` is serialized (`connectMu`); `onFound` fires after the scan burst
  so connects do not overlap an active scan.
