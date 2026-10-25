# Single/both-controller profiles — Design

**Date:** 2026-10-25
**Status:** draft, awaiting approval

## Intent and why

The shipped profiles currently map only the RIGHT pod. The LEFT pod is
connected and its buttons are mappable, but only `notepad` uses them. A rider
who has done the one-time Zwift-app unlock wants the LEFT pod to behave like a
real controller (its arrows steer / navigate, its `MIN` shifts down) while the
RIGHT pod keeps the game-specific actions. A rider who has not unlocked the
LEFT pod (or simply prefers the right pod) must keep today's behavior.

So every profile ships two binding sets:

- `single` — today's map: RIGHT pod only. Works even if the LEFT pod was never
  unlocked.
- `both` — a controller-shaped map: LEFT arrows are literal arrow keys,
  `MIN`/`PLUS` are the game's gear-down/up keys, and the RIGHT pod's
  `Y` `Z` `A` `B` are the game's other real shortcuts.

The user picks the set with a startup flag or toggles it live from the status
screen. **Both pods are always connected in either mode** (the LEFT pod is the
pair's BLE anchor; dropping it drops the RIGHT pod's link too).

`zwift` and `rouvy` profiles are removed.

## Decisions (the things the user decided)

1. Both pods connected always, even in `single` mode — no change to the BLE
   connection logic.
2. Selection is explicit: a `-controllers single|both` flag at startup and a
   status-screen shortcut to toggle live. No auto-detection from the connected
   pods.
3. `both` is not "duplicate the right pod": LEFT arrows are arrow keys,
   `MIN`/`PLUS` gear, the RIGHT pad's other four buttons use other real
   shortcuts from that program.
4. Sessions are never reconnected by a mode switch: it is a live key-map swap
   (same as a profile switch).
5. Tell the user: connect the pair to the Zwift app **once** to unlock the LEFT
   controller; the unlock is persistent. Until then, `both` mode still works
   but only the RIGHT pod types.

## Config schema

Each profile keeps its metadata at the profile level and gains two child maps:

```yaml
profiles:
  mywhoosh:
    focusProgramNamePrefixOnClick: "MyWhoosh"
    startProgram: "MyWhooshTechnologyService...!MYWHOOSH"
    single:
      PLUS: k
      A: right
      B: i
      Y: u
      Z: left
    both:
      LEFT: left
      UP: up
      RIGHT: right
      DOWN: down
      MIN: k
      PLUS: i
      Y: u
      Z: 1
      A: 2
      B: 7
```

- `focusProgramNamePrefixOnClick` and `startProgram` stay at the profile level
  (they are not per-mode).
- **Every profile must define both `single` and `both`.** A missing or empty
  map is an error naming the profile and the missing block. This keeps profile
  cycling total in either mode: a `ctrl+←/→` step can never land on a profile
  that cannot load the active mode.
- The old flat layout (buttons directly under the profile name) is rejected
  with a migration message: move the buttons under `single:` and add a `both:`
  map.
- Unknown keys inside a profile are still errors, so a typo cannot be silently
  ignored.
- `notepad` is the deliberate exception: its `both` map keeps the letters
  `l u r d m` (it exists to prove the left pod types in a text field; arrow keys
  would move the caret instead of showing a character).

## Loader

`internal/config`:

```go
type Mode string
const (
    ModeSingle Mode = "single"
    ModeBoth   Mode = "both"
)
func ParseMode(s string) (Mode, error) // "" -> ModeSingle

// Load now takes the mode; Config gains Mode.
func Load(path, profile string, mode Mode) (Config, error)
```

- `Config.Bindings` is the selected map; `Config.Mode` is echoed for the UI.
- `ProfileList` / `ProfileNames` keep their signatures (names only).
- Parsing uses a per-profile struct with `single`, `both`, the two metadata
  keys and an `,inline` catch-all used only to reject legacy/unknown keys.

## Runtime mode

- Flag: `-controllers single|both` (default `single`). Invalid values are a
  fatal config error with the valid choices. `single` is the default because it
  is exactly today's behavior (RIGHT pod only); `notepad`'s `both` map is still
  covered by a unit test and is exercised by running `-controllers both`.
- TUI: `c` toggles the mode (`Ctrl+C` stays quit). The toggle runs in a Cmd,
  never inline: it calls a new `profileState.toggleMode`.
- `profileState` stores the active `config.Mode`. `apply` loads the current
  profile with that mode, calls `ble.ReleaseAllHeld` then `ble.KeyMap.Swap`,
  and rebuilds the VK reverse index exactly as a profile switch does. A failed
  load keeps the current map AND the current mode.
- A mode toggle never launches `startProgram` (it is not a profile switch) and
  sets `ProfileMsg.ModeChanged`. The model then shows a mode-specific status:
  `Controllers: both — left pod needs a one-time Zwift-app unlock.` or
  `Controllers: single — right pod only.`
- Profile switches and `r` reloads keep the active mode.
- `tui.ProfileMsg` gains `Mode`; the model stores it and the view shows
  `controllers: single` / `controllers: both` next to the profile.
- Footer hint: `c` = `both ctrls` when in single mode, `single ctrl` when in
  both mode.
- Startup logs `profile loaded ... controllers=single|both`.

## Status hint for the one-time unlock

When `controllers: both` is active, the bindings card shows a muted note:
`Left pod needs a one-time Zwift-app unlock — until then only the right pod types.`
This is documentation in the UI, not a gate: we cannot read the unlock state.

## Profile maps

