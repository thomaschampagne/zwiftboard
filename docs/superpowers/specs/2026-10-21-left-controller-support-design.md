# Left Click V2 Controller Support — Design

**Date:** 2026-10-21
**Status:** approved in chat (brainstorming) + builder check — POC scope
**Branch:** `feat/left-controller-support` (worktree), merged back into
`feat/2-controllers-support` when done

## Intent and why

The Zwift Click V2 is a **pair** of controllers. The RIGHT pod's buttons
(`Y Z A B PLUS`) are mapped and working today. The LEFT pod owns five buttons
of its own (`LEFT UP RIGHT DOWN MIN`) and they do nothing: they are not mapped
in any profile, and the status screen has no row for them.

This change makes the LEFT pod a first-class controller: its buttons are
mappable in a profile, each press types exactly **one** key that is released
exactly **once**, and the press is visible on the status screen. The RIGHT
pod's behaviour must be identical to today.

The trigger is a stable POC, not a production release: the mapping lands in the
`notepad` profile only, so it can be exercised on real hardware immediately.
Productionization (mapping the other profiles, README table, release) is a
later pass.

## Evidence (hardware, 2026-10-21)

From the user's run (`zwiftboard.log`):

```
found controller name=zwift/10:21 addr=D4:06:0F:93:10:21 deviceID=10 rssi=-59
found controller name=zwift/86:04 addr=D4:06:0F:A9:86:04 deviceID=11 rssi=-62
connected, handshake sent, listening            controller=zwift/10:21
connected, handshake sent, listening            controller=zwift/86:04
button controller=zwift/86:04 button=MIN   state=pressed
button controller=zwift/86:04 button=MIN   state=released
button controller=zwift/86:04 button=RIGHT state=pressed
...
```

`deviceID` is the Zwift manufacturer record's first byte: `0x0A` = RIGHT
(`zwift/10:21`), `0x0B` = LEFT (`zwift/86:04`).

Consequences, and they shape the whole design:

- **The LEFT pod's `0x23` frames already arrive and already decode.**
  `internal/zwift.Buttons` carries the left bits (`LEFT 0x1`, `UP 0x2`,
  `RIGHT 0x4`, `DOWN 0x8`, `MIN 0x100`) and `internal/config`'s
  `knownButtons` is derived from that map, so `LEFT/UP/RIGHT/DOWN/MIN` are
  already valid YAML button names. **No BLE, transport, handshake or protocol
  change is needed.** The gap is mapping + visibility, not plumbing.
- **The mirroring is asymmetric.** The RIGHT pod's buttons appear on *both*
  controllers' streams; the LEFT pod's own buttons appear **only** on the left
  controller's stream (the right controller logged no button line). The
  existing refcount dedup (`internal/ble/buttons.go`) handles both shapes: a
  left-only press arrives at one handler, a right-pod press at two.
  This asymmetry is a protocol fact and gets an `AGENTS.md` bullet.

## Goals

1. The LEFT pod's five buttons can be mapped in a profile and type their key.
2. Each press types **one** key and releases it **once** — no double-fire, no
   stuck key — including when two different buttons map to the same key.
3. The status screen shows the LEFT pod's bindings and lights up on press.
4. Nothing about the RIGHT pod, the BLE transport, or the reconnect logic
   changes.

## Non-goals

- Mapping the left pod in the other nine profiles (that is the production
  pass; only `notepad` changes now).
- Rewriting the README's profile table, the release flow, or the scoop bucket.
- The legacy flat-config migration (the second item in `todo.md`).
- Any change to the pod pair protocol, the `0xFF` challenge handling, the
  handshake trio, the keepalive cadence, the idle pod reset, the scan-gated
  reconnect, or the teardown probe. **These are hard-won facts — do not
  regress them.**

## Anti-goals

- "It works on my machine" — the LEFT pod must not need a special case in the
  BLE layer. The same code path serves both pods.
- Any change that makes the *existing* (right-pod) path behave differently, or
  that changes the screen of a profile that does not use the left pod.

## Constraints

