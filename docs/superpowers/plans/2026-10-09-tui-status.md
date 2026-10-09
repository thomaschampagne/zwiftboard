# TUI Status Screen Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Bubble Tea TUI, shown by default at startup, that displays Bluetooth state, Left/Right pod state, active profile, the right-pod key table (with click flash) and the focus-window target.

**Architecture:** New `internal/tui` package holds a pure Bubble Tea model with no BLE imports; it is driven by messages. `main.go` feeds it real state through read-only observers (`ble.Connected`, `ble.SideSeenRecently`, the existing Enable retry, a nil-by-default `ble.OnTap` hook). `-demo` feeds it mock toggles instead. `-plain` keeps today's log-only behavior. No change to Session / keepalive / scan gate / connect logic.

**Tech Stack:** Go, `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/lipgloss`.

**Spec:** the approved in-chat design (conversation 2026-10-09) plus user amendment: TUI is the DEFAULT, plain log mode is an option (flag `-plain`; assumed reading of "tty an option").

## Global Constraints

- HANDOFF.md / AGENTS.md hard-won facts must not regress: both pods get sessions (`add`), nothing gated on pod side, idle reset and keepalive untouched, scan-gated reconnect untouched, `Enable()` retried every 5s while BT off.
- Logging is `log/slog` only. No `fmt.Println`/`log.Printf` (TUI output goes through Bubble Tea).
- In TUI mode stderr logging corrupts the screen: logs go to the `-log` file (default `zwiftboard.log` when TUI is on and `-log` is empty), never stderr.
- Right pod buttons: `Y Z A B PLUS` (from `config.ButtonNames()` filtered to bindings present).
- Both controllers must be on, but only the right one's clicks are mapped; prompts say so.
- Tests next to package; dependency injection, no hardware. Tree is CRLF: edit with the edit tool, never sed on LF assumptions; `gofmt -l` noise from CRLF is known.
- Verification set: `go test ./...`, `go vet ./...`, `GOOS=windows go vet ./...`, `GOOS=windows go build ./...`.
- Conventional commits, one logical step per commit.

## Review Focus

- Bluetooth off while pods are "connected" in a stale poll: BT-off view must hide pods/profile and show only the "turn ON" prompt (Task 1 test).
- Rapid repeated clicks on the same button: flash must extend, not stack or panic (Task 1 test).
- Click for a button absent from bindings: must not crash or add a row (Task 1 test).
- Terminal too narrow / zero-size before first `WindowSizeMsg`: `View()` must not panic (Task 1 test).
- Config missing (`cfg.Missing`) or focus empty: table shows "no key mapping", focus line shows "off" (Task 1 test).
- `-addr` mode: pod sides are `PodUnknown`; poller must still work and not mislabel (Task 4: use sightings only when not `-addr`; in `-addr` mode show pods as detected/connected via `ble.Connected` only, document limitation).

## File Structure

- Create `internal/tui/model.go`: state types, messages, `New`, `Init`, `Update`.
- Create `internal/tui/view.go`: lipgloss styles and `View`.
- Create `internal/tui/model_test.go`, `internal/tui/view_test.go`.
- Create `internal/tui/demo.go`: `DemoKeys` handling (mock toggles) kept separate from live path.
- Modify `internal/ble/buttons.go`: `OnTap` hook. Test in `internal/ble/buttons_test.go`.
- Create `internal/tui/live.go`: `Poll(send func(tea.Msg), ...)` pod poller, importable from main only (this is the one file importing ble/zwift).
- Modify `main.go`: flags `-plain`, `-demo`; split into `startListening`; TUI default.
- Modify `go.mod`/`go.sum`, `README.md`, `AGENTS.md` (Layout line).

### Task 1: TUI model and view (pure)

**Files:**
- Create: `internal/tui/model.go`, `internal/tui/view.go`
- Test: `internal/tui/model_test.go`, `internal/tui/view_test.go`

**Interfaces:**
- Produces:
  - `type PodState int` with consts `PodOff, PodDetected, PodConnected`.
  - `type Config struct { Profile string; Bindings map[string]string /* button -> key token */; Buttons []string /* ordered rows */; Focus string; NoMapping bool; Demo bool }`
  - Messages: `BTMsg(bool)`, `PodMsg{Left, Right PodState}`, `ClickMsg(string)`, unexported `expireMsg`.
  - `func New(cfg Config) Model` (starts with BT=false, pods PodOff); `Model` implements `tea.Model`.
  - `const FlashFor = 300 * time.Millisecond`.

