package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"zwiftboard/internal/config"
)

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	os.Exit(m.Run())
}

func on(cfg Config, l, r PodState) Model {
	m := New(cfg)
	m.bt, m.left, m.right = true, l, r
	return m
}

func has(t *testing.T, v tea.View, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(v.Content, s) {
			t.Errorf("view missing %q:\n%s", s, v.Content)
		}
	}
}

func lacks(t *testing.T, v tea.View, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if strings.Contains(v.Content, s) {
			t.Errorf("view should not contain %q:\n%s", s, v.Content)
		}
	}
}

func TestViewBTOffHaltsSetup(t *testing.T) {
	m := New(testCfg())
	m.left, m.right = PodConnected, PodConnected // stale poll
	v := m.View()
	has(t, v, "Bluetooth", "turn")
	lacks(t, v, "Left Controller", "key bindings")
}

func TestViewInlinePrompts(t *testing.T) {
	v := on(testCfg(), PodConnected, PodOff).View()
	has(t, v, "Right Controller: not detected → please activate the Right Controller")
	lacks(t, v, "please activate the Left Controller", "disconnected")
	v = on(testCfg(), PodOff, PodOff).View()
	has(t, v, "Left Controller: not detected → please activate the Left Controller",
		"Right Controller: not detected → please activate the Right Controller")
	v = on(testCfg(), PodDetected, PodDetected).View()
	lacks(t, v, "please activate")
}

func TestViewVersion(t *testing.T) {
	// "dev" is the on-disk build stamp; on release main stamps it via
	// -X main.version. The dev build drives it through the Config field; the
	// release binary carries the release tag (e.g. "1.2.3").
	// "dev" is the on-disk build stamp; on release main stamps it via
	// -X main.version. The dev build drives it through the Config field; the
	// release binary carries the release tag (e.g. "1.2.3"). An empty version
	// hides the suffix (BT-off path shows the bare title).
	for _, v := range []string{"", "dev", "1.2.3", "1.2.3.0"} {
		m := on(testCfg(), PodConnected, PodConnected)
		m.cfg.Version = v
		t.Run(v, func(t *testing.T) {
			out := m.View()
			lacks(t, out, "zwiftboarddev") // title not glued to version
			if v == "" {
				lacks(t, out, "zwiftboard dev")
			} else {
				has(t, out, v) // e.g. "zwiftboard ... (version dev)" / "... 1.2.3"
			}
		})
	}
}

func TestViewUpdateChip(t *testing.T) {
	m := on(testCfg(), PodConnected, PodConnected)
	m.cfg.Version = "1.0.1"
	m.latest = "1.0.2"
	v := m.View()
	has(t, v, "1.0.1", "→ version 1.0.2 available") // version and the update notice

	// No update result → no notice, no second version.
	m.latest = ""
	v = m.View()
	has(t, v, "1.0.1", "version 1.0.1")
	lacks(t, v, "→ version", "1.0.2", "available") // table rows always contain a bare "→"
}

func TestViewIntroLine(t *testing.T) {
	v := on(testCfg(), PodOff, PodConnected).View()
	has(t, v, "WAITING", "Both controllers must be ON")
	lacks(t, v, "Both pods send clicks", "READY")
	v = on(testCfg(), PodConnected, PodConnected).View()
	has(t, v, "READY", "Both controllers connected", "Both pods send clicks")
	lacks(t, v, "Both controllers must be ON", "WAITING")
}

func TestViewTable(t *testing.T) {
	v := on(testCfg(), PodConnected, PodConnected).View()
	has(t, v, "test", "PLUS", "+")
}

func TestProfileMsgUpdatesHeader(t *testing.T) {
	n, _ := New(testCfg()).Update(ProfileMsg{Name: "systm", Index: 2, Bindings: map[string]string{"A": "space"}})
	if !strings.Contains(n.(Model).View().Content, "systm") {
		t.Fatal("profile not shown")
	}
}

func TestViewFocus(t *testing.T) {
	cfg := testCfg()
	cfg.Focus = "MyWhoosh"
	has(t, on(cfg, PodConnected, PodConnected).View(), "MyWhoosh")
	has(t, on(testCfg(), PodConnected, PodConnected).View(), "off")
}

