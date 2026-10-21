# Left Click V2 Controller Support — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Zwift Click V2 **LEFT** pod's five buttons (`LEFT UP RIGHT DOWN MIN`) type their keys like the RIGHT pod's already-working buttons, in the `notepad` profile only, without changing the RIGHT pod's behaviour or the BLE transport.

**Architecture:** No protocol change. The left pod's `0x23` frames already arrive and already decode (`internal/zwift.Buttons` carries the left bits, `internal/config` already accepts the left button names). The gap is (a) no profile maps them, (b) the status screen has no row for them, and (c) a proven stuck-key bug in the hold bookkeeping that the new mapping makes reachable. The plan therefore has four parts: the hold-count fix in `internal/ble/buttons.go`, the two-column key table in `internal/tui` (pod lists live in `internal/zwift`, the protocol-fact package), the `notepad` profile, and honest observability/docs.

**Tech Stack:** Go ≥ 1.27, Bubble Tea v2 (`charm.land/bubbletea/v2`), lipgloss v1, `github.com/charmbracelet/x/ansi`, `gopkg.in/yaml.v3`. `just sanity` (fmt, `go test -race ./...`, vet, build) must be green before each commit.

**Spec:** `docs/superpowers/specs/2026-10-21-left-controller-support-design.md` (same directory). Read it first — this plan argues from it.

**Working directory:** the worktree `/home/smith/.local/share/opencode/worktree/62e9f5/left-controller`, branch `feat/left-controller-support` (based on `feat/2-controllers-support` @ `e55dab2`). All commands below run there.

## Global Constraints