- [ ] **Step 1: `go get github.com/charmbracelet/bubbletea github.com/charmbracelet/lipgloss`; verify `go build ./...` still passes.**
- [ ] **Step 2: Failing tests in `model_test.go`:** `TestUpdateBTMsg` (BTMsg(true) sets bt), `TestClickFlashesMappedButton` (ClickMsg("A") returns a non-nil cmd and `m.Flashing("A")` true; after `expireMsg{button:"A", id: <current>}` false), `TestRapidClickExtendsFlash` (second click bumps id; stale expire for old id leaves it flashing), `TestClickUnmappedIgnored` (ClickMsg("Z") with no Z binding: nil cmd, no flash), `TestQuitKey` (`q` returns `tea.Quit` cmd). Expose `func (m Model) Flashing(button string) bool`.
- [ ] **Step 3: Run `go test ./internal/tui` and confirm FAIL (undefined).**
- [ ] **Step 4: Implement `model.go`.** Flash keyed by button with a per-button counter so stale expiries are ignored; expiry via `tea.Tick(FlashFor, ...)`.
- [ ] **Step 5: Failing tests in `view_test.go`** (strip ANSI with `lipgloss.NewStyle` not needed: set `lipgloss.SetColorProfile(termenv.Ascii)` in `TestMain`): `TestViewBTOffHaltsSetup` (contains "Bluetooth" and "turn", does NOT contain "Left Controller" or the profile name), `TestViewRightDisconnectedPrompt` (BT on, right PodOff: contains "Right controller disconnected: please activate the Right Controller" and not the equivalent Left text), `TestViewBothOffTwoPrompts`, `TestViewTable` (every button and its key token present; text "only the Right controller sends clicks"), `TestViewFocus` (Focus "MyWhoosh" shows it; empty shows "off"), `TestViewNoMapping`, `TestViewZeroSize` (View on `New(...)` before any WindowSizeMsg does not panic).
- [ ] **Step 6: Implement `view.go`: `func (m Model) View() string`.** Panels with `lipgloss.RoundedBorder`; green connected, yellow detected, red off; flashing row gets reverse-video highlight; pod/profile/table panels rendered only when BT is on.
- [ ] **Step 7: `go test ./internal/tui` PASS; `go vet ./...`.**
- [ ] **Step 8: Commit** `feat(tui): pure Bubble Tea status model and view`.

### Task 2: Demo toggles

**Files:**
- Create: `internal/tui/demo.go`; Test: add to `model_test.go`

**Interfaces:**
- Consumes: Task 1 `Model`, `Config.Demo`.
- Produces: in `Update`, when `Config.Demo`: `b` toggles BT, `l`/`r` cycle Left/Right through `PodOff -> PodDetected -> PodConnected -> PodOff`, digits `1..N` fire `ClickMsg(Buttons[i-1])`. When not Demo these keys do nothing. View shows a footer key legend only in Demo.

- [ ] **Step 1: Failing tests:** `TestDemoToggleBT`, `TestDemoCyclePods`, `TestDemoDigitClicks`, `TestLiveIgnoresDemoKeys` (Demo false, `b` leaves bt unchanged).
- [ ] **Step 2: Run, confirm FAIL.**
- [ ] **Step 3: Implement `handleDemoKey(m Model, k tea.KeyMsg) (Model, tea.Cmd)` in `demo.go`, called from `Update`.**
- [ ] **Step 4: Tests PASS.**
- [ ] **Step 5: Commit** `feat(tui): demo-mode toggle keys`.

### Task 3: `ble.OnTap` hook

**Files:**
- Modify: `internal/ble/buttons.go` (var + one nil-checked call after a successful `claimTap` for a mapped button, i.e. only for non-duplicate taps)
- Test: `internal/ble/buttons_test.go`

**Interfaces:**
- Produces: `var OnTap func(button string)` (nil default). Called with the button name (`"A"`), same goroutine as the handler, must be non-blocking for callers (documented).

- [ ] **Step 1: Failing tests:** `TestOnTapCalledOncePerPress` (right and left handlers share a frame as in the existing dedup test; `OnTap` count == 1, restore `OnTap=nil` with `t.Cleanup`), `TestOnTapNilSafe` (existing handler tests unchanged and green), `TestOnTapNotCalledUnmapped`.
- [ ] **Step 2: Run, confirm FAIL.**
- [ ] **Step 3: Implement.** Call goes inside `if claimTap(name) { tap(bnd); if h := OnTap; h != nil { h(name) } }`; do not alter ordering of `tap`, debounce or logging. Comment why: observational only.
- [ ] **Step 4: `go test ./...` PASS (all existing ble tests green).**
- [ ] **Step 5: Commit** `feat(ble): optional OnTap hook for UI observers`.

### Task 4: Live poller

**Files:**
- Create: `internal/tui/live.go`; Test: `internal/tui/live_test.go`

**Interfaces:**
- Consumes: `ble.Connected(zwift.Pod) bool`, `ble.SideSeenRecently(zwift.Pod, time.Duration) bool`.
- Produces: `func PodStates(connected func(zwift.Pod) bool, seen func(zwift.Pod, time.Duration) bool) PodMsg` (pure, injectable; same rule as `pairStatus`: connected > seen within 30s > off) and `func Poll(ctx context.Context, send func(tea.Msg), every time.Duration, connected func(zwift.Pod) bool, seen func(zwift.Pod, time.Duration) bool)` which sends a `PodMsg` only when it changes.

