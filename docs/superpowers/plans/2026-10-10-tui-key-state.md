# TUI Key State (pressed / hold / released) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show the emulated keyboard key's live state — pressed, hold, released — in the TUI key table instead of the current 300ms "◀ triggered" flash.

**Architecture:** Rename the observational `ble.OnTap` hook to `ble.OnKey(button, down)` and fire it on BOTH transitions (press and release); add `ble.OnLifted(vk)` for keys force-released when a session drops mid-hold (no release edge ever arrives). The TUI model keeps a per-button `keyState{down, at}` map; the view derives the phase (pressed < 250ms, hold ≥ 250ms, released for 900ms after up) from that state and the clock. The mirrored pair fires each transition twice — the model ignores duplicates, so the display is idempotent. Nothing new gates a session: the hooks stay observational.

**Tech Stack:** Go, Bubble Tea, lipgloss (existing palette: green 42 = pressed, amber 214 = hold, muted 244 = released).

**Spec:** User request — "Update the UI with emulated keyboard keys state (pressed, hold, released), it's only 'triggered' at the moment. Note: do it as an UI/UX expert."

## Global Constraints

- Conventional commit, ONE atomic commit for the whole task: `feat(tui): ...`
- `go test ./...`, `go vet ./...`, `gofmt -l .` (CRLF noise on all files is pre-existing, content must be gofmt-clean), `GOOS=windows go build ./...` must all pass.
- `internal/tui` stays OBSERVATIONAL: no BLE import, fed only by messages; never gate a session.
- Keep `ble.OnKey`/`ble.OnLifted` non-blocking (fire-and-forget goroutines when calling `p.Send`, as `OnTap` did).
- Copy-on-write for the `keys` map (same pattern the `flash` map used — `Model` is copied by value).
- Labels are word+glyph+colour, never colour alone (repo convention).
- Do NOT touch the user's uncommitted `config.yaml` working-tree edit.

## UI Design (frontend-design applied to the terminal)

- Keep the existing token system (blue card accents, green/amber/red states) — no new palette.
- Third status column in the key table, aligned, quiet when idle:
  - **idle:** nothing (the column stays empty — restraint).
  - **pressed:** green inverted keycap + `▶ pressed` (bold, same energy as the old flash, but the word states the state).
  - **hold:** amber inverted keycap + braille pulse `⠹ hold` — the pulse frame advances every 100ms while a key is down (motion = the key is actively repeating, matching `keys.keyRepeatDelay`).
  - **released:** keycap back to normal, muted `○ released`, fades after 900ms.
- Redraw cadence: 500ms idle tick (unchanged, logs), 100ms while any key is down (pulse + timely pressed→hold flip).

---

### Task 1: TUI key state

**Files:**
- Modify: `internal/ble/buttons.go` (hooks)
- Modify: `internal/ble/buttons_test.go` (hook tests)
- Modify: `internal/tui/model.go` (state machine)
- Modify: `internal/tui/demo.go` (demo press cycle)
- Modify: `internal/tui/view.go` (status column)
- Modify: `internal/tui/model_test.go`, `internal/tui/view_test.go` (replace flash tests)
- Modify: `tuirun.go` (wire hooks), `tuirun_test.go` (vkIndex test)
- Modify: `README.md` (one sentence: table shows live key state)

**Interfaces:**
- Consumes: `ble.ButtonHandler` (edge-triggered press/release per pod), `Holds.ReleaseAll` (force-lift path), `keys.Binding{VK, Token}` from `cfg.Bindings`, existing `Model`/`Update`/`View`.
- Produces (for tests + wiring):
  - `ble.OnKey func(button string, down bool)` (replaces `ble.OnTap`)
  - `ble.OnLifted func(vk uint16)`
  - `tui.KeyStateMsg{Button string; Down bool}`
  - `func (m Model) keyPhase(button string) KeyPhase` with `PhaseIdle/PhasePressed/PhaseHold/PhaseReleased`
  - `func vkIndex(b map[string]keys.Binding) map[uint16][]string` (package main)