`single` = the current RIGHT-pod map (unchanged). `both` follows the rule above.
Sources are the program's official shortcut list; a button with no documented
action is left unmapped rather than inventing a key.

### mywhoosh / mywhoosh-hd

`both`:

| Button | Key | Action |
|---|---|---|
| LEFT / UP / RIGHT / DOWN | left / up / right / down | arrows (left/right steer) |
| MIN | k | gear down |
| PLUS | i | gear up |
| Y | u | minimal UI |
| Z | 1 | peace emote |
| A | 2 | wave emote |
| B | 7 (mywhoosh) / h (mywhoosh-hd) | thumbs-up emote / hide UI (HD only) |

### trainerroad

`both`:

| Button | Key | Action |
|---|---|---|
| LEFT / UP / RIGHT / DOWN | left / up / right / down | resistance / intensity arrows |
| MIN | down | intensity down |
| PLUS | up | intensity up |
| Y | h | heart-rate graph |
| Z | w | workout text |
| A | space | play / pause |
| B | t | trainer mode |

### systm

`both`:

| Button | Key | Action |
|---|---|---|
| LEFT / UP / RIGHT / DOWN | left / up / right / down | arrows (up/down intensity) |
| MIN | down | intensity down |
| PLUS | up | intensity up |
| Y | m | mute |
| Z | esc | close settings |
| A | space | pause / play |
| B | `` ` `` | ERG toggle |

### trainingpeaks

`both`:

| Button | Key | Action |
|---|---|---|
| LEFT / UP / RIGHT / DOWN | left / up / right / down | turn / tactical / action menu |
| MIN | - | resistance down |
| PLUS | = | resistance up |
| Y | g | power/HR/cadence graph |
| Z | u | U-turn |
| A | h | toggle HUD |
| B | 1 | behind camera |

### biketerra

`both`:

| Button | Key | Action |
|---|---|---|
| LEFT / UP / RIGHT / DOWN | left / up / right / down | menu / steer |
| MIN | s | gear down |
| PLUS | w | gear up |
| Y | u | toggle UI |
| Z / A / B | — | no other documented shortcut |

### tacx

`both`:

| Button | Key | Action |
|---|---|---|
| LEFT / UP / RIGHT / DOWN | left / up / right / down | views / difficulty |
| MIN | down | difficulty down |
| PLUS | up | difficulty up |
| Y | space | pause / resume |
| Z | left | previous view |
| A | right | next view |
| B | — | no other documented shortcut |

### notepad

`single`: `PLUS p`, `A a`, `B b`, `Y y`, `Z z`.
`both`: the same plus `LEFT l`, `UP u`, `RIGHT r`, `DOWN d`, `MIN m` (typing
test; deliberate exception to the arrow rule).

## Files touched

- `config.yaml` — nested maps, drop `zwift`/`rouvy`, header notes.
- `internal/config/config.go` — mode, nested parsing, validation.
- `internal/config/config_test.go` — fixtures moved under `single:`; new mode
  tests (selection, missing mode, legacy flat, unknown key).
- `main.go` — `-controllers` flag, pass mode, startup log.
- `profile.go` — mode in `profileState`, `toggleMode`, load with mode.
- `tuirun.go` — wire `ToggleMode`, pass mode to `tui.Config`.
- `internal/tui/model.go`, `view.go`, `model_test.go`, `view_test.go` — mode
  field, `c` key, status line, unlock hint.
- `profiles_test.go`, `defaultconfig_test.go`, `tuirun_test.go` — shipped set,
  both-mode loading, README table, toggle behavior.
- `README.md`, `AGENTS.md`, `internal/zwift/protocol.go` — flag, shortcut,
  schema and one-time-unlock documentation; drop zwift/rouvy.

## Tests

- Loader: `single` vs `both` select the right map; a profile missing or
  emptying either block errors; legacy flat layout errors with a migration
  hint; an unknown profile key errors; focus/start still parse at profile
  level.
- Shipped config: every remaining profile loads in BOTH modes; `zwift` and
  `rouvy` are gone; README rows match the shipped set; notepad `both` still
  asserts `l u r d m`; each game `single` maps the five right-pod buttons it
  uses; each game `both` maps the four left arrows and `MIN`/`PLUS`.
- TUI: `controllers: single|both` renders; `c` invokes the toggle callback;
  `ProfileMsg` updates the displayed mode and bindings.
- profileState: toggling swaps the live map without a profile change and keeps
  the profile name; a failed load leaves mode and map untouched.

No hardware is needed; `Watch`/`Session` are untouched.

## Non-goals

- No change to BLE connection, keepalive, reset or dedup behavior.
- No auto-selecting the mode from which pods are connected.
- No per-profile default mode or config key; the flag is the only persisted
  choice for the run.
- No new key tokens or game actions beyond documented shortcuts.
- No `zwift`/`rouvy` replacement profiles.

## Risks / open

- A user with an existing flat config gets a hard error after upgrading. The
  error names the problem and the fix; the config is embedded, so a fresh
  install is unaffected. Accepted (the format change was requested).
- The LEFT pod may not type if the one-time Zwift unlock was never done. This
  cannot be detected reliably, so it is documentation + a status hint, not a
  gate.
- Tacx/Biketerra document few "other" shortcuts, so some RIGHT-pod buttons are
  intentionally unmapped in `both`. Adding an invented key would violate the
  user's rule; leave them out until a real shortcut is confirmed.
- Mode toggling reloads the config from disk (same as a profile switch). A
  broken file keeps the current map and mode, shown on the status line.
