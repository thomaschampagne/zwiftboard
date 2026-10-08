# Survive Windows Bluetooth-Off (crash-after-disable-bt) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the process survive Bluetooth being switched off mid-run — no more unrecoverable AV (`0xc0000005`) — with scan/reconnect resuming when BT returns.

**Architecture:** The double-teardown bug: tinygo's `ConnectionStatusChanged` handler auto-calls `Device.Disconnect()` when BT dies, then our blind `defer dev.Disconnect()` touches the freed GATT session → AV. Fix: one `endSession(where, probe, disconnect)` policy — probe the device with a graceful operation (write / service discovery), Disconnect only if it still answers. Plus: close three unguarded panic paths (dead Watch recover, `StopScan` AfterFunc, scan-callback goroutine) and a concurrent-map race at burst teardown.

**Tech Stack:** Go, `tinygo.org/x/bluetooth` v0.16.0, `log/slog`. No new deps.

**Spec:** Root-cause analysis of `crash-after-disable-bt-windows.log` (trace: `GattSession.Close` ← tinygo `Disconnect` ← our `ble.go:140` defer ← `Session` return at `ble.go:196`). Writes/discovery fail gracefully on a dead session; only `Disconnect` AVs — proven by the keepalive write returning an error *before* the crash.

## Global Constraints
- Stdlib only; `log/slog` only in prod code. Comments explain *why*; preserve protocol comments when moving code.
- **No commits** — user has not requested any; leave the working tree for review.
- Tests must pass on Linux with no BLE hardware (DI/stubs); do not use `t.Parallel` (package vars are shared).
- Formatting: worktree files are CRLF, so `gofmt -l .` flags everything pre-existing. Verify touched files LF-normalized: `for f in <files>; do tr -d '\r' <"$f" | gofmt -l /dev/stdin; done` → must print nothing.
- Gates (every task): `go test ./...`, `go vet ./...`, `GOOS=windows go build ./...`.
- Runtime faults = `Warn` + continue; startup failures stay `Error` + `os.Exit(1)`.

## Review Focus
1. **Dead device / BT-off → Disconnect skipped** (the AV fix) → `TestEndSessionSkipsDisconnectWhenProbeFails`.
2. **Live device exits** (handshake/notify/discovery failure) must *still* Disconnect — the controller accepts only ONE BLE connection, leaking it blocks reconnect → `TestEndSessionDisconnectsWhenProbeOK`.
3. **Late scan callback after `Scan` returns** → `fatal error: concurrent map read and map write` (uncatchable) → `TestScanBurstLateCallbackRace`, run under `-race`.
4. **Panics on foreign goroutines** (scan callback, `StopScan` AfterFunc, `adapter.Scan` itself) escape every existing defer → process death → `TestScanBurstCallbackPanicRecovered`, `TestScanBurstScanCallPanicRecovered`, `TestStopScanPanicRecovered`.
5. **Residual: device dies between successful probe and Disconnect** → still AVs; only fixable by forking tinygo. *Not testable* → documented in AGENTS.md hard-won facts (Task 3).
6. **Watch loop must never die** (hard-won fact: keep scanning forever) → per-iteration guard in `Watch`. *Not unit-tested:* Watch never returns, and a seam for it would add production complexity for a 3-line defer; verified by review + `GOOS=windows` build + the user's Windows scenario.

---

### Task 1: Probe-before-teardown (`endSession` + both call sites)

**Files:**
- Modify: `internal/ble/ble.go` (Session `defer` at :140, `keep` at :191, `connect.fail` at :224)
- Test: `internal/ble/session_test.go` (new)

**Interfaces:**
- Produces: `func endSession(where string, probe func() error, disconnect func())` — Task 2 consumes nothing from this; independent.

- [ ] **Step 1: Write failing tests**

```go
func TestEndSessionSkipsDisconnectWhenProbeFails(t *testing.T) {
    probes, disconnects := 0, 0
    endSession("t", func() error { probes++; return errors.New("device gone") },
        func() { disconnects++ })
    if probes != 1 || disconnects != 0 {
        t.Fatalf("probes=%d disconnects=%d, want 1/0", probes, disconnects)
    }
}

func TestEndSessionDisconnectsWhenProbeOK(t *testing.T) {
    disconnects := 0
    endSession("t", func() error { return nil }, func() { disconnects++ })
    if disconnects != 1 { t.Fatalf("disconnects=%d, want 1", disconnects) }
}
```

- [ ] **Step 2: Run tests to verify they fail** — `go test ./internal/ble/` → FAIL: `undefined: endSession`
- [ ] **Step 3: Implement `endSession` + rewire both teardown sites** in `internal/ble/ble.go`

```go
func endSession(where string, probe func() error, disconnect func()) {
    if err := probe(); err != nil {
        slog.Debug("device no longer answers — skipping disconnect", "where", where, "probe", err)
        return
    }
    disconnect()
}
```
WHY comment on the function: BT-off → tinygo's `ConnectionStatusChanged` already ran `Device.Disconnect()`; a second call is a use-after-free AV `0xc0000005` (the crash). Probes (writes, discovery) fail gracefully; only `Disconnect` AVs.
- `Session`: move `keep := []byte{0x00, 0x08, 0x10}` above the defer; replace `defer dev.Disconnect()` with `defer func() { endSession("session "+t.Label, func() error { _, err := writeChar(syncRx, keep); return err }, func() { _ = dev.Disconnect() }) }()`. The probe *is* a keepalive — harmless on a live device. Delete the old `keep` declaration at :191.
- `connect`: replace `fail := func(e error) { _ = dev.Disconnect(); err = e }` with `fail` that calls `endSession("connect "+t.Label, func() error { _, de := dev.DiscoverServices(nil); return de }, func() { _ = dev.Disconnect() })` then sets `err`. One-shot discovery (NOT the retried `discoverServices` helper) so no 750ms×3 delay on the failure path.
- [ ] **Step 4: Run tests to verify they pass** — `go test ./internal/ble/ -run TestEndSession -v` → PASS
- [ ] **Step 5: Run gates** — `go test ./... && go vet ./... && GOOS=windows go build ./...` → all clean