- [ ] **Step 1: Write the failing tests (ble)**

Replace `TestOnTapFiresPerPressedTransition` and `TestOnTapNotCalledUnmapped` in `internal/ble/buttons_test.go`; add the lift test. New content:

```go
// OnKey reports BOTH transitions of a mapped button. The mirrored frame from
// the pair's other unit fires it again — observational only (the UI dedups),
// the key itself is pressed once.
func TestOnKeyReportsBothTransitions(t *testing.T) {
	rec := &keyRecorder{}
	type ev struct {
		btn string
		down bool
	}
	var got []ev
	OnKey = func(b string, down bool) { got = append(got, ev{b, down}) }
	t.Cleanup(func() { OnKey = nil })

	rHK := holdKeys(rec.downFn, rec.upFn)
	lHK := holdKeys(rec.downFn, rec.upFn)
	right := ButtonHandler("right", map[string]keys.Binding{"B": {VK: 0x42, Token: "b"}}, rHK.Press, rHK.Release)
	left := ButtonHandler("left", map[string]keys.Binding{"B": {VK: 0x42, Token: "b"}}, lHK.Press, lHK.Release)
	t.Cleanup(rHK.ReleaseAll)
	t.Cleanup(lHK.ReleaseAll)

	press := []byte{0x23, 0x08, 0xDF, 0xFF, 0xFF, 0xFF, 0x0F}
	idle := []byte{0x23, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}
	right(press)
	left(press) // mirrored duplicate
	if len(got) != 2 || got[0] != (ev{"B", true}) || got[1] != (ev{"B", true}) {
		t.Fatalf("OnKey after press = %v, want two downs", got)
	}
	if down, _ := rec.counts(); down != 1 {
		t.Fatalf("mirrored press downs = %d, want 1", down)
	}
	right(idle)
	left(idle) // mirrored release
	if len(got) != 4 || got[2] != (ev{"B", false}) || got[3] != (ev{"B", false}) {
		t.Fatalf("OnKey after release = %v, want two ups", got)
	}
	if _, up := rec.counts(); up != 1 {
		t.Fatalf("mirrored release ups = %d, want 1", up)
	}
}

func TestOnKeyNotCalledUnmapped(t *testing.T) {
	called := false
	OnKey = func(string, bool) { called = true }
	t.Cleanup(func() { OnKey = nil })

	h := ButtonHandler("t", map[string]keys.Binding{}, func(keys.Binding) {}, func(keys.Binding) {})
	h([]byte{0x23, 0x08, 0xDF, 0xFF, 0xFF, 0xFF, 0x0F})
	if called {
		t.Fatal("OnKey must not fire for an unmapped button")
	}
}

// A session dropped mid-hold never sees the release edge: ReleaseAll must
// report each force-lifted VK so the UI can clear the held row.
func TestReleaseAllLiftNotifiesOnLifted(t *testing.T) {
	rec := &keyRecorder{}
	var lifted []uint16
	OnLifted = func(vk uint16) { lifted = append(lifted, vk) }
	t.Cleanup(func() { OnLifted = nil })

	hk := holdKeys(rec.downFn, rec.upFn)
	h := ButtonHandler("t", map[string]keys.Binding{"A": {VK: 0x41, Token: "a"}}, hk.Press, hk.Release)
	h([]byte{0x23, 0x08, 0xEF, 0xFF, 0xFF, 0xFF, 0x0F})
	hk.ReleaseAll()
	if len(lifted) != 1 || lifted[0] != 0x41 {
		t.Fatalf("OnLifted calls = %v, want [0x41]", lifted)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/ble/ -run 'OnKey|OnLifted'`
Expected: FAIL — `undefined: OnKey` / `undefined: OnLifted`.

- [ ] **Step 3: Implement the hooks in `internal/ble/buttons.go`**

Replace the `OnTap` declaration (lines 12-17) with:

```go
// OnKey, when set, is told every transition of a mapped button: down=true on
// press, down=false on release (the UI's live key state). Observational only:
// it runs on the BLE notification goroutine and must not block, since nothing
// in a session waits on it. The pair mirrors every frame, so one physical
// transition fires it twice — the UI dedups.
var OnKey func(button string, down bool)

// OnLifted, when set, is told each VK force-released by ReleaseAll (a session
// dropped mid-hold: the release edge never arrived). Normal releases are
// reported through OnKey instead. Same observational rules.
var OnLifted func(vk uint16)
```

In `ButtonHandler`, replace:

```go
			if state == "pressed" {
				down(bnd)
				if h := OnTap; h != nil {
					h(name)
				}
			} else {
				up(bnd)
			}
```

with:

```go
			if state == "pressed" {
				down(bnd)
				if h := OnKey; h != nil {
					h(name, true)
				}
			} else {
				up(bnd)
				if h := OnKey; h != nil {
					h(name, false)
				}
			}
```

In `ReleaseAll`, replace the lift loop:

```go
	for _, vk := range lift {
		keys.ReleaseVK(vk)
	}
```

with:

```go
	for _, vk := range lift {
		keys.ReleaseVK(vk)
		if h := OnLifted; h != nil {
			h(vk)
		}
	}
```

- [ ] **Step 4: Run the ble tests to verify they pass**

Run: `go test ./internal/ble/`
Expected: PASS (all).

- [ ] **Step 5: Write the failing model tests**

In `internal/tui/model_test.go`, delete every test touching the old flash system (`TestClickFlashesMappedButton` ~line 26, the stale-flash test ~lines 41-57, the unmapped-click test, `TestDemoDigitClicks` ~line 106). Find them all with `grep -n 'ClickMsg\|expireMsg\|Flashing\|flash' internal/tui/*_test.go` — no reference may remain after this step. Add:

```go
// clock returns a model with a fake clock and presses/releases helpers.
func keyModel() (Model, *time.Time) {
	now := time.Now()
	m := New(testCfg())
	m.clock = func() time.Time { return now }
	return m, &now
}

func TestKeyDownShowsPressedThenHold(t *testing.T) {
	m, now := keyModel()
	next, _ := m.Update(KeyStateMsg{Button: "A", Down: true})
	m = next.(Model)
	if p := m.keyPhase("A"); p != PhasePressed {
		t.Fatalf("phase right after down = %v, want pressed", p)
	}
	*now = now.Add(300 * time.Millisecond) // past holdAfter
	if p := m.keyPhase("A"); p != PhaseHold {
		t.Fatalf("phase after 300ms = %v, want hold", p)
	}
}

func TestKeyUpShowsReleasedThenIdle(t *testing.T) {
	m, now := keyModel()
	next, _ := m.Update(KeyStateMsg{Button: "A", Down: true})
	next, _ = next.Update(KeyStateMsg{Button: "A", Down: false})
	m = next.(Model)
	if p := m.keyPhase("A"); p != PhaseReleased {
		t.Fatalf("phase after up = %v, want released", p)
	}
	*now = now.Add(releaseFor)
	next, _ = m.Update(keyExpireMsg{button: "A"})
	if p := next.(Model).keyPhase("A"); p != PhaseIdle {
		t.Fatalf("phase after expiry = %v, want idle", p)
	}
	if _, ok := next.(Model).keys["A"]; ok {
		t.Fatal("expired key must leave the map")
	}
}

func TestKeyMirrorDedup(t *testing.T) {
	m, _ := keyModel()
	next, _ := m.Update(KeyStateMsg{Button: "A", Down: true})
	first := next.(Model).keys["A"].at
	next, _ = next.Update(KeyStateMsg{Button: "A", Down: true}) // mirrored pod
	if got := next.(Model).keys["A"].at; !got.Equal(first) {
		t.Fatal("duplicate down must not restamp the press")
	}
	next, _ = next.Update(KeyStateMsg{Button: "A", Down: false})
	next, _ = next.Update(KeyStateMsg{Button: "A", Down: false}) // mirrored release
	if p := next.(Model).keyPhase("A"); p != PhaseReleased {
		t.Fatalf("phase after duplicate up = %v, want released", p)
	}
}

func TestKeyStateIgnoresUnmapped(t *testing.T) {
	m, _ := keyModel()
	next, cmd := m.Update(KeyStateMsg{Button: "X", Down: true})
	if cmd != nil || len(next.(Model).keys) != 0 {
		t.Fatal("unmapped button must not enter the key state")
	}
}

func TestStaleExpiryDoesNotClearHeldKey(t *testing.T) {
	m, _ := keyModel()
	next, _ := m.Update(KeyStateMsg{Button: "A", Down: true})
	next, _ = next.Update(KeyStateMsg{Button: "A", Down: false})
	next, _ = next.Update(KeyStateMsg{Button: "A", Down: true}) // re-pressed
	next, _ = next.Update(keyExpireMsg{button: "A"})            // stale fade timer
	if p := next.(Model).keyPhase("A"); p == PhaseIdle {
		t.Fatal("stale expiry must not clear a re-pressed key")
	}
}

func TestDemoPressRunsFullCycle(t *testing.T) {
	n, cmd := demoModel().Update(key("3")) // Buttons[2] = "A"
	if cmd == nil {
		t.Fatal("digit should emit a cmd")
	}
	// Batch: fast-tick + scheduled release.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("demo press cmds = %v, want a 2-command batch", cmd())
	}
	n, _ = n.Update(batch[0]())
	if n.(Model).keyPhase("A") != PhasePressed {
		t.Fatal("3 should press A")
	}
	up := batch[1]() // fires demoHoldFor later
	n, _ = n.Update(up)
	if n.(Model).keyPhase("A") != PhaseReleased {
		t.Fatal("scheduled up should release A")
	}
}
```