func TestViewStartProgram(t *testing.T) {
	// The table only renders once Bluetooth is on (the BT-off screen is the
	// minimal "turn it on" prompt), so drive the BT-on view like TestViewFocus.
	cfg := testCfg()
	cfg.StartProgram = "MyWhooshHD.exe"
	has(t, on(cfg, PodConnected, PodConnected).View(), "MyWhooshHD.exe")
	lacks(t, on(testCfg(), PodConnected, PodConnected).View(), "Start program")
}

func TestViewNoMapping(t *testing.T) {
	cfg := testCfg()
	cfg.NoMapping = true
	has(t, on(cfg, PodConnected, PodConnected).View(), "no key mapping")
}

func TestViewZeroSize(t *testing.T) {
	_ = New(testCfg()).View()
	_ = on(testCfg(), PodOff, PodDetected).View()
}

func TestViewLogPanel(t *testing.T) {
	logs := NewLogBuffer(10)
	logs.Write([]byte("level=INFO msg=hello-from-slog\n"))
	cfg := testCfg()
	cfg.Logs = logs
	m := on(cfg, PodConnected, PodConnected)
	lacks(t, m.View(), "hello-from-slog")
	has(t, m.View(), "ctrl+l logs")
	m.showLogs = true
	has(t, m.View(), "hello-from-slog", "Logs")
}

func TestViewLogPanelWithoutBuffer(t *testing.T) {
	m := on(testCfg(), PodConnected, PodConnected)
	m.showLogs = true
	has(t, m.View(), "no log output")
}

func TestViewNarrowWidthDoesNotPanic(t *testing.T) {
	for _, w := range []int{0, 1, 10, 30, 200} {
		m := on(testCfg(), PodOff, PodOff)
		m.width = w
		m.showLogs = true
		_ = m.View()
	}
}

func focusCfg() Config {
	cfg := testCfg()
	cfg.Focus = "MyWhoosh"
	return cfg
}

func TestViewFocusMissingAlert(t *testing.T) {
	m := on(focusCfg(), PodConnected, PodConnected)
	lacks(t, m.View(), "not found") // nothing reported yet: no false alarm
	m.focusMissing = true
	has(t, m.View(), "ALERT", `"MyWhoosh" not found`, "clicks are dropped")
	cfg := testCfg() // no focus configured: never alert
	m = on(cfg, PodConnected, PodConnected)
	m.focusMissing = true
	lacks(t, m.View(), "ALERT", "not found")
}

func TestViewLogPanelWiderThanCards(t *testing.T) {
	logs := NewLogBuffer(5)
	line := strings.Repeat("x", 130)
	logs.Write([]byte(line + "\n"))
	cfg := testCfg()
	cfg.Logs = logs
	m := on(cfg, PodConnected, PodConnected)
	m.showLogs = true
	m.width = 170
	has(t, m.View(), line)
	m.width = 80
	v := m.View()
	lacks(t, v, line)
	has(t, v, "…")
}

func TestViewFooterOpenConfigHint(t *testing.T) {
	has(t, on(openCfg(func() error { return nil }), PodConnected, PodConnected).View(), "ctrl+e edit")
	lacks(t, on(testCfg(), PodConnected, PodConnected).View(), "edit")
}

func TestViewFooterProfileSwitchHint(t *testing.T) {
	cfg := testCfg()
	cfg.SwitchProfile = func(int) {}
	cfg.Profiles = []string{"a", "b"}
	has(t, on(cfg, PodConnected, PodConnected).View(), "ctrl+←/→ switch profile")

	// A single profile hides the hint even though switching is wired.
	one := testCfg()
	one.SwitchProfile = func(int) {}
	one.Profiles = []string{"a"}
	lacks(t, on(one, PodConnected, PodConnected).View(), "switch profile")

	lacks(t, on(testCfg(), PodConnected, PodConnected).View(), "switch profile")
}

func TestViewFooterActionHints(t *testing.T) {
	cfg := testCfg()
	cfg.Reload = func() {}
	cfg.LaunchProgram = func() error { return nil }
	cfg.StartProgram = "MyWhooshHD.exe"
	v := on(cfg, PodConnected, PodConnected).View()
	has(t, v, "ctrl+r reload", "ctrl+s start")

	// The reload hint is hidden without the callback, the start hint without a
	// program on the active profile.
	lacks(t, on(testCfg(), PodConnected, PodConnected).View(), "ctrl+r reload", "ctrl+s start")
	cfg.StartProgram = ""
	lacks(t, on(cfg, PodConnected, PodConnected).View(), "ctrl+s start")
}