- Go ≥ 1.27, `just sanity` (fmt, vet, test, build) must be green.
- Logging is `log/slog` only. No `fmt.Println`/`log.Printf`.
- Tests live next to their package; keep the dependency-injection seams
  (`ButtonHandler`'s down/up params) so logic stays testable without hardware.
- Conventional commits, one logical step per commit.
- `config.yaml` is `//go:embed`ed by `defaultconfig.go`: editing it edits the
  **shipped default**. `ensureConfig` never overwrites an existing user
  config, so the POC's new bindings only reach a user who passes
  `-config config.yaml` (or copies the file) — this must be in the test
  instructions.

## The design

### Part 1 — `notepad` profile maps both pods (`config.yaml`)

Only the `notepad` profile changes. The left pod's buttons are listed first,
then the right pod's, so the file mirrors the two-column table:

```yaml
  # Start notepad
  notepad:
    LEFT: l           # left pod: arrow left
    UP: u             # left pod: arrow up
    RIGHT: r          # left pod: arrow right
    DOWN: d           # left pod: arrow down
    MIN: m            # left pod: minus
    PLUS: p
    A: a
    B: b
    Y: y
    Z: z
    focusProgramNamePrefixOnClick: "Notepad"
    startProgram: "notepad.exe"
```

Letters, not arrows, on purpose: the right pod already maps to letters
(`A→a B→b Y→y Z→z PLUS→p`), so the left pod's `l u r d m` are symmetric and
the character is *visible* in the Notepad text box (an arrow key only moves
the caret).

The header comment block above `profiles:` (currently "BOTH PODS CONNECTED,
RIGHT POD MAPPED: … Only the RIGHT pod's buttons (Y Z A B PLUS) are mapped
below.") must be corrected — it is the file's own description of the feature.

### Part 2 — the status screen shows the left pod

`internal/tui`'s key table renders one row per button, and the row list comes
from `tui.Config.Buttons`, which `tuirun.go` fills with a hardcoded
`rightButtons = []string{"Y", "Z", "A", "B", "PLUS"}`. A left-pod press would
therefore type its key but show **no row at all** — invisible on the default
UI.

The table becomes **two columns**, the left pod's rows on the left, the right
pod's on the right, mirroring the hardware and keeping the screen exactly as
tall as today.

**Layout decisions (mine, from the builder check):**

- **No column-header row.** The button names identify the pod by themselves
  (`LEFT UP RIGHT DOWN MIN` vs `Y Z A B PLUS`), and the status card above
  already names "Left Controller" and "Right Controller". A header line would
  cost a screen line for nothing.
- **No wrapping, at any width.** A cell is `name(6) + "→ "(2) + keycap(11) +
  "  " + status(12..14)`. The keycap field is 11, not 9: the keycap style
  carries its own one-space padding on each side (`sKeycap = …Padding(0, 1)`).
  The status field is **12 idle and 14 when it carries a label** — the label
  styles (`sDown`/`sHold`/`sReleased`) have `Padding(0, 1)` too, so the label
  is 14 cells, not 12. A row is therefore **31 cells unmapped, 33 idle, 35
  labelled**, and the cell is sized for the widest case: **35**. The
  two-column table needs `35 + 2 + 35` = **72** cells of inner width. The
  card's inner width is `min(terminal, 120) - 4` (minimum 36).
  **Rule:** render side by side when the inner width is ≥ 72; below that,
  stack the two blocks vertically (left pod's rows, then the right pod's) so
  nothing ever wraps into a taller table. The stacked fallback is taller, but
  only on a terminal too narrow for the side-by-side form. (Widths measured
  with `ansi.StringWidth`, not guessed. The status cell keeps its current
  rendering on purpose: the right-pod-only screen must stay byte for byte
  the same.)
- **The left column appears only when the active profile maps at least one
  left-pod button.** The other nine profiles keep today's screen byte for
  byte — the table is the *profile's* bindings, and a profile that does not
  use the left pod gets no left column. (The right column is always shown, as
  today, with `—` for unmapped buttons.)

**Where the pod split lives:** `internal/zwift/protocol.go` — the split is a
protocol fact and belongs with the other protocol facts:

```go
// LeftButtons / RightButtons are the Click V2 pod button names, in table order.
var LeftButtons  = []string{"LEFT", "UP", "RIGHT", "DOWN", "MIN"}
var RightButtons = []string{"Y", "Z", "A", "B", "PLUS"}
```

The bit→name map `Buttons` stays as it is. A test asserts the two lists and
the bit map agree exactly, so a future bit cannot be added to one and
forgotten in the other.

**Files:**

- `internal/zwift/protocol.go` — add `LeftButtons` / `RightButtons`.
- `internal/tui/model.go` — `Config.Buttons []string` becomes
  `Config.LeftButtons []string` + `Config.RightButtons []string`.
- `internal/tui/view.go` — `table()` renders the two columns (or the stacked
  fallback); the card title "Right Click V2 · key bindings" becomes
  "Click V2 · key bindings".
- `internal/tui/view.go` `banner()` — "Only the Right controller is supported
  as a virtual keyboard" and "Only the Right one sends clicks; the Left one
  just has to stay on." are both false once this ships. New text states that
  both controllers work and that they must both be on because they pair with
  each other.
- `internal/tui/demo.go` — the demo's digit shortcuts walk a flat list (left
  pod then right pod): `1`–`9` for the first nine, `0` for the tenth. The
  footer hint "1-5 click" becomes "1-0 click", and `main.go`'s `-demo` flag
  help says the same.

This part is **observational only** — the TUI never gates a session, it only
renders.

### Part 3 — the same-VK stuck key (proven bug, must ship with this)

`internal/ble/buttons.go` keeps two pieces of state per key:

- `heldCount map[uint16]int` — a **count**, global across handlers.
- `Holds.own map[uint16]bool` — a **flag**, per handler.

They disagree when one handler holds **two different buttons that map to the
same VK**. `Release` deletes the flag on the first release, so the second
release finds no flag, returns early, and never decrements the count — the key
stays down forever, and the poisoned count affects every later press of that
VK in the whole process.

Proven by probe (before the fix):

```
TestProbeSameVKTwoButtonsHeldTogether:   down=1 up=0   <- key stuck down
TestProbeSameVKMirroredHeldTogether:     down=0 up=0   <- key never even
                                             pressed: the count was already
                                             poisoned by the first test
```

The mirrored probe is the realistic shape: the LEFT pod's stream carries the
mirrored RIGHT-pod state **and** its own button, so one handler sees the
mirrored `Z` press and its own `LEFT` press — two buttons, one VK.

This is reachable today only if one profile maps two buttons to the same key.
Part 1 makes it reachable through the obvious steering mapping
(`LEFT: left` on the left pod + `Z: left` on the right pod), so the fix ships
with this change rather than after it.

**Fix:** `Holds.own` becomes `map[uint16]int` — how many of *this handler's*
buttons currently hold that VK.

- `Press`: `own[VK]++`, `heldCount[VK]++`, send `Down` when the count reaches 1.
- `Release`: if `own[VK] == 0` → return (never saw it pressed); else
  `own[VK]--`, `heldCount[VK]--`, send `Up` when the count reaches 0.
- **`Press`'s rollback (the `down` call failed — no focus window):** decrement
  the count, never delete the key — `own[VK]--`, `heldCount[VK]--`. Deleting
  the key would wipe the whole count for that VK and reintroduce the
  early-lift bug in the focus-missing path.
- `ReleaseAll`: subtract `own[vk]` — the **count**, not one — from
  `heldCount[vk]`, so a session that dies mid-hold gives back exactly what it
  took.

Correctness of the fixed path, by hand:

| step | event | `own` (left handler) | `heldCount` | action |
|------|-------|----------------------|-------------|--------|
| 1 | right `Z` down (mirrored to left) | 1 | 2 (1 from right handler) | `Down` once |
| 2 | left `LEFT` down | 2 | 3 | key already down |
| 3 | left `LEFT` up | 1 | 2 | key stays down (`Z` still held) |
| 4 | right `Z` up (mirrored) | 0 | 0 | `Up` once |

The key is down for exactly the time *any* mapped button is held, and comes up
exactly once. Today step 3 deletes the flag, step 4 finds none, and the key
sticks down.

The existing tests are the regression net — `TestCrossControllerMirrorSingleDown`,
`TestDroppedSessionKeepsSiblingHold`, `TestDroppedSoleSessionReleases`,
`TestDroppedPressNotRecordedHeld`, `TestReleaseAllHeldLifts`,
`TestReleaseAllLiftNotifiesOnLifted` must all stay green and unchanged.

Two new tests become permanent regressions:
`TestSameVKTwoButtonsHeldTogether` and `TestSameVKMirroredHeldTogether`.

### Part 4 — observability and honest documentation

- `main.go` — the connect log lines are keyed off registration **order**, not
  the pod side: the first controller found logs *"controller connected — right
  pod supported (Y/Z/A/B/+)"* even when it is the left pod. The pod side **is**
  known at discovery (it comes from the advertisement's Zwift manufacturer
  record, `Target.Side`), so the line becomes truthful at discovery:
  the controller's label plus its side (`left` / `right` / `unknown` — the
  side is `PodUnknown` when the advertisement matched only on the `zwift*`
  local name, with no manufacturer record). The "both controllers work"
  statement lives in the TUI banner, which is driven by the real pod states
  and therefore cannot lie. **No change to `internal/ble`.**