Note: `keyModel` sets the unexported `clock` field the model gains in Step 6 — write the tests first, they fail to compile (`undefined: keyPhase`, `clock` field), which is the red state.

- [ ] **Step 6: Run model tests to verify they fail**

Run: `go test ./internal/tui/`
Expected: FAIL — `undefined: KeyStateMsg`, `unknown field clock`, `undefined: keyPhase`, etc.

- [ ] **Step 7: Implement the model state machine**

In `internal/tui/model.go`:

1. Replace the `FlashFor` const (line 12-13) and the `ClickMsg`/`expireMsg` declarations with:

```go
// KeyStateMsg reports one emulated key's transition: Down true on press,
// false on release. The mirrored pair fires it twice; Update dedups.
type KeyStateMsg struct {
	Button string
	Down   bool
}

// demoPressMsg starts a demo press: down now, a scheduled KeyStateMsg up
// after demoHoldFor, so the screen walks pressed → hold → released.
type demoPressMsg struct{ button string }

// keyExpireMsg clears a released key's fade-out row.
type keyExpireMsg struct{ button string }

// KeyPhase is what a key row shows. Derived from keyState + clock.
type KeyPhase int

const (
	PhaseIdle KeyPhase = iota
	PhasePressed
	PhaseHold
	PhaseReleased
)

const (
	// holdAfter mirrors keys.keyRepeatDelay: pressed flips to hold when the
	// emulated auto-repeat would have started.
	holdAfter = 250 * time.Millisecond
	// releaseFor is how long the released row lingers before vanishing.
	releaseFor = 900 * time.Millisecond
	// holdTick is the redraw cadence while a key is down (pulse animation).
	holdTick = 100 * time.Millisecond
	// demoHoldFor: the demo key stays down long enough to pass through hold.
	demoHoldFor = 1200 * time.Millisecond
)
```

Delete `expireMsg` (and its `Update` case) and the `ClickMsg` case.

2. Model fields: remove `flash map[string]int` and `seq int`; add

