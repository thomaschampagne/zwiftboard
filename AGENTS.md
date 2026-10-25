# AGENTS.md

## What this is

`zwiftboard` — Go CLI that turns Zwift Click V2 controllers (BLE) into
keyboard input on Windows. Runs on Windows only; developed cross-platform.

## Superpowers

- Superpowers skills installed. Before ANY task, check for relevant skill and invoke it. Mandatory, not optional.
- New feature or behavior change: `brainstorming` first. No code before design approved.
- After design: `using-git-worktrees`, then `writing-plans`, then `subagent-driven-development` or `executing-plans`.
- Implementation: `test-driven-development`. RED, GREEN, REFACTOR. Write failing test first, watch fail.
- Bug or failing test: `systematic-debugging`. Root cause before fix.
- Before claiming done: `verification-before-completion`. Run command, show output. Evidence over claim.
- Between tasks: `requesting-code-review`. Critical issues block.
- Branch finished: `finishing-a-development-branch`.
- Principles: YAGNI, DRY, simplicity first.
- If skill seem skipped, say so and invoke it.

## Commands

- Lint, Test, Build (local): `just sanity`
- CI (check-only): `just ci`
- Deps: `just update-deps` (blanket `go get -u ./...`; BLE libs included —
  hardware re-verify before a release with BLE bumps)
- List tasks: `just --list`

No hardware here: `Watch`/`Session` correctness is reviewed, not tested.
Tests cover config parsing, key resolution, the button-handler dedup logic, the
launch classifier/resolver, the window-match helper, the scan-gate/side tracking,
the TUI key-state model, pod states, the log buffer and the config-path tests.

## Layout

- `main.go` (root) — flags, logger setup, target registry, wiring only; a
  single-binary app, so the entry point lives at the repo root (not `cmd/`).
  Go's own docs and most single-binary tools keep `main.go` at the root; the
  `cmd/` convention is for repos with multiple binaries or library+binaries
  combos. Two hard constraints pin this layout: `defaultconfig.go` does
  `//go:embed config.yaml` (embed cannot `..` above its package dir), and
  `tuirun.go`/`defaultconfig.go` are `package main` wiring — package main
  lives where the binary lives.
- `internal/ble/` — scan (`Watch`), connect (`Session`), button decode, hold + cross-session dedup.
  Scan/reconnect windows and the idle-reset are internal defaults now (the old
  `-scan`/`-reconnect`/`-addr`/`-ack`/`-idle-reset` flags were removed)
- `internal/keys/` — config token → Windows VK code; `Down`/`Up`/`ReleaseVK` (keybd_event on windows, no-op elsewhere)
- `internal/launch/` — per-profile `startProgram` launcher (exe/script/MS Store);
  Windows impl (`process_windows.go`) + no-op stub (`process_other.go`); the
  classify/resolve policy lives in `launch.go`
- `internal/config/` — parses the config: top-level `loglevel:` and
  `defaultProfile:` (the startup profile when `-p` is absent; unknown name is
  fatal), a `profiles:` map of button→key, plus the per-profile
  `focusProgramNamePrefixOnClick` and `startProgram`; returns errors (never exits)
- `internal/zwift/` — protocol facts: GATT UUIDs, button bits, frame decode, label helpers
- `internal/tui/` — Bubble Tea **v2** (`charm.land/bubbletea/v2`; lipgloss stays
  v1) status screen (default UI; `-plain` = logs). OBSERVATIONAL
  only: fed by `ble.Connected`/`SideSeenRecently`/`ble.OnKey`; never gate a session on it.
  Handles the profile-switch chords (`ctrl+left`/`ctrl+right`, fallbacks
  `ctrl+n`/`ctrl+p`), plus `r` reload config, `s` start the profile's program,
  `e` edit config, `l` toggle the log panel and `q` quit.
  `tuirun.go` (root) wires it; in TUI mode slog goes to the log file, not stderr

## Conventions

- Conventional commits, one logical step per commit.
- Logging is `log/slog` only — no `log.Printf`, no `fmt.Println` (the sole
  exception is `-version`, which prints to stdout). Level comes
  from `loglevel:` in config.yaml; `-v` forces debug. Raw/diagnostic lines are
  `Debug`, lifecycle `Info`, recoverable problems `Warn`, startup failures
  `Error` + `os.Exit(1)`.