- [ ] **Step 1: Failing tests:** `TestPodStatesPrecedence` (table: connected beats seen; seen -> Detected; neither -> Off; per side), `TestPollSendsOnlyOnChange` (fake funcs, 1ms interval, cancel context, count sends).
- [ ] **Step 2: Run, confirm FAIL.**
- [ ] **Step 3: Implement.** In `-addr` mode pods are `PodUnknown`, so `SideSeenRecently` is false: such pods show Off until connected, then Connected (documented limitation in README).
- [ ] **Step 4: PASS.**
- [ ] **Step 5: Commit** `feat(tui): live pod-state poller`.

### Task 5: Wire `main.go` (TUI default, `-plain`, `-demo`)

**Files:**
- Modify: `main.go`

**Interfaces:**
- Consumes: everything above.
- Produces: flags `-plain` (bool, "plain log output instead of the TUI"), `-demo` (bool, mock TUI, no BLE). `func startListening(cfg config.Config, scanFor, reconnect time.Duration, addrList string, plain bool)` containing the existing registry/`add`/`Watch` block moved VERBATIM (all comments kept). `pairStatus` goroutine runs only in plain mode.

- [ ] **Step 1: Extract `startListening` with no behavior change** (move block lines for registry, `add`, `-addr` handling, `Watch`; keep comments). Run `go build ./... && GOOS=windows go build ./...`; `git diff` must show a pure move. Commit `refactor: extract startListening from main`.
- [ ] **Step 2: Flags and logging.** TUI mode (neither `-plain`): if `-log` empty use `zwiftboard.log`; logger writes to the file only, never stderr. `-plain` keeps the current tee-to-stderr behavior exactly. Error exits before the TUI starts still print via slog to stderr (set stderr logger until the TUI starts, then switch).
- [ ] **Step 3: BT gate in TUI mode.** Run the existing `Enable()` retry loop (same 5s sleep, `ble.Guarded`) in a goroutine; after each failed attempt `p.Send(tui.BTMsg(false))`; on success `p.Send(tui.BTMsg(true))`, then call `startListening`, set `ble.OnTap = func(b string){ p.Send(tui.ClickMsg(b)) }`, and `go tui.Poll(...)` with `ble.Connected` / `ble.SideSeenRecently`. In `-plain` the loop stays inline as today. BLE must not start before BT is on (unchanged semantics).
- [ ] **Step 4: Build `tui.Config`** from `cfg`: `Profile`, `Bindings` (button -> `Token`), `Buttons` = `config.ButtonNames()` filtered to Right-pod set `Y Z A B PLUS` that exist in `zwift.Buttons`, `Focus`, `NoMapping = cfg.Missing`, `Demo = *demo`. `-demo` runs `tea.NewProgram(model, tea.WithAltScreen())` without BLE or `Enable`.
- [ ] **Step 5: Quit.** `q`/Ctrl+C ends the program via `tea.Quit` and `os.Exit(0)` after `Run` returns (sessions are process-scoped today; no new teardown path).
- [ ] **Step 6: Verify:** `go test ./...`, `go vet ./...`, `GOOS=windows go vet ./...`, `GOOS=windows go build ./...`; on Linux `go run . -demo` then manually exercise `b l r 1-5 q`; `go run . -plain` still logs as before (BT enable fails on Linux: retry message appears).
- [ ] **Step 7: Commit** `feat: show TUI by default, add -plain and -demo`.

### Task 6: Docs

**Files:**
- Modify: `README.md`, `AGENTS.md` (Layout: add `internal/tui/`), `HANDOFF.md` (next-step line)

- [ ] **Step 1: README: build/run instructions** (`go build -o zwiftboard.exe .`, `zwiftboard.exe`, `-plain`, `-demo`, log file location, demo key legend, `-addr` limitation, runtime BT-off is not live-detected after startup).
- [ ] **Step 2: AGENTS.md layout line and note "TUI is observational; never gate sessions on it".**
- [ ] **Step 3: Commit** `docs: document TUI, -plain and -demo`.
- [ ] **Step 4: Hardware check note for the user:** run `zwiftboard.exe -p test-notepad`, both controllers on, idle 10+ min, confirm same stability as baseline `0f7872e`.

## Self-Review

- Spec coverage: BT gate (T1, T5), dual-pod prompts (T1), profile + table + flash (T1, T3, T5), focus line (T1, T5), mock toggles (T2), default TUI + `-plain` (T5), run instructions (T6).
- Type consistency: `PodState`, `PodMsg`, `ClickMsg`, `BTMsg`, `Config` names match across tasks; `OnTap` signature `func(string)` consistent.
- Known gaps: runtime BT-off after startup is not observed (existing code has no signal); flagged in README.