- **The RIGHT pod's behaviour must not change.** Its buttons, its keys, its timings, its log lines stay as they are today. The nine shipped profiles other than `notepad` keep their right-pod-only mapping and their current screen.
- **Do not touch the hard-won facts** (AGENTS.md): the handshake trio (`RideOn 02 03`, `00 08 00`, `00 08 10`), the 3s keepalive cadence, the idle pod reset (`0x18`, 55s), the scan-gated reconnect (`ReconnectWindow` 30s), the teardown probe (`endSession`), the pod-pair connect rule, the profile-switch path. `internal/ble`'s transport code (`ble.go`, `session.go`, `scan.go`, `side.go`) is **not** touched by this plan — only `internal/ble/buttons.go`'s hold bookkeeping changes.
- `config.yaml` is `//go:embed`-ed by `defaultconfig.go`: editing it edits the **shipped default**. `ensureConfig` never overwrites an existing user config, so the POC's new bindings only reach a user who passes `-config config.yaml` (or copies the file to `%LocalAppData%\zwiftboard\config.yml`). Say so in the commit message and the final report.
- Logging is `log/slog` only — no `log.Printf`, no `fmt.Println` (the sole exception is `-version`, which prints to stdout).
- Comments explain protocol facts and *why*; keep them when moving code.
- Tests live next to their package. Keep the dependency-injection seams (`ButtonHandler`'s down/up funcs as parameters) so the logic stays testable without hardware.
- Conventional commits, one logical step per commit. Everything in English.
- `just sanity` (local) or `just ci` (check-only) must be green before every commit.
- **No hardware here:** `Watch`/`Session` correctness is reviewed, not tested. The hardware check is the user's.

## Review Focus

- **The hold count (Task 1).** `Holds.own` becomes a *count*, not a flag. Every path that gives a reservation back must give back exactly what it took: `Press` (increment), `Release` (decrement), the `Press` rollback when `down` fails (decrement, never delete the whole VK), and `ReleaseAll` (subtract this handler's whole count). A single wrong decrement leaves a key stuck down for the whole process — the worst possible failure for a keyboard driver.
- **The table never wraps (Task 2).** No rendered *button row* may be wider than the card's inner width, at *any* terminal width (40 … 200). A wider line wraps inside the card and shreds the layout. The button cell is **35** cells in its widest state (the labelled one), so the side-by-side form needs **72**; below that the two blocks stack. Measured with `ansi.StringWidth`, not guessed — see the width table in Task 2, Step 8.
- **The right-pod-only screen stays byte for byte (Task 2).** The button row's rendering is *moved*, not rewritten: the status label keeps its `Padding(0,1)` style, so the row is 33 cells idle and 35 when a label is shown. A profile that maps no left-pod button renders exactly the rows it renders today.
- **The screen of a right-pod-only profile must not change.** The left column appears only when the active profile maps at least one left-pod button.
- **The TUI stays observational.** Nothing in this change may gate a session, a connect or a reconnect on a pod side or on what the table shows.
- **The `-demo` digit shortcuts and the footer hint must agree** with the new flat button list (ten buttons: `1`–`9` then `0`).

---

### Task 1: The same-VK stuck key — the hold state becomes a count

**Files:**
- Modify: `internal/ble/buttons.go` (`Holds.own`, `holdKeys`, `Press`, `Release`, `ReleaseAll`)
- Modify: `internal/ble/buttons_test.go` (append two regression tests)

**Interfaces:**
- Consumes: `holdKeys(down, up func(keys.Binding) bool) *Holds`, `(*Holds).Press(keys.Binding)`, `(*Holds).Release(keys.Binding)`, `(*Holds).ReleaseAll()`, `ButtonHandler(label string, km *KeyMap, down, up func(keys.Binding)) func([]byte)`, `keys.Binding{VK uint16, Token string}`, the test helpers `keyRecorder` (`downFn`/`upFn`/`counts`) already in `buttons_test.go`.
- Produces: `Holds.own` changes type from `map[uint16]bool` to `map[uint16]int` (how many of *this handler's* buttons currently hold that VK). Nothing outside `internal/ble/buttons.go` touches `own`.

**Why:** the RIGHT pod's buttons are mirrored onto **both** controllers' streams, so one handler can see two different buttons mapped to the **same** key (e.g. the mirrored `Z` and the left pod's own `LEFT`, both bound to `left`). Today `own` is a per-VK *flag* while `heldCount` is a *count*: the first `Release` deletes the flag, the second finds none, returns early and never decrements the count — the key stays down forever and poisons every later press of that VK.

- [ ] **Step 1: Write the failing test** — append to `internal/ble/buttons_test.go`:

```go
// Two buttons of the SAME handler mapped to the SAME key (a profile that
// binds e.g. LEFT and UP to one key) must hold the key until the LAST of
// them is released, and lift it exactly once. The held state is a per-VK
// COUNT, not a flag: with a flag the first release wiped it and the second
// release found nothing to lift, leaving the key stuck down for the whole
// process.
func TestSameVKTwoButtonsHeldTogether(t *testing.T) {
	rec := &keyRecorder{}
	hk := holdKeys(rec.downFn, rec.upFn)
	km := NewKeyMap(map[string]keys.Binding{
		"LEFT": {VK: 0x25, Token: "left"},
		"UP":   {VK: 0x25, Token: "left"},
	})
	h := ButtonHandler("t", km, hk.Press, hk.Release)
	t.Cleanup(hk.ReleaseAll)

	both := []byte{0x23, 0x08, 0xFC, 0xFF, 0xFF, 0xFF, 0x0F} // LEFT + UP pressed
	upOnly := []byte{0x23, 0x08, 0xFD, 0xFF, 0xFF, 0xFF, 0x0F} // LEFT released, UP held
	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}  // all released

	h(both)
	if down, up := rec.counts(); down != 1 || up != 0 {
		t.Fatalf("both buttons down: down=%d up=%d, want 1/0", down, up)
	}
	h(upOnly)
	if down, up := rec.counts(); down != 1 || up != 0 {
		t.Fatalf("one of them released: down=%d up=%d, want 1/0 (the key is still held)", down, up)
	}
	h(idle)
	if down, up := rec.counts(); down != 1 || up != 1 {
		t.Fatalf("both released: down=%d up=%d, want 1/1 (the key must not stay stuck)", down, up)
	}
}
```

- [ ] **Step 2: Run the test to verify it FAILS**

Run: `go test ./internal/ble/ -run TestSameVKTwoButtonsHeldTogether -v`
Expected: FAIL — `both released: down=1 up=0, want 1/1 (the key must not stay stuck)`.

- [ ] **Step 3: Write the second failing test** — append to `internal/ble/buttons_test.go`:

```go
// The realistic shape: the LEFT pod's stream carries the mirrored RIGHT-pod
// state AND the left pod's own button, so one handler sees two buttons on
// one VK. The key must go down once and come up once — never stay down.
func TestSameVKMirroredHeldTogether(t *testing.T) {
	rec := &keyRecorder{}
	rHK := holdKeys(rec.downFn, rec.upFn)
	lHK := holdKeys(rec.downFn, rec.upFn)
	// The right pod presses Z; the left pod mirrors Z and presses its own
	// LEFT. Both are bound to the same key (left arrow).
	rKM := NewKeyMap(map[string]keys.Binding{"Z": {VK: 0x25, Token: "left"}})
	lKM := NewKeyMap(map[string]keys.Binding{
		"Z":    {VK: 0x25, Token: "left"}, // mirrored from the right pod
		"LEFT": {VK: 0x25, Token: "left"}, // the left pod's own button
	})
	right := ButtonHandler("right", rKM, rHK.Press, rHK.Release)
	left := ButtonHandler("left", lKM, lHK.Press, lHK.Release)
	t.Cleanup(rHK.ReleaseAll)
	t.Cleanup(lHK.ReleaseAll)

	zPress := []byte{0x23, 0x08, 0xFF, 0xFE, 0xFF, 0xFF, 0x0F} // Z pressed
	zLeft := []byte{0x23, 0x08, 0xFE, 0xFE, 0xFF, 0xFF, 0x0F} // Z + LEFT pressed
	lPress := []byte{0x23, 0x08, 0xFE, 0xFF, 0xFF, 0xFF, 0x0F} // LEFT pressed, Z released
	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}  // all released

	right(zPress)
	left(zPress)  // the mirrored copy of the right pod's press
	left(zLeft)   // the left pod's own LEFT joins the mirrored Z
	left(lPress)  // Z released (mirrored), LEFT still held
	right(idle)   // Z released on the right pod
	left(idle)    // the mirrored release

	if down, up := rec.counts(); down != 1 || up != 1 {
		t.Fatalf("mirrored + own button on one VK: down=%d up=%d, want 1/1", down, up)
	}
}
```

- [ ] **Step 4: Run the test to verify it FAILS**

Run: `go test ./internal/ble/ -run TestSameVK -v`
Expected: FAIL — both new tests fail. `TestSameVKTwoButtonsHeldTogether` reports
`both released: down=1 up=0, want 1/1` (the key stays stuck down).
`TestSameVKMirroredHeldTogether` reports whatever the shared count was left at
— it is usually `down=0 up=0, want 1/1`, because the FIRST test's bug leaves
`heldCount[0x25]` at 1, so the mirrored test's first press is not the first
reservation and no key goes down at all. **Both are the same root cause**
(the per-VK flag instead of a count); the exact numbers depend on the order
the tests run in.

- [ ] **Step 5: Change `Holds.own` to a count** — `internal/ble/buttons.go`:

Replace the struct field and its comment:

```go
type Holds struct {
	down func(keys.Binding) bool
	up   func(keys.Binding) bool
	// own counts how many of THIS handler's buttons currently hold the VK.
	// A count, not a flag: two different buttons of one handler can map to
	// the same key (the mirrored right-pod press plus the left pod's own
	// button), and the key must stay down until the LAST of them is
	// released. With a flag the first release wiped it and the second
	// release found nothing to lift — the key stuck down for the process.
	own map[uint16]int
}
```

and in `holdKeys`:

```go
func holdKeys(down, up func(keys.Binding) bool) *Holds {
	return &Holds{down: down, up: up, own: map[uint16]int{}}
}
```

- [ ] **Step 6: Fix `Press`, `Release` and `ReleaseAll`** — `internal/ble/buttons.go`:

```go
func (h *Holds) Press(b keys.Binding) {
	keyMu.Lock()
	h.own[b.VK]++
	heldCount[b.VK]++
	first := heldCount[b.VK] == 1
	keyMu.Unlock()
	if !first {
		return
	}
	if !h.down(b) {
		keyMu.Lock()
		// Give back exactly what this press took: ONE reservation, not the
		// whole key — another handler (or another button of this one) may
		// still hold it.
		if h.own[b.VK] > 0 {
			h.own[b.VK]--
		}
		if heldCount[b.VK] > 0 {
			heldCount[b.VK]--
		}
		if heldCount[b.VK] == 0 {
			delete(heldCount, b.VK)
		}
		keyMu.Unlock()
	}
}

// Release is called on a released transition. It removes this handler's
// reservation and sends Up when no live handler sees the key pressed anymore.
func (h *Holds) Release(b keys.Binding) {
	keyMu.Lock()
	if h.own[b.VK] == 0 {
		keyMu.Unlock() // never saw it pressed (e.g. subscribed mid-hold): nothing to lift
		return
	}
	h.own[b.VK]--
	if heldCount[b.VK] > 0 {
		heldCount[b.VK]--
	}
	last := heldCount[b.VK] == 0
	keyMu.Unlock()
	if last {
		h.up(b)
	}
}

// ReleaseAll frees every key this session held and no other live handler
// still sees pressed. Session defers it, so a keepalive failure (real
// disconnect) can never leave a key stuck down. A key the sibling session
// still sees pressed is left down: the same mirrored release is on its way to
// that sibling and will lift it.
func (h *Holds) ReleaseAll() {
	keyMu.Lock()
	for vk, n := range h.own {
		// Give back exactly what this handler took: its whole count, not
		// one — it may hold the same VK through several buttons.
		heldCount[vk] -= n
		if heldCount[vk] < 0 {
			heldCount[vk] = 0
		}
	}
	h.own = map[uint16]int{}
	lift := make([]uint16, 0, len(heldCount))
	for vk, n := range heldCount {
		if n <= 0 {
			delete(heldCount, vk)
			lift = append(lift, vk)
		}
	}
	keyMu.Unlock()
	releaseVKs(lift)
}
```

- [ ] **Step 7: Run the package tests to verify they now PASS**

Run: `go test ./internal/ble/ -v`
Expected: PASS — all tests, including the two new ones and the six existing dedup tests (`TestCrossControllerMirrorSingleDown`, `TestDroppedSessionKeepsSiblingHold`, `TestDroppedSoleSessionReleases`, `TestDroppedPressNotRecordedHeld`, `TestReleaseAllHeldLifts`, `TestReleaseAllLiftNotifiesOnLifted`) **unchanged and green**.

- [ ] **Step 8: Run the full suite**

Run: `just sanity`
Expected: fmt, `go test -race ./...`, vet and build all pass (exit 0).

- [ ] **Step 9: Commit**

```bash
git add internal/ble/buttons.go internal/ble/buttons_test.go
git commit -m "fix: hold a key until the last button on that key is released"
```

---

### Task 2: The status screen shows both pods

**Files:**
- Modify: `internal/zwift/protocol.go` (add `LeftButtons` / `RightButtons` after the `Buttons` map)
- Modify: `internal/zwift/protocol_test.go` (add `TestPodButtonListsMatchBitMap`)
- Modify: `internal/tui/model.go` (`Config.Buttons` → `Config.LeftButtons` + `Config.RightButtons`)
- Modify: `internal/tui/view.go` (`table()` split into `buttonRow`/`rows`/`twoColumns`/`tableRows`, the card title, the banner, the footer hint, the new width constants)
- Modify: `internal/tui/demo.go` (the flat button list for the digit shortcuts)
- Modify: `internal/tui/model_test.go` (`testCfg()`, re-point `TestDemoPressRunsFullCycle`)
- Modify: `internal/tui/view_test.go` (`testCfg()` is in model_test.go; new table tests, updated banner test)
- Modify: `tuirun.go` (drop the local `rightButtons`, fill the two new `tui.Config` fields)
- Modify: `main.go` (the `-demo` flag help text)

**Interfaces:**
- Consumes: `keys.Binding`; the TUI's `Config.Bindings` (button → key token); `m.cfg.Focus`, `m.cfg.StartProgram`, `m.cfg.NoMapping`.
- Produces:
  - `zwift.LeftButtons []string` = `{"LEFT", "UP", "RIGHT", "DOWN", "MIN"}` and `zwift.RightButtons []string` = `{"Y", "Z", "A", "B", "PLUS"}` (new, in `internal/zwift/protocol.go`).
  - `tui.Config.LeftButtons []string` and `tui.Config.RightButtons []string` (replace `tui.Config.Buttons []string`).
  - `tui.Model.buttonRow(name string) string`, `tui.Model.rows(names []string) string`, `tui.Model.twoColumns(left, right []string) string`, `tui.Model.tableRows() string`, `tui.Model.mapsLeftPod() bool` (new, unexported).

- [ ] **Step 1: Write the failing test** — append to `internal/zwift/protocol_test.go`:

```go
// The pod button lists and the bit map must agree exactly: a bit added to
// one and forgotten in the other would silently drop a button.
func TestPodButtonListsMatchBitMap(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range append(append([]string{}, LeftButtons...), RightButtons...) {
		if seen[n] {
			t.Errorf("button %q is listed twice", n)
		}
		seen[n] = true
	}
	if len(seen) != len(Buttons) {
		t.Errorf("the pod lists cover %d buttons, the bit map has %d", len(seen), len(Buttons))
	}
	for bit, name := range Buttons {
		if !seen[name] {
			t.Errorf("bit 0x%x (%s) is in the bit map but in no pod list", bit, name)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it FAILS**

Run: `go test ./internal/zwift/ -run TestPodButtonListsMatchBitMap -v`
Expected: build failure — `undefined: LeftButtons` (the feature does not exist yet).

- [ ] **Step 3: Add the pod lists** — `internal/zwift/protocol.go`, right after the `Buttons` map:

```go
// LeftButtons and RightButtons are the Click V2's two pods' buttons, in
// table order. The LEFT pod owns the four arrows and MINUS; the RIGHT pod
// owns Y Z A B and PLUS. The split is a protocol fact (it matches the bits
// above), so it lives here and not in the UI.
var (
	LeftButtons  = []string{"LEFT", "UP", "RIGHT", "DOWN", "MIN"}
	RightButtons = []string{"Y", "Z", "A", "B", "PLUS"}
)
```

- [ ] **Step 4: Run the test to verify it now PASSES**

Run: `go test ./internal/zwift/ -v`
Expected: PASS.

- [ ] **Step 5: Write the failing TUI tests** — in `internal/tui/model_test.go`, replace `testCfg()`:

```go
func testCfg() Config {
	return Config{
		Profile:      "test",
		LeftButtons:  []string{"LEFT", "UP", "RIGHT", "DOWN", "MIN"},
		RightButtons: []string{"Y", "Z", "A", "B", "PLUS"},
		Bindings:     map[string]string{"A": "a", "B": "b", "PLUS": "+"},
	}
}
```

and in `internal/tui/view_test.go`, append:

```go
// leftCfg is the test config with a left-pod binding: the profile maps one
// left-pod button, so the table gets a left column.
func leftCfg() Config {
	cfg := testCfg()
	cfg.Bindings["LEFT"] = "l"
	return cfg
}

// buttonRows counts the table's button rows (the rows carrying the "→").
func buttonRows(table string) int {
	n := 0
	for _, l := range strings.Split(table, "\n") {
		if strings.Contains(ansi.Strip(l), "→") {
			n++
		}
	}
	return n
}

// The two pods share the table: the left pod's column beside the right
// pod's, five rows for ten buttons.
func TestViewTableShowsBothPods(t *testing.T) {
	m := on(leftCfg(), PodConnected, PodConnected)
	m.width = 120
	v := m.View()
	has(t, v, "LEFT", "MIN", "PLUS")
	if got := buttonRows(m.table()); got != 5 {
		t.Errorf("table has %d rows, want 5 (ten buttons in two columns):\n%s", got, m.table())
	}
}

// The two pods sit side by side on ONE line: the left pod's arrow at cell 6
// (after the 6-cell name) and the right pod's at cell 43 (the 35-cell left
// cell + the 2-cell gap + the 6-cell name). The cells are measured, so a
// mis-sized cellWidth shows up here instead of as a wrapped table.
func TestViewTableColumnsShareALine(t *testing.T) {
	m := on(leftCfg(), PodConnected, PodConnected)
	m.width = 120
	var row string
	for _, l := range strings.Split(m.table(), "\n") {
		if plain := ansi.Strip(l); strings.Contains(plain, "→") {
			row = plain
			break
		}
	}
	if row == "" {
		t.Fatalf("no button row rendered:\n%s", m.table())
	}
	var arrows []int
	cell := 0
	for _, r := range row {
		if r == '→' {
			arrows = append(arrows, cell)
		}
		cell += ansi.StringWidth(string(r))
	}
	if len(arrows) != 2 {
		t.Fatalf("row has %d arrows, want 2 (one per pod):\n%q", len(arrows), row)
	}
	if arrows[0] != 6 {
		t.Errorf("left arrow at cell %d, want 6 (after the 6-cell name)", arrows[0])
	}
	if arrows[1] != 43 {
		t.Errorf("right arrow at cell %d, want 43 (left cell 35 + gap 2 + name 6)", arrows[1])
	}
	if !strings.HasPrefix(row, "LEFT") {
		t.Errorf("row should start with the left pod's name:\n%q", row)
	}
	if !strings.Contains(row, "Y") {
		t.Errorf("row should carry the right pod's name:\n%q", row)
	}
}

// A profile that maps no left-pod button keeps today's screen: the right
// pod's column alone, and no left column.
func TestViewTableNoLeftColumnWithoutLeftBinding(t *testing.T) {
	m := on(testCfg(), PodConnected, PodConnected)
	m.width = 120
	if plain := ansi.Strip(m.table()); strings.Contains(plain, "LEFT") {
		t.Fatalf("no left-pod button is mapped, the table must not show a left column:\n%s", plain)
	}
	if got := buttonRows(m.table()); got != 5 {
		t.Errorf("table has %d rows, want 5:\n%s", got, m.table())
	}
}

// Too narrow for the side-by-side form: the two blocks stack instead of
// wrapping, so the table stays ten rows tall.
func TestViewTableStacksOnNarrowScreen(t *testing.T) {
	m := on(leftCfg(), PodConnected, PodConnected)
	m.width = 60 // inner width 56 < 72
	if got := buttonRows(m.table()); got != 10 {
		t.Errorf("narrow table has %d rows, want 10 (the two blocks stacked):\n%s", got, m.table())
	}
}

// No BUTTON ROW may be wider than the card's inner width: a wider line
// wraps inside the card and shreds the layout. The focus line under the
// table is not checked — it is 50 cells wide and has always wrapped on a
// narrow terminal; that is a pre-existing quirk, not this change's
// business.
//
// The pressed row is checked too: it is the widest one (a labelled status
// cell is 14 cells, not 12), so a cell sized for the idle row alone would
// overflow here.
func TestViewTableFitsInnerWidth(t *testing.T) {
	for _, w := range []int{40, 60, 76, 80, 120, 200} {
		m := on(leftCfg(), PodConnected, PodConnected)
		m.width = w
		inner := m.innerWidth()
		check := func(mm Model, what string) {
			t.Helper()
			for _, line := range strings.Split(mm.table(), "\n") {
				plain := ansi.Strip(line)
				if !strings.Contains(plain, "→") {
					continue // not a button row
				}
				if got := ansi.StringWidth(line); got > inner {
					t.Errorf("width %d (%s): a button row is %d cells wide, inner width is %d:\n%q",
						w, what, got, inner, plain)
				}
			}
		}
		check(m, "idle")
		next, _ := m.Update(KeyStateMsg{Button: "LEFT", Down: true})
		check(next.(Model), "pressed")
	}
}
```

and add the `ansi` import to `internal/tui/view_test.go`:

```go
import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)
```

- [ ] **Step 6: Run the TUI tests to verify they FAIL**

Run: `go test ./internal/tui/ -v`
Expected: build failure — `unknown field LeftButtons in struct literal` (the config field does not exist yet).

- [ ] **Step 7: Replace `Config.Buttons` with the two pod lists** — `internal/tui/model.go`:

```go
type Config struct {
	Profile  string
	Bindings map[string]string // button -> key token
	// LeftButtons and RightButtons are the Click V2 pods' button names, in
	// table order: the left pod's column and the right pod's column.
	LeftButtons  []string
	RightButtons []string
	Focus        string            // focusProgramNamePrefixOnClick; "" = off
	StartProgram string            // program launched on activation; "" = none
	NoMapping    bool              // config file missing
	Demo         bool              // enable mock toggle keys
	Logs         *LogBuffer        // log panel source; nil = none
	ConfigPath   string            // shown in status messages
	Version      string            // shown next to the title; the version stamp (e.g. "1.2.3"); "" hides it
	OpenConfig   func() error      // opens the config file in an editor; nil = shortcut off
	Profiles     []string          // profile names in order; nil = switching off
	ProfileIndex int               // active index into Profiles
	// SwitchProfile requests a relative profile switch; main wires it and
	// pushes a ProfileMsg back. nil = switching off.
	SwitchProfile func(delta int)
	// LaunchProgram starts the active profile's startProgram on demand (the "s"
	// shortcut), so a program the user closed can be reopened. nil = not wired;
	// the shortcut is also hidden while StartProgram is "".
	LaunchProgram func() error
	// Reload re-reads the config from disk and applies it to the active profile
	// without a BLE reconnect (the "r" shortcut). nil = reload off.
	Reload func()
}
```

Then run `gofmt -w .` — the struct's field alignment changes with the new
fields, and `just sanity` runs the formatter check.

- [ ] **Step 8: Render the two columns** — `internal/tui/view.go`:

Add the width constants to the existing `const` block:

```go
	// cellWidth is the fixed width of ONE button cell in the two-column
	// table: the widest a row can get, so the columns line up whatever
	// the button's state.
	//
	// The row is name(6) + "→ "(2) + keycap(11) + "  "(2) + status(12..14).
	// The keycap is 11, not 9: the keycap style carries its own one-space
	// padding on each side of the 9-cell token cell. The status cell is 12
	// when the row is idle and 14 when it carries a label (the label style
	// has Padding(0,1) too), so the cell is sized for the LABELLED row:
	// 6+2+11+2+14 = 35. An unmapped row (a "—" with no keycap style) is
	// only 31 and is padded up to the cell width.
	//
	// Measured with ansi.StringWidth, not guessed.
	cellWidth = 35
	// columnGap separates the two pod columns.
	columnGap = 2
	// twoColumnWidth is the inner width the side-by-side table needs:
	// two cells of 35 plus the gap.
	twoColumnWidth = 2*cellWidth + columnGap
```

Replace the whole `table()` function with:

```go
func (m Model) table() string {
	var b strings.Builder
	if m.cfg.NoMapping {
		b.WriteString(sMuted.Render("no key mapping — button logging only"))
	} else {
		b.WriteString(m.tableRows())
	}
	focus := sMuted.Render("off")
	if m.cfg.Focus != "" {
		focus = sBold.Render(m.cfg.Focus)
		if m.focusMissing {
			focus += sBad.Render("  ✗ not found")
		}
	}
	out := strings.TrimRight(b.String(), "\n") + "\n\n" + sMuted.Render("Auto-focus window on click starting with name: ") + focus
	if m.cfg.StartProgram != "" {
		out += "\n" + sMuted.Render("Start program on activation: ") + sBold.Render(m.cfg.StartProgram)
	}
	return out
}

// buttonRow renders one button row: the name, the keycap and the status
// label. It is the single-column table's row, moved out unchanged: the
// right-pod-only screen must keep rendering exactly the same bytes.
//
// The row's width depends on the button's state — 31 cells unmapped, 33
// idle, 35 when the status carries a label — so the two-column layout
// pads the cell to cellWidth (the widest case).
func (m Model) buttonRow(name string) string {
	key, ok := m.cfg.Bindings[name]
	// Fixed-width keycap cell (keycapW): the status column starts at
	// the same offset whatever the token, in every phase.
	cap := sMuted.Render("—" + strings.Repeat(" ", keycapW-1))
	label := ""
	var style lipgloss.Style
	if ok {
		tok := padRight(key, keycapW)
		cap = sKeycap.Render(tok)
		switch m.keyPhase(name) {
		case PhasePressed:
			cap = sDown.Render(tok)
			label, style = "▶ pressed", sDown
		case PhaseHold:
			cap = sHold.Render(tok)
			label, style = pulse(m.now())+" hold", sHold
		case PhaseReleased:
			label, style = "○ released", sReleased
		}
	}
	out := padRight(label, 12)
	if label != "" {
		out = style.Render(padRight(label, 12))
	}
	return sBold.Render(padRight(name, 6)) + sMuted.Render("→ ") + cap + "  " + out
}

// mapsLeftPod reports whether the active profile maps at least one left-pod
// button: only then does the table get a left column. A profile that does
// not use the left pod keeps the screen it has today.
func (m Model) mapsLeftPod() bool {
	for _, name := range m.cfg.LeftButtons {
		if _, ok := m.cfg.Bindings[name]; ok {
			return true
		}
	}
	return false
}

// tableRows renders the button rows: the left pod's column beside the right
// pod's when the profile maps a left-pod button and the card is wide enough,
// the right pod's alone otherwise, and the two blocks stacked when the card
// is too narrow for the side-by-side form (so nothing ever wraps).
func (m Model) tableRows() string {
	left, right := m.cfg.LeftButtons, m.cfg.RightButtons
	if !m.mapsLeftPod() {
		return m.rows(right)
	}
	if m.innerWidth() >= twoColumnWidth {
		return m.twoColumns(left, right)
	}
	return m.rows(append(append([]string{}, left...), right...))
}

// rows renders one button row per name, stacked.
func (m Model) rows(names []string) string {
	var b strings.Builder
	for _, name := range names {
		b.WriteString(m.buttonRow(name) + "\n")
	}
	return b.String()
}

// twoColumns renders the left pod's rows beside the right pod's rows. The
// left cell is padded to cellWidth so the columns line up whatever the token
// length; the right cell keeps its natural width (the row's own trailing
// spaces are the table's edge).
func (m Model) twoColumns(left, right []string) string {
	var b strings.Builder
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	for i := 0; i < n; i++ {
		var l, r string
		if i < len(left) {
			l = padRight(m.buttonRow(left[i]), cellWidth)
		}
		if i < len(right) {
			r = m.buttonRow(right[i])
		}
		line := l
		if r != "" {
			if l != "" {
				line += strings.Repeat(" ", columnGap)
			}
			line += r
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
```

- [ ] **Step 9: Run the TUI tests to verify they now PASS**

Run: `go test ./internal/tui/ -v`
Expected: PASS — including the five new table tests (`TestViewTableShowsBothPods`,
`TestViewTableColumnsShareALine`, `TestViewTableNoLeftColumnWithoutLeftBinding`,
`TestViewTableStacksOnNarrowScreen`, `TestViewTableFitsInnerWidth`).

- [ ] **Step 10: Make the demo walk the flat button list** — `internal/tui/demo.go`:

```go
package tui

import tea "charm.land/bubbletea/v2"

// demoButtons is the flat list the demo's digit shortcuts walk: the left
// pod's buttons first, then the right pod's — the hardware's own order.
// 1-9 select the first nine, 0 the tenth.
func (m Model) demoButtons() []string {
	return append(append([]string{}, m.cfg.LeftButtons...), m.cfg.RightButtons...)
}

// demoKey handles the mock toggles available only with Config.Demo:
// b = Bluetooth, [ and ] = cycle Left/Right pod (l is the log panel),
// f = toggle "focus window missing", 1..9 and 0 = click the n-th button.
func (m Model) demoKey(k string) (tea.Model, tea.Cmd) {
	next := func(s PodState) PodState { return (s + 1) % 3 }
	switch k {
	case "b":
		m.bt = !m.bt
	case "f":
		m.focusMissing = !m.focusMissing
	case "[":
		m.left = next(m.left)
	case "]":
		m.right = next(m.right)
	default:
		if len(k) == 1 {
			btns := m.demoButtons()
			i := -1
			switch {
			case k[0] >= '1' && k[0] <= '9':
				i = int(k[0] - '1')
			case k[0] == '0':
				i = 9
			}
			if i >= 0 && i < len(btns) {
				// Press through Update now: the caller's cmd must already be
				// the press batch (fast tick + scheduled release), not a
				// demoPressMsg the program loop would have to re-feed.
				return m.Update(demoPressMsg{button: btns[i]})
			}
		}
	}
	return m, nil
}
```

- [ ] **Step 11: Re-point the demo test** — `internal/tui/model_test.go`:

```go
func TestDemoPressRunsFullCycle(t *testing.T) {
	n, cmd := demoModel().Update(key("8")) // demoButtons[7] = "A"
```

(the rest of the test is unchanged; the comment on the first line is the only edit).

- [ ] **Step 12: Run the demo test to verify it PASSES**

Run: `go test ./internal/tui/ -run TestDemoPressRunsFullCycle -v`
Expected: PASS.

- [ ] **Step 13: Update the banner test (RED first)** — `internal/tui/view_test.go`:

```go
func TestViewIntroLine(t *testing.T) {
	v := on(testCfg(), PodOff, PodConnected).View()
	has(t, v, "WAITING", "Both controllers must be ON")
	lacks(t, v, "Both pods send clicks", "READY")
	v = on(testCfg(), PodConnected, PodConnected).View()
	has(t, v, "READY", "Both controllers connected", "Both pods send clicks")
	lacks(t, v, "Both controllers must be ON", "WAITING")
}
```

- [ ] **Step 14: Run the banner test to verify it FAILS**

Run: `go test ./internal/tui/ -run TestViewIntroLine -v`
Expected: FAIL — the view still says "Only the Right controller is supported as
a virtual keyboard" and "Only the Right one sends clicks…".

- [ ] **Step 15: Fix the banner, the card title and the footer hint** — `internal/tui/view.go`:

In `View()`, replace the card title:

```go
	b.WriteString(m.card(cAccent, "Click V2 · key bindings", m.table()) + "\n")
```

Replace `banner()`:

```go
// banner is the one-glance state: a coloured chip plus a headline, with the
// supporting rule in a muted second line. Colour is never the only signal —
// the chip carries the word (READY / WAITING).
func (m Model) banner() string {
	if m.left == PodConnected && m.right == PodConnected {
		return m.cardW(m.innerWidth(), cOK, "",
			sChipOK.Render("READY")+" "+sBold.Render("Both controllers connected")+"\n"+
				sMuted.Render("Both pods send clicks — the left pod's arrows and minus are mappable too"))
	}
	return m.cardW(m.innerWidth(), cWarn, "",
		sChipWarn.Render("WAITING")+" "+sBold.Render("Both controllers must be ON")+"\n"+
			sMuted.Render("Either pod sends clicks — keep both on so the pair stays linked"))
}
```

In `footer()`, replace the demo hint:

```go
	if m.cfg.Demo {
		f += sMuted.Render("  ·  demo: ") + hint("b", "bluetooth") + sMuted.Render(" ") +
			hint("[ ]", "left/right pod") + sMuted.Render(" ") + hint("1-0", "click") + sMuted.Render(" ") + hint("f", "focus alert")
	}
```

In `main.go`, the `-demo` flag help:

```go
	demo := flag.Bool("demo", false, "TUI with mock toggle keys (b bluetooth, [ ] pods, f focus alert, 1-0 click), no Bluetooth — try the screen without hardware")
```

- [ ] **Step 16: Run the TUI tests to verify they now PASS**

Run: `go test ./internal/tui/ -v`
Expected: PASS — the banner test and the four table tests.

- [ ] **Step 17: Wire the pod lists from main** — `tuirun.go`:

Delete the local list:

```go
// rightButtons are the Click V2 right pod's buttons, the only ones normally
// mapped; they form the key table's rows.
var rightButtons = []string{"Y", "Z", "A", "B", "PLUS"}
```

and in `tuiConfig`, replace `Buttons: rightButtons,` with:

```go
		LeftButtons:  zwift.LeftButtons,
		RightButtons: zwift.RightButtons,
```

(the file already imports `zwiftboard/internal/zwift`).

- [ ] **Step 18: Run the full suite**

Run: `just sanity`
Expected: fmt, `go test -race ./...`, vet and build all pass (exit 0). Every existing test in `internal/tui` is still green — only `testCfg()`, `TestDemoPressRunsFullCycle` and `TestViewIntroLine` changed.

- [ ] **Step 19: Commit**

```bash
git add internal/zwift/protocol.go internal/zwift/protocol_test.go internal/tui/ tuirun.go main.go
git commit -m "feat: show the left pod's bindings in the status screen"
```

---

### Task 3: The `notepad` profile maps both pods

**Files:**
- Modify: `config.yaml` (the header block above `profiles:` and the `notepad` profile)
- Modify: `profiles_test.go` (add `TestNotepadProfileMapsBothPods`)

**Interfaces:**
- Consumes: `zwift.LeftButtons`, `zwift.RightButtons` (Task 2), `config.Load(path, profile) (config.Config, error)`, `defaultConfig []byte` (the embedded `config.yaml`), the test helper `writeDefaultConfig(t) string` already in `profiles_test.go`.
- Produces: the shipped `notepad` profile binds all ten buttons.

- [ ] **Step 1: Write the failing test** — append to `profiles_test.go`:

```go
// The notepad profile is the hardware-test profile of the left-controller
// POC: it must map the LEFT pod's five buttons as well as the RIGHT pod's.
func TestNotepadProfileMapsBothPods(t *testing.T) {
	cfg, err := config.Load(writeDefaultConfig(t), "notepad")
	if err != nil {
		t.Fatal(err)
	}
	for _, btn := range append(append([]string{}, zwift.LeftButtons...), zwift.RightButtons...) {
		if _, ok := cfg.Bindings[btn]; !ok {
			t.Errorf("profile notepad: missing binding for %s", btn)
		}
	}
}
```

and add the import to `profiles_test.go`:

```go
import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"zwiftboard/internal/config"
	"zwiftboard/internal/zwift"
)
```

- [ ] **Step 2: Run the test to verify it FAILS**

Run: `go test . -run TestNotepadProfileMapsBothPods -v`
Expected: FAIL — `profile notepad: missing binding for LEFT` (and for UP, RIGHT, DOWN, MIN).

- [ ] **Step 3: Map the left pod in the `notepad` profile** — `config.yaml`:

```yaml
  # Start notepad. Both pods are mapped: the left pod's arrows and minus
  # type l u r d m, the right pod's buttons type a b y z p.
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

(Letters, not arrows, on purpose: the right pod already maps to letters, so the left pod's `l u r d m` are symmetric and the character is *visible* in the Notepad text box — an arrow key only moves the caret.)

- [ ] **Step 4: Correct the header block** — `config.yaml`, replace the "BOTH PODS CONNECTED, RIGHT POD MAPPED" block:

```yaml
# BOTH PODS CONNECTED, BOTH PODS MAPPABLE: the Click V2 is a pair and zwiftboard
# keeps BOTH pods connected — the LEFT pod is the pair's BLE anchor and letting
# it drop drops the RIGHT pod's link too. Both pods' buttons can be mapped
# below: the LEFT pod has LEFT UP RIGHT DOWN MIN, the RIGHT pod has Y Z A B
# PLUS. The shipped profiles map the RIGHT pod only, except `notepad` which
# maps both (the left-pod hardware-test profile). Gear shift lives on the
# right pod: PLUS = gear down (k), B = gear up (i).
```

- [ ] **Step 5: Run the test to verify it now PASSES**

Run: `go test . -v`
Expected: PASS — `TestNotepadProfileMapsBothPods` and the existing `TestShippedProfilesMatchREADME`, `TestEveryShippedProfileLoads`, `TestNewProfilesMapEveryButton`.

- [ ] **Step 6: Run the full suite**

Run: `just sanity`
Expected: fmt, `go test -race ./...`, vet and build all pass (exit 0).

- [ ] **Step 7: Commit**

```bash
git add config.yaml profiles_test.go
git commit -m "feat: map the left pod's buttons in the notepad profile"
```

---

### Task 4: Honest observability and documentation

**Files:**
- Modify: `main.go` (the connect log lines, the package comment)
- Modify: `README.md` (the intro bullet, the "How it works" paragraphs, the `notepad` row of the "Included profiles" table)
- Modify: `AGENTS.md` (the hard-won facts)

**Interfaces:**
- Consumes: `ble.Target.Side` (a `zwift.Pod`, already decoded from the advertisement's Zwift manufacturer record — `zwift.PodSide`), `zwift.Pod.String()` → `"left"` / `"right"` / `"unknown"`.
- Produces: log lines that name the pod side instead of guessing it from the discovery order.

- [ ] **Step 1: Make the connect log lines side-aware** — `main.go`:

Replace:

```go
		if n == 1 {
			// First controller. Only the RIGHT pod's buttons are mapped; a lone
			// RIGHT pod's LED may keep blinking (advertising) — cosmetic, not a fault.
			slog.Info("controller connected — right pod supported (Y/Z/A/B/+); a lone right pod's LED may keep blinking (cosmetic)", "controller", t.Label)
		} else if n >= 2 {
			// Both pods of the pair are connected — the LEFT pod is the pair's BLE
			// anchor and must stay connected; only the RIGHT pod's buttons are mapped.
			slog.Info("more than one controller connected — only the right pod's buttons are mapped (left pod has just to stay on)", "controllers", n)
		}
```

with:

```go
		// The pod side comes from the advertisement's Zwift manufacturer
		// record, so the log names it instead of guessing from the
		// discovery order (the first controller found is often the LEFT one).
		if n == 1 && t.Side == zwift.PodRight {
			// A lone RIGHT pod keeps blinking (advertising) — cosmetic, not a fault.
			slog.Info("controller connected — a lone right pod's LED may keep blinking (cosmetic)", "controller", t.Label, "pod", t.Side.String())
		} else {
			slog.Info("controller connected", "controller", t.Label, "pod", t.Side.String(), "controllers", n)
		}
```

and add the import to `main.go`:

```go
	"zwiftboard/internal/ble"
	"zwiftboard/internal/config"
	"zwiftboard/internal/keys"
	"zwiftboard/internal/tui"
	"zwiftboard/internal/zwift"
```

- [ ] **Step 2: Fix the package comment** — `main.go`:

Replace:

```go
// Click V2 is a pair of devices (LEFT: arrows + minus, RIGHT: Y/Z/A/B + plus).
// Click V2 is a PAIR: both pods are connected (each gets its own session and
// keepalive). Connecting only the right pod, or merely detecting the left one,
// does not stop the right pod's ~65s idle drop; with both connected the link
// stays up. Only the right pod's buttons are normally mapped (Y Z A B PLUS).
```

with:

```go
// Click V2 is a pair of devices (LEFT: arrows + minus, RIGHT: Y/Z/A/B + plus).
// Click V2 is a PAIR: both pods are connected (each gets its own session and
// keepalive). Connecting only the right pod, or merely detecting the left one,
// does not stop the right pod's ~65s idle drop; with both connected the link
// stays up. Both pods' buttons are mappable (LEFT UP RIGHT DOWN MIN on the
// left pod, Y Z A B PLUS on the right); the shipped profiles map the right
// pod, the `notepad` profile maps both.
```

- [ ] **Step 3: Run the build**

Run: `go build ./... && go vet ./...`
Expected: no output, exit 0.

- [ ] **Step 4: Fix the README's intro bullet** — `README.md`:

Replace:

```markdown
> **Only the RIGHT pod fires keys.** The Click V2 is a pair and zwiftboard keeps
> _both_ pods connected, but the mapped buttons all live on the RIGHT pod
> (`Y` `Z` `A` `B` `PLUS`). The LEFT pod's arrows/minus are not used — it just
> has to stay switched on so the pair's link stays alive.
```

with:

```markdown
> **Both pods can fire keys.** The Click V2 is a pair and zwiftboard keeps
> _both_ pods connected. The RIGHT pod's buttons (`Y` `Z` `A` `B` `PLUS`) are
> mapped in every shipped profile; the LEFT pod's buttons (`LEFT` `UP` `RIGHT`
> `DOWN` `MIN`) can be mapped too — the shipped `notepad` profile does, so the
> left pod's arrows and minus are usable.
```

- [ ] **Step 5: Fix the README's `notepad` row** — `README.md`, the "Included profiles" table:

```markdown
| `notepad`       | `+ p`, `A a`, `B b`, `Y y`, `Z z`, plus the left pod's `LEFT l`, `UP u`, `RIGHT r`, `DOWN d`, `MIN m` | focuses `Notepad` and starts it — the shipped default |
```

- [ ] **Step 6: Fix the README's "How it works" paragraphs** — `README.md`:

Replace the step 5 paragraph:

```markdown
5. **Hold** — a mapped key goes **down** on the pressed transition and
   **up** on the released one, so holding a button holds the key (steering
   in MyWhoosh needs the left/right hold). While held, the key auto-repeats
   like a real keyboard (250ms delay, ~33ms interval — a synthetic press is
   otherwise a single event, so letters would not repeat in text apps).
   The pair mirrors every press (both units send the same frame), so dedup
   is edge-triggered: a handler only acts on bits that changed since the
   previous frame, and an identical mirrored bitmap changes nothing. If
   `focusProgramNamePrefixOnClick` is set, the configured window is brought
   to the foreground before the key goes out via Windows `keybd_event`.
```

with:

```markdown
5. **Hold** — a mapped key goes **down** on the pressed transition and
   **up** on the released one, so holding a button holds the key (steering
   in MyWhoosh needs the left/right hold). While held, the key auto-repeats
   like a real keyboard (250ms delay, ~33ms interval — a synthetic press is
   otherwise a single event, so letters would not repeat in text apps).
   The mirroring is asymmetric: the RIGHT pod's presses arrive on both
   units' streams, the LEFT pod's own presses only on the left unit's.
   Dedup is edge-triggered: a handler only acts on bits that changed since
   the previous frame, and an identical mirrored bitmap changes nothing. If
   `focusProgramNamePrefixOnClick` is set, the configured window is brought
   to the foreground before the key goes out via Windows `keybd_event`.
```

and replace the "Both pods connected, only the right mapped." paragraph:

```markdown
**Both pods connected, both mappable.** The Click V2 ships as two pods
that work as a _pair_: the LEFT pod is the pair's BLE anchor and the RIGHT pod
mirrors its state to the LEFT over a private RF link. This build connects _both_
pods — keeping the LEFT anchor connected is what keeps the RIGHT pod's link
alive — and both pods' buttons can be mapped. The shipped profiles map the
RIGHT pod (`Y` `Z` `A` `B` `PLUS`); `notepad` also maps the LEFT pod (`LEFT`
`UP` `RIGHT` `DOWN` `MIN`). Scanning never gives up, so a pod turned on
minutes later still joins.
```

- [ ] **Step 7: Record the facts in AGENTS.md** — `AGENTS.md`:

Replace the mirroring bullet:

```markdown
- The Click pair MIRRORS button state: one press arrives as the same frame
  from both units → dedup is edge-triggered per handler: only bits that
  changed since the previous frame act, so an identical mirrored bitmap
  changes nothing.
```

with:

```markdown
- The Click pair MIRRORS button state, but ASYMMETRICALLY: the RIGHT pod's
  buttons arrive as the same frame from both units, the LEFT pod's own
  buttons ONLY from the left unit (seen in zwiftboard.log, 2026-10-21). Dedup
  is edge-triggered per handler: only bits that changed since the previous
  frame act, so an identical mirrored bitmap changes nothing.
```

Replace in the "two-pod PAIR" bullet the sentence `only the RIGHT pod's buttons (Y Z A B PLUS) are normally mapped.` with:

```markdown
  both pods' buttons are mappable (LEFT UP RIGHT DOWN MIN on the left pod,
  Y Z A B PLUS on the right); the shipped profiles map the right pod, the
  `notepad` profile maps both.
```

and add a new bullet after the "Button frame `0x23`" bullet:

```markdown
- The held state is a per-VK COUNT per handler (`Holds.own`), not a flag: two
  different buttons of the SAME handler mapped to the SAME key (the mirrored
  `Z` plus the left pod's own `LEFT`, both bound to `left`) must hold the key
  until the LAST of them is released. With a flag the first release wiped it
  and the second release found nothing to lift — the key stayed down for the
  whole process. Every path gives back exactly what it took: `Press` counts
  up, `Release` counts down, the `Press` rollback counts down (it must never
  delete the whole VK), `ReleaseAll` subtracts the handler's whole count.
```

- [ ] **Step 8: Run the full suite**

Run: `just sanity`
Expected: fmt, `go test -race ./...`, vet and build all pass (exit 0).

- [ ] **Step 9: Commit**

```bash
git add main.go README.md AGENTS.md
git commit -m "docs: report the pod side at connect and document both pods"
```

---

## After the plan

1. `requesting-code-review` on the branch.
2. `verification-before-completion`: run `just sanity` and show its output.
3. `finishing-a-development-branch`: merge `feat/left-controller-support` back into
   `feat/2-controllers-support` (the repo's own pattern:
   `.worktrees/app-profiles` → `feature/app-profiles-shortcuts` → `main`).
4. Hand the user the hardware test:

```bash
# from the repo checkout, so the embedded default's notepad profile is loaded
go run . -config config.yaml -p notepad -v
```

Then press each left-pod button: `LEFT/UP/RIGHT/DOWN/MIN` must type `l u r d m`
in Notepad, once per press, and the status screen must show a `▶ pressed` row
for the button that was pressed.
