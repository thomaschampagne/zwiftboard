package tui

import "testing"

// recoveryModel is a model whose first screen is the config-recovery dialog.
func recoveryModel(start func(string) (Config, bool)) Model {
	cfg := testCfg()
	cfg.Profile = "broken"
	cfg.RecoveryPath = "/home/rider/.config/zwiftboard/config.yml"
	cfg.RecoveryErr = "parse config.yml: bad indentation"
	cfg.RecoveryStart = start
	return New(cfg)
}

// The dialog is the first screen and names the file, the reason and both
// choices: the status screen is not drawn until it is answered.
func TestRecoveryDialogIsTheFirstScreen(t *testing.T) {
	m := recoveryModel(func(string) (Config, bool) {
		t.Fatal("the restore must not run before a yes")
		return Config{}, false
	})
	v := m.View()
	has(t, v, "/home/rider/.config/zwiftboard/config.yml", "bad indentation",
		"back up and restore the built-in default", "keep the broken file")
	lacks(t, v, "Click V2 · key bindings")
}

// Every status shortcut is inert while the dialog owns the keys, and only a
// deliberate y/n answers it.
func TestRecoveryDialogOwnsTheKeys(t *testing.T) {
	m := recoveryModel(func(string) (Config, bool) { return Config{}, false })
	for _, k := range []string{"q", "l", "z", "r", "e", "s"} {
		next, cmd := m.Update(key(k))
		if cmd != nil {
			t.Fatalf("%q returned a cmd during the dialog", k)
		}
		if !next.(Model).recovery.Active {
			t.Fatalf("%q closed the dialog", k)
		}
	}
}

// A yes runs the hook with the dialog's path, swaps in the rebuilt config and
// shows the status screen with the restored profile.
func TestRecoveryYesRestoresTheDefaultProfile(t *testing.T) {
	var gotPath string
	rebuilt := testCfg()
	rebuilt.Profile = "notepad"
	rebuilt.Bindings = map[string]string{"A": "a"}
	m := recoveryModel(func(path string) (Config, bool) {
		gotPath = path
		return rebuilt, true
	})

	next, _ := m.Update(key("y"))
	mm := next.(Model)
	if gotPath != m.recovery.Path {
		t.Fatalf("hook path = %q, want %q", gotPath, m.recovery.Path)
	}
	if mm.recovery.Active {
		t.Fatal("a yes must close the dialog")
	}
	if mm.cfg.Profile != "notepad" {
		t.Fatalf("profile = %q, want the rebuilt notepad", mm.cfg.Profile)
	}
	if _, ok := mm.cfg.Bindings["A"]; !ok {
		t.Fatal("the rebuilt bindings were not swapped in")
	}
	v := mm.View()
	has(t, v, "notepad")
	lacks(t, v, "Config file failed to load")
}

// A no keeps the broken config and never runs the restore.
func TestRecoveryNoKeepsTheBrokenFile(t *testing.T) {
	called := false
	m := recoveryModel(func(string) (Config, bool) {
		called = true
		return Config{}, false
	})

	next, _ := m.Update(key("n"))
	mm := next.(Model)
	if called {
		t.Fatal("n must not run the restore")
	}
	if mm.recovery.Active {
		t.Fatal("n must close the dialog")
	}
	if mm.cfg.Profile != "broken" {
		t.Fatalf("profile = %q, want the broken config kept", mm.cfg.Profile)
	}
	has(t, mm.View(), "kept the broken config")
}

// A failed restore keeps the dialog open and the broken config in place, with
// the reason on the dialog.
func TestRecoveryFailureStaysOnTheDialog(t *testing.T) {
	m := recoveryModel(func(string) (Config, bool) { return Config{}, false })

	next, _ := m.Update(key("y"))
	mm := next.(Model)
	if !mm.recovery.Active {
		t.Fatal("a failed restore must keep the dialog open")
	}
	if mm.cfg.Profile != "broken" {
		t.Fatal("a failed restore must keep the broken config")
	}
	has(t, mm.View(), "could not restore")
}

// The dialog needs both the path and the hook: a missing one leaves the normal
// screen in charge.
func TestRecoveryNeedsPathAndHook(t *testing.T) {
	cfg := testCfg()
	cfg.RecoveryStart = func(string) (Config, bool) { return Config{}, false }
	if New(cfg).recovery.Active {
		t.Fatal("no path must not open the dialog")
	}
	cfg = testCfg()
	cfg.RecoveryPath = "config.yml"
	if New(cfg).recovery.Active {
		t.Fatal("no hook must not open the dialog")
	}
}