```go
	keys  map[string]keyState // live emulated-key state, copy-on-write
	clock func() time.Time    // test hook; nil = time.Now
```

3. `New`:

```go
// New returns a model with Bluetooth off, both pods not detected and no key
// state.
func New(cfg Config) Model {
	return Model{cfg: cfg, keys: map[string]keyState{}}
}
```

4. Replace `Flashing` with the phase helpers:

```go
// keyState is one emulated key's state: at is the press time while down and
// the release time while up.
type keyState struct {
	down bool
	at   time.Time
}

func (m Model) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

// keyPhase derives the row's phase: pressed < holdAfter, hold until release,
// released for releaseFor, then idle (the entry is removed by keyExpireMsg).
func (m Model) keyPhase(button string) KeyPhase {
	s, ok := m.keys[button]
	if !ok {
		return PhaseIdle
	}
	age := m.now().Sub(s.at)
	switch {
	case s.down && age < holdAfter:
		return PhasePressed
	case s.down:
		return PhaseHold
	case age < releaseFor:
		return PhaseReleased
	default:
		return PhaseIdle
	}
}

// anyKeyDown reports whether any emulated key is held (faster redraw tick).
func (m Model) anyKeyDown() bool {
	for _, s := range m.keys {
		if s.down {
			return true
		}
	}
	return false
}
```

5. Tick handling — replace the `tick()` function and the `Init`/`tickMsg` uses:

```go
// tickCmd schedules the next redraw: fast while a key is down (the hold
// pulse), slow otherwise (log lines).
func (m Model) tickCmd() tea.Cmd {
	d := refreshEvery
	if m.anyKeyDown() {
		d = holdTick
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) Init() tea.Cmd { return m.tickCmd() }
```

In `Update`, `case tickMsg:` becomes `return m, m.tickCmd()`.

6. In `Update`, add (replacing the old `ClickMsg`/`expireMsg` cases):

```go
	case KeyStateMsg:
		if _, mapped := m.cfg.Bindings[msg.Button]; !mapped {
			return m, nil
		}
		if msg.Down {
			return m.keyDown(msg.Button)
		}
		return m.keyUp(msg.Button)
	case demoPressMsg:
		if _, mapped := m.cfg.Bindings[msg.button]; !mapped {
			return m, nil
		}
		next, fast := m.keyDown(msg.button)
		return next, tea.Batch(fast, tea.Tick(demoHoldFor, func(time.Time) tea.Msg {
			return KeyStateMsg{Button: msg.button, Down: false}
		}))
	case keyExpireMsg:
		if s, ok := m.keys[msg.button]; ok && !s.down {
			k := make(map[string]keyState, len(m.keys))
			for n, v := range m.keys {
				k[n] = v
			}
			delete(k, msg.button)
			m.keys = k
		}
```

7. Replace `click` with:

```go
// keyDown records a press (copy-on-write, like the old flash map) and asks
// for an early redraw so pressed → hold flips on time. A duplicate down — the
// pair's mirrored frame — is ignored, keeping the first press's stamp.
func (m Model) keyDown(button string) (tea.Model, tea.Cmd) {
	if s, ok := m.keys[button]; ok && s.down {
		return m, nil
	}
	k := make(map[string]keyState, len(m.keys)+1)
	for n, v := range m.keys {
		k[n] = v
	}
	k[button] = keyState{down: true, at: m.now()}
	m.keys = k
	return m, tea.Tick(holdTick, func(time.Time) tea.Msg { return tickMsg{} })
}

// keyUp records a release and schedules the fade-out expiry. A duplicate up
// (mirrored frame) or an up for a key never seen down is ignored.
func (m Model) keyUp(button string) (tea.Model, tea.Cmd) {
	s, ok := m.keys[button]
	if !ok || !s.down {
		return m, nil
	}
	k := make(map[string]keyState, len(m.keys))
	for n, v := range m.keys {
		k[n] = v
	}
	k[button] = keyState{down: false, at: m.now()}
	m.keys = k
	return m, tea.Tick(releaseFor, func(time.Time) tea.Msg { return keyExpireMsg{button} })
}
```