- `config.yaml` header — the "BOTH PODS CONNECTED, RIGHT POD MAPPED" block is
  wrong after this change.
- `README.md` — the intro bullet ("The LEFT pod's arrows/minus are not used"),
  the "Both pods connected, only the right mapped" paragraph, the `notepad`
  row of the "Included profiles" table (it now understates what `notepad`
  binds), and `main.go`'s package comment (same claim).
- `AGENTS.md` — the hard-won facts: the "Click V2 is a two-pod PAIR" bullet
  gains "both pods' buttons are mappable"; a new fact records that the
  mirroring is **asymmetric** (right-pod buttons on both streams, left-pod
  buttons only on the left one); a new fact records that the held state is a
  per-VK **count** per handler (two buttons on one VK must not lift the key
  early).

## Tests

| Area | Test | Asserts |
|------|------|---------|
| config | `TestNotepadProfileMapsBothPods` (root) | the shipped `notepad` profile maps all ten buttons |
| config | `TestEveryShippedProfileLoads` (existing) | every shipped profile still loads |
| zwift | `TestPodButtonListsMatchBitMap` | `LeftButtons` + `RightButtons` == the bit map, exactly |
| ble | `TestSameVKTwoButtonsHeldTogether` | two buttons on one VK: one down, one up, key held until the last release |
| ble | `TestSameVKMirroredHeldTogether` | the mirrored + own-button shape: one down, one up |
| ble | the six existing dedup tests (unchanged) | the mirror/drop semantics still hold |
| tui | `TestViewTable` (extended) | the left pod's rows render next to the right pod's |
| tui | `TestViewIntroLine` (updated) | the banner no longer claims right-only support |
| tui | `TestViewKeyStates` (existing) | a left-pod press lights its row |
| tui | `TestDemoPressRunsFullCycle` (re-pointed) | the demo's digit shortcuts follow the new flat button list |