### Task 2: Panic-guard + race hardening (scan, Watch, main wiring)

**Files:**
- Modify: `internal/ble/ble.go` (`scanBurst` :102-129, `Watch` :77-98), `main.go` (:38-48, :153, :174-184)
- Test: `internal/ble/scan_test.go` (new)

**Interfaces:**
- Produces: `type collector` with `add(key string, t Target) bool` and `snapshot() (map[string]Target, []string)`; package vars `scanFn func(cb func(*bluetooth.Adapter, bluetooth.ScanResult)) error` (default `adapter.Scan`) and `stopScanFn func()` (default `_ = adapter.StopScan()`).
- Consumes: `recoverLog`, `Target` (existing).

- [ ] **Step 1: Write failing tests** (helpers: `stubScan(t, fn)` / `stubStop(t, fn)` save+restore via `t.Cleanup`; `fakePayload` implements all 6 `AdvertisementPayload` methods — `LocalName`, `HasServiceUUID`, `ServiceUUIDs`, `Bytes`, `ManufacturerData`, `ServiceData`; `zwiftResult()` builds `bluetooth.ScanResult{Address: bluetooth.Address{}, RSSI: -50, AdvertisementPayload: fakePayload{name: "Zwift Click"}}`)

```go
func TestScanBurstCallbackPanicRecovered(t *testing.T)   // known() panics inside cb → scanBurst returns, empty, nil err
func TestScanBurstScanCallPanicRecovered(t *testing.T)   // scanFn panics → scanBurst returns, empty, nil err
func TestScanBurstLateCallbackRace(t *testing.T)         // scanFn: 1 sync cb, then 50 goroutine cbs, return immediately;
                                                         // snapshot len==1 both; still len==1 after wg.Wait(); run with -race
func TestStopScanPanicRecovered(t *testing.T)            // stopScanFn panics; scanFn sleeps 30ms, burst=1ms → returns, nil err
```
(The last three fail today: panic escapes / no guard exists.)

- [ ] **Step 2: Run tests to verify they fail** — `go test ./internal/ble/ -run 'ScanBurst|StopScan'` → FAIL (panics)
- [ ] **Step 3: Implement** in `internal/ble/ble.go`:
  - `scanFn`/`stopScanFn` package vars (one-line WHY: DI so scan logic is testable without hardware, per AGENTS).
  - `collector` type: `mu sync.Mutex`; `add` locks, dedup-checks `pending`, inserts + appends `order`, returns bool; `snapshot` locks, returns a **cloned** map and slice (late callbacks may still write the originals — nobody reads them).
  - `scanBurst`: inner func keeps `defer recoverLog("scan")` (covers a panicking `scanFn` call) + `timer := time.AfterFunc(burst, func() { defer recoverLog("stop scan"); stopScanFn() })` (AfterFunc runs on its own goroutine); the scan callback gets `defer recoverLog("scan callback")` as its FIRST line (it runs on WinRT's goroutine — the old defer at :105 could never catch its panics), then `known(key)` check, `c.add(key, target)`, and the existing `slog.Info("found controller", ...)` only when `add` returned true; return `c.snapshot()` + `scanFn`'s error.
  - `Watch`: wrap the whole loop body (scan + `onFound` delivery) in `func() { defer recoverLog("watch iteration"); ... }()`, sleep stays outside. WHY: hard-won fact — never stop looking; a panic in `onFound` must cost one burst, not the loop.
  - `main.go`: delete the duplicated local `guarded` (:38-48), use `ble.Guarded("session "+t.Label, ...)` at :153; replace :174-184 with plain `go ble.Watch(*scanFor, registered, add)` + comment pointing at the in-Watch iteration guard (the old recover was dead code — nested `go` made it unreachable); drop now-unused `runtime/debug` import if build says so.
- [ ] **Step 4: Run tests to verify they pass** — `go test -race ./internal/ble/ -v` → PASS (race detector clean)
- [ ] **Step 5: Run gates** — `go test ./... && go vet ./... && GOOS=windows go build ./...` → clean

### Task 3: Docs — record the hard-won fact

**Files:** `AGENTS.md` (Hard-won facts), `README.md` (troubleshooting row :199)

- [ ] **Step 1: Add AGENTS.md bullet:** teardown must PROBE before `Device.Disconnect()` — tinygo auto-disconnects on BT-off, and Disconnect on the freed GATT session is an unrecoverable AV `0xc0000005` (`crash-after-disable-bt-windows.log`); writes/discovery fail gracefully and serve as the probe (`endSession`); residual probe→Disconnect microsecond race documented as accepted (needs a tinygo fix).
- [ ] **Step 2: Extend README row :199:** switching Windows Bluetooth off mid-run no longer crashes — sessions end, scan retries every 3s, reconnects when BT returns.

### Task 4: Final verification

- [ ] Run full gates: `go test ./...`, `go test -race ./internal/ble/`, `go vet ./...`, `GOOS=windows go build ./...`, LF-normalized gofmt check on touched files.
- [ ] Ask user to re-run the disable-BT scenario on Windows (only real acceptance test — no BT hardware here).
