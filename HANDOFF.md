# HANDOFF

## Current state (2026-10-31, shortcut Ctrl migration)

Uncommitted. Footer shortcuts moved to Ctrl; bare-key bindings removed.
`just sanity` green; golangci-lint shows only the 12 pre-existing errcheck.

- All status-screen shortcuts need Ctrl: `ctrl+l` logs, `ctrl+e` edit, `ctrl+r`
  reload, `ctrl+s` start, `ctrl+z` sign in, `ctrl+m` mode toggle, `ctrl+q` quit
  (`ctrl+c` kept), `ctrl+←/→` profile. Bare-key bindings removed.
- Footer labels are short and fit on one line at the 136-cell card width;
  `wrapSegments` is the fallback for narrow terminals, so no shortcut is dropped.
- Terminal conflicts: bubbletea runs `term.MakeRaw`, so Ctrl+Z/S/Q reach the app.
  Ctrl+M arrives as `enter` on legacy terminals (`\r`) and as `ctrl+m` only where
  the terminal reports modified keys (kitty / modifyOtherKeys). In a legacy
  terminal the mode toggle does nothing; Enter never toggles it.
- Demo keys (`b`, `f`, `[ ]`, `1-0`) are still bare; demo-only, not changed.

### Next steps

- Hardware/terminal check: confirm `ctrl+m` reaches the app in Windows Terminal
  (Windows console may report it as Enter).
- Consider a fallback key for legacy terminals if that check fails.

## Previous state (2026-10-31)

Branch `feat/left-controller-support-zwift-sign-in`. Per-profile controller
modes are implemented and tested. `just sanity` green; `go test -race ./...`
green; Windows cross-build green; golangci-lint reports the same 12 errcheck
findings that already exist on the branch.

### What changed

- **YAML** — each profile has `single:` (right pod only) and `double:` (both
  pods). Per-profile `focusProgramNamePrefixOnClick` / `startProgram` stay at
  profile level. Legacy flat profiles load as `single` (right-pod buttons only; all buttons
  in `double`) and log a warning. A left-pod button under `single:` is an error.
  Config files over 1 MiB are refused.
- **Mode** — `config.Mode` (`single`/`double`), default `single`, set on the
  shared `profileState` (`profile.go`). The `m` key (`toggleModeKey` in
  `internal/tui/model.go`) toggles it; the KeyMap swaps to the other map and
  held keys are released. The mode survives profile switches.
- **TUI** — header shows `SINGLE`/`DOUBLE` in the green chip; footer shows
  `m Toggle mode`; the Keys Bindings panel lists the right pod only in single
  mode, and both controllers under `Left controller` / `Right controller`
  headings in double mode.
- **Shipped `config.yaml`** — every profile migrated: `single` = right-pod
  buttons, `double` = left + right. No left-pod entries are missing, so no
  "still to define" comments were needed.
- **Docs** — README "Controller modes" section, `m` in the key list, updated
  example.

### Review findings (fixed)

- Removed an unneeded `copyMap` (the legacy `double` shares the read-only map).
- Replaced the `mapsLeftPod` heuristic with an explicit mode. Single mode no
  longer shows the left column just because a left button is bound.
- Header test: staticcheck De Morgan nit fixed.

### Open, not changed

1. **Legacy warning fires on every load** (each switch / reload). Harmless, but
   noisy for legacy users until they migrate.
2. **Toggling releases held keys** and has no confirmation. Deliberate: the
   status line says `Mode: …`, and the held rows clear.
3. **Header title** keeps the existing lowercase `zwiftboard` (spec said
   `Zwiftboard`); left as-is to avoid changing existing output.
4. **Debug logs contain the server reply** (see previous notes below).
5. **Lifetime copy conflict**: `login.go`/`tuirun.go` say "a few days", AGENTS.md
   says "~24h". Confirm before changing.
6. **Not verified on hardware**: mode switching with real BLE traffic
   (`ble.ReleaseAllHeld` + `KeyMap.Swap` are unit-level only).

### Next steps

- Hardware check: press `m` while a left-pod button is held; confirm no key sticks.
- Decide on open item 1 (legacy warning frequency) and item 5 (lifetime copy).

### Earlier notes (review against `dev`, 2026-10-30)

- Recovery now calls `writeDefault` (no legacy migration), test
  `TestRecoverConfigIgnoresLegacyConfig`.
- `Client.Login` no longer holds the mutex across the Zwift HTTP call; test
  `TestLoginDoesNotBlockReadyWhileWaitingForZwift`.
- Debug logs of `dlock response` include the full reply in hex. Decide: keep at
  Debug only, or log length + hash.
- Replay experiments (Experiment C) are research-only.