- Comments explain protocol facts and _why_ (there are many device quirks);
  keep them when moving code.
- Tests live next to their package; keep `buttonHandler`-style dependency
  injection (down/up funcs as parameters) so logic stays testable without
  hardware.
- Releases publish to `ScoopInstaller/Extras` via a `scoop` job in
  `.github/workflows/release.yml`. It runs only on a `main` push that
  semantic-release turns into a NEW release (the `release` job's `published`
  output); it downloads that release's `checksums.txt`, renders the manifest
  with `just scoop-manifest VERSION SHA256` (the same recipe used locally) into
  `extras/bucket/zwiftboard.json`, and opens/updates a PR against
  `ScoopInstaller/Extras:master` via `peter-evans/create-pull-request` pushed to
  the `SCOOP_EXTRAS_PAT` owner's fork (classic PAT, `repo` scope; the owner must
  have forked `ScoopInstaller/Extras`). Nothing is pushed to Extras directly — a
  human merges the PR. A missing `SCOOP_EXTRAS_PAT` fails the job fast with an
  actionable message. After opening the PR the job posts the `/verify` comment
  the CONTRIBUTING guide requires (fork PRs are not auto-verified on open). The
  very first manifest is a NEW package: Extras wants a package-request issue
  approved before the initial PR, which this automation cannot file for you.
  A separate `bucket` job (same trigger, no PAT) renders the same manifest into
  `bucket/zwiftboard.json` and commits it straight back to `main`, so the repo
  itself works as a personal scoop bucket
  (`scoop bucket add zwiftboard https://github.com/thomaschampagne/zwiftboard`).
  Release assets are
  `zwiftboard.exe` / `zwiftboard.zip` / `checksums.txt`; emulate the build +
  manifest locally with `just scoop-release <version>` (needs `zip` +
  `sha256sum`).

## Hard-won facts (do not regress)

- WinRT returns a partial GATT service list right after connect: enumerate
  ALL services with retry and match characteristics by UUID (`connect`).
- The Click pair MIRRORS button state, but ASYMMETRICALLY: the RIGHT pod's
  buttons arrive as the same frame from both units, the LEFT pod's own
  buttons ONLY from the left unit (seen in zwiftboard.log, 2026-10-21). Dedup
  is edge-triggered per handler: only bits that changed since the previous
  frame act, so an identical mirrored bitmap changes nothing.
- A synthetic `keybd_event` down has NO OS auto-repeat: it is a single event
  and the keyboard driver does not repeat it for you. Games that poll the key
  state see the hold, but text apps (Notepad, chat) need the repeated
  WM_KEYDOWN a physical key produces while it is held down — so `Down`
  re-sends the press (250ms delay, ~33ms interval) until `Up`. Do not regress
  this or held letters stop repeating in text fields.
- The Click V2 is a two-pod PAIR, not two independent remotes: the LEFT pod is
  the pair's BLE anchor and the RIGHT pod mirrors state to it over a private RF
  link. Both pods are connected (see BOTH PODS ARE CONNECTED); both pods'
  buttons are mappable (LEFT UP RIGHT DOWN MIN on the left pod, Y Z A B PLUS on
  the right); the shipped profiles map the right pod, the `notepad` profile
  maps both. A lone RIGHT pod's LED keeps blinking (advertising mode) even
  though its frames decode fine — cosmetic, not a fault.
- Handshake trio (`RideOn 02 03`, `00 08 00`, `00 08 10`), then a keepalive
  every 3s re-sending the raw `RideOn` opcode frame. PROVEN NOT TO HOLD A LONE
  RIGHT POD: on Windows the link still dies ~65s after the pod's LAST OWN
  TRANSMISSION (a button frame), deterministically (~64-68s), no matter the
  payload — plain `RideOn`, `RideOn 02 03`, `00 08 10`, `00 08 00`, with and
  without ±5s keepalive jitter all failed identically. The right pod's idle
  watchdog is reset only by its own inbound frames; nothing we write to sync-rx
  resets it, and there is no unlock/firmware hook for the lone right pod. The
  keepalive's real job is LIVENESS: a successful write proves the session is up,
  a failed write is the ONLY disconnect signal (idle 0x23 silence is normal),
  and a 3s write cadence also keeps us inside WinRT's connection timer.