func TestViewFooterSignInHint(t *testing.T) {
	// A wired login advertises the "ctrl+z" sign-in shortcut in the footer.
	auth := testCfg()
	auth.Auth = AuthConfig{Login: func(string, string) error { return nil }}
	has(t, on(auth, PodConnected, PodConnected).View(), "ctrl+z sign in")

	// No login wired (demo): the shortcut is inert, so the footer hides it.
	lacks(t, on(testCfg(), PodConnected, PodConnected).View(), "ctrl+z sign in")
}

// The header carries the four-dot pod mark in front of the title, all on one
// line: the app icon's dots (cyan, orange, green, magenta).
func TestViewHeaderPodLogo(t *testing.T) {
	m := on(testCfg(), PodConnected, PodConnected)
	h := m.header()
	if strings.Contains(h, "\n") {
		t.Fatalf("header should be a single line:\n%q", h)
	}
	if n := strings.Count(h, "●"); n != 4 {
		t.Errorf("header has %d pod dots, want 4:\n%s", n, h)
	}
	if !strings.HasPrefix(h, "● ● ● ● zwiftboard") {
		t.Errorf("header = %q, want four dots before the title", h)
	}
}

func TestViewHeaderProfileIndex(t *testing.T) {
	cfg := testCfg()
	cfg.Profile = "systm"
	cfg.Profiles = []string{"a", "systm", "c"}
	cfg.ProfileIndex = 1
	has(t, on(cfg, PodConnected, PodConnected).View(), "systm (2/3)")
}

func TestViewShowsStatus(t *testing.T) {
	m := on(testCfg(), PodConnected, PodConnected)
	m.status = "Opened config.yaml"
	has(t, m.View(), "Opened config.yaml")
	m.bt = false // also visible while Bluetooth is off
	has(t, m.View(), "Opened config.yaml")
}

func TestViewLogPanelDoubleWidth(t *testing.T) {
	logs := NewLogBuffer(5)
	line := strings.Repeat("y", 290)
	logs.Write([]byte(line + "\n"))
	cfg := testCfg()
	cfg.Logs = logs
	m := on(cfg, PodConnected, PodConnected)
	m.showLogs = true
	m.width = 320
	has(t, m.View(), line)
}

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

// padRight must pad to a display-cell width, not a byte count: the status
// labels carry multibyte glyphs (▶, ○, the braille pulse), so byte padding left
// the status column misaligned. "▶ pressed" is 11 bytes but 9 cells.
func TestPadRightUsesDisplayWidth(t *testing.T) {
	for _, s := range []string{"▶ pressed", "○ released", "pressed", "backspace"} {
		if w := lipgloss.Width(padRight(s, 12)); w != 12 {
			t.Errorf("padRight(%q) display width = %d, want 12", s, w)
		}
	}
}

// doubleCfg is the test config in double mode with a left-pod binding, so the
// table shows both controllers.
func doubleCfg() Config {
	cfg := testCfg()
	cfg.Mode = config.ModeDouble
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
	m := on(doubleCfg(), PodConnected, PodConnected)
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
	m := on(doubleCfg(), PodConnected, PodConnected)
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
	m := on(doubleCfg(), PodConnected, PodConnected)
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
		m := on(doubleCfg(), PodConnected, PodConnected)
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
		// One button per column: the widest row is the one where BOTH
		// cells carry a status label (the labelled cell is 14, not 12),
		// so a single pressed column would not measure the worst case.
		next, _ := m.Update(KeyStateMsg{Button: "LEFT", Down: true})
		next, _ = next.Update(KeyStateMsg{Button: "A", Down: true})
		check(next.(Model), "pressed")
	}
}