`just sanity` must be green before each commit.

**No hardware here:** `Watch`/`Session` correctness is reviewed, not tested.
The hardware check is the user's: run with the repo config, press each left
pod button, and confirm one character per press in Notepad.

## Test instructions for the hardware check

```bash
# from the repo, so the embedded default's notepad profile is the one loaded
go run . -config config.yaml -p notepad -v
```

(`-config` is required: the user config at `%LocalAppData%\zwiftboard\config.yml`
is a separate copy that `ensureConfig` never overwrites.) Then press each
left-pod button: `LEFT/UP/RIGHT/DOWN/MIN` must type `l u r d m` in Notepad,
once per press, and the status screen must show a `▶ pressed` row for the
button that was pressed.

## Decisions taken (my calls, flagged for review)

- The `notepad` mapping uses **letters** (`l u r d m`), not arrow keys.
- The **same-VK fix ships in this POC** rather than after it.
- The status screen uses the **two-column** table, with no header row, a
  stacked fallback below 72 cells of inner width, and the left column shown
  only for profiles that use the left pod.
- Only the `notepad` profile is touched; the other nine keep their
  right-pod-only mapping.
- The "both controllers work" wording lives in the TUI banner, not in a
  discovery-time log line.
- Work happens in a worktree on `feat/left-controller-support`, merged back
  into `feat/2-controllers-support` when done (the repo's own pattern:
  `.worktrees/app-profiles` → `feature/app-profiles-shortcuts` → `main`).

## Out of scope — the production pass

- Mapping the left pod in the other nine profiles, and the requirement that
  every shipped profile maps all ten buttons.
- The README "Included profiles" table for the other nine profiles.
- The legacy flat-config migration (`todo.md`, second item).
- Release, scoop, changelog.