- RECONNECT IS SCAN-GATED, never address-hammered. `Connect(addr)` on a sleeping
  pod leaks one Windows `GattSession` with `SetMaintainConnection(true)` per
  attempt (tinygo gap_windows.go creates it unconditionally and only
  `Disconnect()` releases it, which never runs for a device that didn't
  physically connect); hundreds of leaked sessions over a long outage wedged the
  Windows BLE stack and stalled recovery ~54 minutes. The scanner records every
  sighting (`markSeen`), and a controller's session goroutine only calls
  `Session` when it was seen advertising within the reconnect window (default 30s); a
  fresh sighting means the pod is awake, the connect succeeds, and nothing
  leaks. A session END also clears the sighting (`ClearSighting`): the pod may
  advertise all session long (keeping the gate open), so without the clear a
  drop would be followed by retries of the now-sleeping pod by address.
- PERIODIC POD RESET defeats the ~65s idle sleep (default 55s):
  after that much button silence the keepalive tick writes a lone `0x18`
  (`resetPod`) to sync-rx instead of the RideOn ping. The pod reboots ON
  OUR SCHEDULE, before its ~65s watchdog strands it asleep for 30-40s; it
  re-advertises within seconds and the scan gate reconnects. Activity (any
  received frame) restarts the idle clock and suppresses the reset, so a
  session in use is never rebooted.
- THE WRITE-LIVE ZOMBIE (seen on the LEFT pod, 2026-10-21 log): a pod's app
  can die (LED sleep) while its BLE stack keeps ACK-ing our writes — the
  stream stops, `Session` never returns, nothing logs. The scheduled `0x18`
  reset is swallowed by it, and a rebooted pod can revive on the maintained
  link WITHOUT notifications. So the keepalive loop recycles the session
  when it hears NOTHING for `PostResetGrace` (15s) after the reset it sent
  (`postResetStale`): the caller tears down and the scan gate reconnects for
  a fresh handshake + CCCD. A single frame after the reset (the `0xFF`
  challenge marks a revived pod) cancels the recycle. Pre-reset idle
  silence still NEVER ends a session.
- Button frame `0x23` + protobuf field 1 = bitmap, **0 = pressed**; bits are
  the Click V2 set in `internal/zwift.Buttons`, not the Zwift Ride layout.