// Single mode shows the right controller alone and no controller heading.
func TestViewKeyBindingsSingleModeShowsRightOnly(t *testing.T) {
	m := on(testCfg(), PodConnected, PodConnected)
	m.width = 120
	table := m.table()
	if strings.Contains(ansi.Strip(table), leftHeading) || strings.Contains(table, "LEFT") {
		t.Fatalf("single mode must not show the left controller:\n%s", table)
	}
	if got := buttonRows(table); got != 5 {
		t.Errorf("table has %d rows, want 5 (the right controller):\n%s", got, table)
	}
}

// Double mode labels both controllers and shows all ten buttons, side by side
// when the card is wide.
func TestViewKeyBindingsDoubleModeLabelsBothControllers(t *testing.T) {
	m := on(doubleCfg(), PodConnected, PodConnected)
	m.width = 120
	table := ansi.Strip(m.table())
	for _, h := range []string{leftHeading, rightHeading} {
		if !strings.Contains(table, h) {
			t.Fatalf("double mode missing heading %q:\n%s", h, table)
		}
	}
	if strings.Index(table, leftHeading) > strings.Index(table, rightHeading) {
		t.Errorf("left heading must come before the right heading:\n%s", table)
	}
	if got := buttonRows(m.table()); got != 5 {
		t.Errorf("table has %d rows, want 5 (ten buttons in two columns):\n%s", got, table)
	}
}

// Narrow double mode stacks the controllers, the left block first, with each
// heading above its own rows.
func TestViewKeyBindingsDoubleModeStackedHeadings(t *testing.T) {
	m := on(doubleCfg(), PodConnected, PodConnected)
	m.width = 60
	table := ansi.Strip(m.table())
	left, right := strings.Index(table, leftHeading), strings.Index(table, rightHeading)
	if left < 0 || right < 0 || left > right {
		t.Fatalf("stacked table needs the left heading, then the right one:\n%s", table)
	}
	if got := buttonRows(m.table()); got != 10 {
		t.Errorf("stacked table has %d rows, want 10:\n%s", got, table)
	}
}

// The header shows the mode between the title and the profile name.
func TestViewHeaderShowsModeBetweenTitleAndProfile(t *testing.T) {
	cfg := testCfg()
	cfg.Profile = "MyProfile"
	cfg.Mode = config.ModeDouble
	h := on(cfg, PodConnected, PodConnected).header()
	title, mode, profile := strings.Index(h, "zwiftboard"), strings.Index(h, "DOUBLE"), strings.Index(h, "MyProfile")
	if title < 0 || mode < 0 || profile < 0 || title >= mode || mode >= profile {
		t.Fatalf("header want zwiftboard, Mode: DOUBLE, MyProfile in order: %q", h)
	}
}

// The footer advertises "ctrl+m" only when toggling is wired.
func TestViewFooterToggleModeHint(t *testing.T) {
	cfg := testCfg()
	cfg.ToggleMode = func() {}
	has(t, on(cfg, PodConnected, PodConnected).View(), "ctrl+m mode")
	lacks(t, on(testCfg(), PodConnected, PodConnected).View(), "ctrl+m mode")
}

// The full footer (every shortcut wired) wraps inside the card width, so no
// line runs past the panels and no shortcut is dropped.
func TestViewFooterFitsCardWidth(t *testing.T) {
	cfg := doubleCfg()
	cfg.OpenConfig = func() error { return nil }
	cfg.Reload = func() {}
	cfg.LaunchProgram = func() error { return nil }
	cfg.StartProgram = "x.exe"
	cfg.SwitchProfile = func(int) {}
	cfg.Profiles = []string{"a", "b"}
	cfg.Auth.Login = func(string, string) error { return nil }
	cfg.ToggleMode = func() {}
	m := on(cfg, PodConnected, PodConnected)
	m.width = 300
	footer := m.footer()
	if strings.Contains(footer, "\n") {
		t.Fatalf("full footer should stay on one line at %d cells:\n%s", m.innerWidth(), footer)
	}
	for _, line := range strings.Split(footer, "\n") {
		if w := ansi.StringWidth(line); w > m.innerWidth() {
			t.Fatalf("footer line is %d cells, card inner width is %d", w, m.innerWidth())
		}
	}
	has(t, m.View(), "ctrl+l logs", "ctrl+e edit", "ctrl+r reload", "ctrl+s start",
		"ctrl+←/→ switch profile", "ctrl+z sign in", "ctrl+c quit")
}