- [ ] **Step 8: Run model tests to verify they pass**

Run: `go test ./internal/tui/ -run 'Key|Demo'`
Expected: PASS. (View tests still fail — flash rendering goes next.)

- [ ] **Step 9: Write the failing view test + demo change**

In `internal/tui/view_test.go`, replace `TestViewFlashShowsTriggered` with:

```go
func TestViewKeyStates(t *testing.T) {
	m := on(testCfg(), PodConnected, PodConnected)
	m.clock = func() time.Time { return time.Now() }
	next, _ := m.Update(KeyStateMsg{Button: "A", Down: true})
	has(t, next.(Model).View(), "▶ pressed")
	lacks(t, next.(Model).View(), "released")

	// Force the hold phase by backdating the press.
	m2 := next.(Model)
	s := m2.keys["A"]
	s.at = time.Now().Add(-holdAfter - time.Millisecond)
	k := make(map[string]keyState, 1)
	k["A"] = s
	m2.keys = k
	has(t, m2.View(), "hold")

	// Released: normal keycap, muted word.
	m3 := m2
	k2 := make(map[string]keyState, 1)
	k2["A"] = keyState{down: false, at: time.Now()}
	m3.keys = k2
	v := m3.View()
	has(t, v, "released")
	lacks(t, v, "▶ pressed")
}
```

Add `"time"` to the test file's imports if missing.

In `internal/tui/demo.go`, replace the digit branch:

```go
	default:
		if len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
			if i := int(k[0] - '1'); i < len(m.cfg.Buttons) {
				name := m.cfg.Buttons[i]
				return m, func() tea.Msg { return demoPressMsg(name) }
			}
		}
```

- [ ] **Step 10: Run all tui tests to verify they pass**

Run: `go test ./internal/tui/`
Expected: PASS (all).

- [ ] **Step 11: Implement the view status column**

In `internal/tui/view.go`:

1. Remove `sFlash`, add:

```go
	sDown    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(cOK).Padding(0, 1)
	sHold    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(cWarn).Padding(0, 1)
```

2. Add the pulse (near `padRight`):

```go
// pulseFrames advance one frame per holdTick: the braille spinner is the
// "key is repeating" motion cue while a key is held.
var pulseFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func pulse(now time.Time) string {
	return pulseFrames[int(now.UnixNano()/int64(holdTick))%len(pulseFrames)]
}
```

Add `"time"` to the imports.

3. Rewrite the row loop in `table()`:

```go
		for _, name := range m.cfg.Buttons {
			key, ok := m.cfg.Bindings[name]
			cap := sMuted.Render("—")
			status := ""
			if ok {
				cap = sKeycap.Render(key)
				switch m.keyPhase(name) {
				case PhasePressed:
					cap = sDown.Render(key)
					status = sDown.Render("▶ pressed")
				case PhaseHold:
					cap = sHold.Render(key)
					status = sHold.Render(pulse(m.now()) + " hold")
				case PhaseReleased:
					status = sMuted.Render("○ released")
				}
			}
			b.WriteString(sBold.Render(padRight(name, 6)) + sMuted.Render("→ ") + cap +
				sMuted.Render("  ") + padRight(status, 12) + "\n")
		}
```

(`padRight` pads the ANSI-free logical width fine here because a plain padded status is appended after styles; for the coloured statuses pad BEFORE styling is wrong — so pad a plain version and style it: build `label := "▶ pressed"` etc., pad the label, then render. Adjust: keep `status` as PLAIN text, render once at write time:

```go
			label := ""
			var style lipgloss.Style
			if ok {
				cap = sKeycap.Render(key)
				switch m.keyPhase(name) {
				case PhasePressed:
					cap = sDown.Render(key)
					label, style = "▶ pressed", sDown
				case PhaseHold:
					cap = sHold.Render(key)
					label, style = pulse(m.now())+" hold", sHold
				case PhaseReleased:
					label, style = "○ released", sMuted
				}
			}
			out := padRight(label, 12)
			if label != "" {
				out = style.Render(padRight(label, 12))
			}
			b.WriteString(sBold.Render(padRight(name, 6)) + sMuted.Render("→ ") + cap + "  " + out + "\n")
```

Use this second form — it keeps the column aligned (padding inside the style).)