- The held state is a per-VK COUNT per handler (`Holds.own`), not a flag: two
  different buttons of the SAME handler mapped to the SAME key (the mirrored
  `Z` plus the left pod's own `LEFT`, both bound to `left`) must hold the key
  until the LAST of them is released. With a flag the first release wiped it
  and the second release found nothing to lift — the key stayed down for the
  whole process. Every path gives back exactly what it took: `Press` counts
  up, `Release` counts down, the `Press` rollback counts down (it must never
  delete the whole VK), `ReleaseAll` subtracts the handler's whole count.
- LEFT controller unlock: ONE Zwift-app handshake GRANTS a persistent unlock
  (the bare `ff 04 00` only keeps an already-unlocked pod happy). NOT a ~24h
  lease: one unlock (2026-10-22) still held through the 2026-10-24 (~morning)
  and 2026-10-25 (11:54) sessions, the second after ~26h of complete non-use
  — the "~24h" figure was folklore (clickbridge: multi-day rides with no app;
  BikeControl #68: one app session fixed it permanently for several users).
  The 2026-10-21/22 lockouts were a NEVER-unlocked pod (the first-ever unlock
  came AFTER those failures), not an expired one. A LOCKED pod's engine dies
  ~45-65s after the unlock echo while status `2A`/battery `FF 05`/heartbeat
  `19 10 50`/RideOn echo frames keep flowing — invisible to any
  frame-activity clock, writes still succeed, nothing logs; the lock is
  permanent for the session (status frames keep the idle clock fresh, so the
  scheduled `0x18` reset never fires). The proposed lock trigger — the
  challenge count — is FALSIFIED: on 2026-10-25 13:36 the proven-stable
  c1ac085 binary logged a demonstrably HEALTHY LEFT pod sending TWO connect
  challenges ~15.5s apart (different EC keys) and then 535 button frames over
  4 minutes. A second challenge is therefore NOT a lock precursor; arming the
  lock-reset net on that count reboot-looped healthy pods (user-reported
  regression). The net (`resetTick`) ships DISABLED (`LockChallenges=0`), so
  the tick is exactly c1ac085's idle-only `keepaliveTick`. The `challenges`
  count stays observational in the liveness breadcrumb; re-enable the net only
  on a signal that provably correlates with the locked state.
  The `AckInterval` (15s) ladder does NOT renew the unlock (disproven
  2026-10-22: gaps ≤18s still lost the engine; Run A's "last-ack + 30.03s"
  was coincidence). The challenge frame is an 85-byte SIGNED message:
  `FF 03 00` + protobuf {field1: 33-byte SEC1 compressed EC point, field2:
  varint 33751040, field3: 40-byte signature} — the old "03→02 countdown" was
  a misread of the EC point prefix across different challenges (the old docs
  quoted 6-byte prefixes of full 85-byte frames). Open: whether the unlock
  survives battery removal or a `0x18` reboot (never exercised on an
  unlocked pod); re-run the Zwift-app unlock only if a pod factory-locks.
- Liveness: the keepalive write is the ONLY signal. The old LEFT-anchor silence
  watchdog (`podWatcher`) was removed — an idle pod legitimately stops streaming
  `0x23` frames, and idle silence must NEVER end a session (the controller must
  stay usable after a long idle for virtual shifting). A session ends only when a
  keepalive write actually fails (a real disconnect); the caller reconnects. The
  `0xFF` challenge still arrives on the ASYNC characteristic, not sync-tx, and
  the `0xFF` 03 challenge is answered immediately (`ff 04 00`) so an unlocked pod
  keeps streaming.
- Scanning runs in bursts forever (`Watch`): never Fatal when nothing is
  found, never stop looking for a controller that appears 10 minutes later.
- `connect` is serialized (`connectMu`); `onFound` fires after the scan burst
  so connects do not overlap an active scan.
- Teardown must PROBE before `Device.Disconnect()`: when BT goes off, tinygo's
  `ConnectionStatusChanged` handler already ran `Disconnect()` and freed the
  GATT session; calling it again is an unrecoverable AV (0xc0000005). Only
  WRITES fail gracefully on a dead session — service discovery does NOT
  (DiscoverServices AVs on the freed COM
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
  left one (an early "wait for the left pod" pair gate, since removed), both
  still dropped the right pod ~67-72s after its last button. Do not stop
  connecting the left pod. The pod side is in the advertisement (Zwift
  manufacturer record 0x094A, first byte:
  `0x0B` LEFT, `0x0A` RIGHT — `zwift.PodSide`); it is used for the
  OBSERVATIONAL pair status only — the TUI derives it from `ble.Connected` /
  `ble.SideSeenRecently` via `tui.PodStates` (per side: not detected / detected /
  connected, "ready" when both are connected).
  Never gate a session on it: a connected pod may stop advertising, so a gate
  on the other side's sighting can deadlock a reconnect.
- Startup: Bluetooth off is not fatal — `Enable()` is retried every 5s with a
  "turn it ON" message until the radio is on.
- Profile switching is a LIVE key-map swap, never a BLE reconnect: the TUI calls
  `ble.KeyMap.Swap` (in place) + `ble.ReleaseAllHeld` (no stuck key) +
  `keys.SetWindowTarget`, and re-reads the config from disk so button map,
  `focusProgramNamePrefixOnClick` and `startProgram` apply immediately.
  `keys.windowTarget` is an `atomic.Pointer[string]` for this reason: the TUI
  goroutine retargets it while BLE handler goroutines read it. A reload error is
  shown on the status screen and the current profile stays active. `loglevel:`
  is NOT hot-reloaded.
