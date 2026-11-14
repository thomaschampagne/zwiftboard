package launch

import (
	"os/exec"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := map[string]Kind{
		`C:\x\game.exe`:             KindExe,
		`MyWhooshHD.exe`:            KindExe,
		`deploy.bat`:                KindCmd,
		`deploy.cmd`:                KindCmd,
		`run.ps1`:                   KindPowerShell,
		`MyWhoosh.MyWhoosh_abc!App`: KindStore,
	}
	for in, want := range cases {
		if got := classify(in); got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestEnvLaunchBranches(t *testing.T) {
	var started bool
	ok := Env{Exists: func(string) bool { return true }, Running: func(string) bool { return false },
		Start: func(*exec.Cmd) error { started = true; return nil }}

	// empty: no start
	if err := ok.Launch(""); err != nil || started {
		t.Fatal("empty should be no-op")
	}

	// running: skip, no start
	started = false
	run := Env{Exists: func(string) bool { return true }, Running: func(string) bool { return true },
		Start: func(*exec.Cmd) error { started = true; return nil }}
	if err := run.Launch(`C:\x\game.exe`); err != nil || started {
		t.Fatal("running should skip")
	}

	// missing existing path: error, no start
	started = false
	miss := Env{Exists: func(string) bool { return false }, Running: func(string) bool { return false },
		Start: func(*exec.Cmd) error { started = true; return nil }}
	if err := miss.Launch(`C:\x\game.exe`); err == nil || started {
		t.Fatal("missing should warn/skip")
	}
}

func TestCommandShapes(t *testing.T) {
	if got := command(`a.bat`, KindCmd).Args; got[0] != "cmd.exe" || got[1] != "/c" {
		t.Fatalf("%v", got)
	}
	if got := command(`a.ps1`, KindPowerShell).Args; got[0] != "powershell.exe" {
		t.Fatalf("%v", got)
	}
	if got := command(`Pkg_abc!App`, KindStore).Args; got[0] != "explorer.exe" || got[1] != `shell:AppsFolder\Pkg_abc!App` {
		t.Fatalf("%v", got)
	}
}

func TestLaunchUnresolvedBareExe(t *testing.T) {
	// A bare name missing from PATH (off Windows) must warn and skip, not fall
	// through to a doomed Start.
	var started bool
	e := Env{
		Exists:  func(string) bool { return true },
		Running: func(string) bool { return false },
		Start:   func(*exec.Cmd) error { started = true; return nil },
	}
	if err := e.Launch("zwiftboard_no_such_program_12345.exe"); err == nil || started {
		t.Fatal("unresolved bare exe should warn/skip")
	}
}

func TestStartProgramExpandLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\Sample User\AppData\Local`)
	path := `%localappdata%\BikeTerra\BikeTerra.exe`
	want := `C:\Users\Sample User\AppData\Local\BikeTerra\BikeTerra.exe`
	var checked, started bool
	e := Env{
		Exists: func(got string) bool {
			checked = true
			if got != want {
				t.Errorf("exists path = %q, want %q", got, want)
			}
			return true
		},
		Running: func(string) bool { return false },
		Start: func(cmd *exec.Cmd) error {
			started = true
			if len(cmd.Args) != 1 || cmd.Args[0] != want {
				t.Errorf("command = %q, want %q", cmd.Args, want)
			}
			return nil
		},
	}
	if err := e.Launch(path); err != nil {
		t.Fatal(err)
	}
	if !checked || !started {
		t.Fatalf("expected existence check and launch: checked=%v started=%v", checked, started)
	}
}

func TestExpandEnvUnknownVariableLeftUnchanged(t *testing.T) {
	const spec = `%ZWIFTBOARD_THIS_IS_NOT_AN_ENV_VARIABLE%\app.exe`
	if got := expandEnv(spec); !strings.HasPrefix(got, `%ZWIFTBOARD_THIS_IS_NOT_AN_ENV_VARIABLE%`) {
		t.Fatalf("unknown variable should remain unchanged, got %q", got)
	}
}