- [ ] **Step 12: Run the full test suite**

Run: `go test ./...`
Expected: PASS. Remaining failures (if any) are leftover flash references — fix them the same way.

- [ ] **Step 13: Wire the hooks in `tuirun.go`**

Add `vkIndex` (package main, bottom of `tuirun.go`):

```go
// vkIndex maps a VK back to every button mapped to it: ReleaseAll force-lifts
// by VK (the button name is not in hand), so the UI needs the reverse lookup.
func vkIndex(b map[string]keys.Binding) map[uint16][]string {
	m := map[uint16][]string{}
	for name, bnd := range b {
		m[bnd.VK] = append(m[bnd.VK], name)
	}
	return m
}
```

Replace the `ble.OnTap = ...` line in `liveFeed` (line 81) with:

```go
	// Set before any session exists (no race). Send blocks until the program
	// loop takes the message, and the hooks run on BLE notification /
	// session-goroutines, so hand it off: a busy screen must never stall
	// button handling.
	ble.OnKey = func(button string, down bool) {
		go p.Send(tui.KeyStateMsg{Button: button, Down: down})
	}
	idx := vkIndex(cfg.Bindings)
	ble.OnLifted = func(vk uint16) {
		for _, name := range idx[vk] {
			n := name
			go p.Send(tui.KeyStateMsg{Button: n, Down: false})
		}
	}
```

Update the stale `OnTap` comments in `liveFeed`'s doc block (line 61) to `OnKey`.

In `tuirun_test.go` add:

```go
func TestVKIndex(t *testing.T) {
	idx := vkIndex(map[string]keys.Binding{
		"A": {VK: 0x41, Token: "a"},
		"B": {VK: 0x41, Token: "a"}, // two buttons, same key
		"Z": {VK: 0x5A, Token: "z"},
	})
	if got := idx[0x41]; len(got) != 2 {
		t.Fatalf("0x41 buttons = %v, want A and B", got)
	}
	if got := idx[0x5A]; len(got) != 1 || got[0] != "Z" {
		t.Fatalf("0x5A buttons = %v, want [Z]", got)
	}
}
```

- [ ] **Step 14: Run everything + build**

Run: `go test ./... && go vet ./... && GOOS=windows go build ./...`
Expected: all green. Then `gofmt -l .` — only pre-existing CRLF noise (whole-file diffs of line endings), no content diffs for edited files: check with `gofmt -d internal/tui/model.go | grep '^[+-][^+-]'` style spot check if in doubt.

- [ ] **Step 15: Update README (one sentence)**

In `README.md` line 69, replace `a button row lights up briefly when clicked.` with
`each button row shows the emulated key's live state: ▶ pressed while down, braille-pulsed hold while it auto-repeats, ○ released fading after the button comes up.`

- [ ] **Step 16: Commit (atomic)**

```bash
git add internal/ble/buttons.go internal/ble/buttons_test.go \
        internal/tui/model.go internal/tui/demo.go internal/tui/view.go \
        internal/tui/model_test.go internal/tui/view_test.go \
        tuirun.go tuirun_test.go README.md
git commit -m "feat(tui): show pressed, hold and released emulated key state"
```

Body: explain the three phases (pressed < 250ms mirrors keys.keyRepeatDelay, hold pulse at 100ms redraw, released fades 900ms), the OnTap→OnKey rename (both transitions; AGENTS.md already named the hook OnKey), OnLifted for the session-drop path that never sees a release edge, and the mirrored-pair dedup (first stamp wins).
