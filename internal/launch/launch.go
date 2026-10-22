package launch

import (
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Kind is how a startProgram spec should be launched.
type Kind int

const (
	KindExe Kind = iota
	KindCmd
	KindPowerShell
	KindStore
)

// aumidRe matches a Microsoft Store AUMID: PackageFamilyName!AppId.
var aumidRe = regexp.MustCompile(`^[A-Za-z0-9.]+_[A-Za-z0-9]+!`)

// classify decides how a startProgram spec should be launched.
func classify(spec string) Kind {
	lower := strings.ToLower(spec)
	switch {
	case strings.HasSuffix(lower, ".ps1"):
		return KindPowerShell
	case strings.HasSuffix(lower, ".bat"), strings.HasSuffix(lower, ".cmd"):
		return KindCmd
	case aumidRe.MatchString(spec):
		return KindStore
	default:
		return KindExe
	}
}

// pathLike reports whether spec names a file path rather than a bare executable
// name resolved off PATH.
func pathLike(spec string) bool {
	return strings.ContainsAny(spec, `\/:`)
}

// exeName is the process name used to detect an already-running program. Only
// exe specs have one; scripts and Store apps return "".
func exeName(spec string) string {
	if classify(spec) != KindExe {
		return ""
	}
	base := filepath.Base(spec)
	if strings.HasSuffix(strings.ToLower(base), ".exe") {
		base = base[:len(base)-len(".exe")]
	}
	return base
}

// command builds the exec.Cmd for a non-exe spec (scripts, Store AUMIDs). Exe
// specs are handled by Launch, which resolves the target path first.
func command(spec string, k Kind) *exec.Cmd {
	switch k {
	case KindCmd:
		return exec.Command("cmd.exe", "/c", spec)
	case KindPowerShell:
		return exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", spec)
	case KindStore:
		return exec.Command("explorer.exe", `shell:AppsFolder\`+spec)
	default:
		return platformCommand(spec)
	}
}

// Env is the injected OS surface used by Launch. The platform files supply the
// real implementation; tests supply fakes so no OS call happens on Linux.
type Env struct {
	Exists  func(string) bool
	Running func(string) bool
	Start   func(*exec.Cmd) error
}

// Launch applies the launch policy: empty is a no-op; an already-running exe is
// skipped; a bare name is resolved via PATH then Windows App Paths (warn if
// unresolved); an existing path that is missing is skipped with a warning;
// anything else is started.
func (e Env) Launch(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}

	k := classify(spec)
	name := exeName(spec)

	if k == KindExe && name != "" && e.Running(name) {
		slog.Info("startProgram already running", "program", spec)
		return nil
	}

	target := spec
	if k == KindExe {
		switch {
		case pathLike(spec):
			if !e.Exists(spec) {
				slog.Warn("startProgram not found", "program", spec)
				return fmt.Errorf("startProgram not found: %s", spec)
			}
		default:
			// A bare exe name: CreateProcess (exec.Command) only consults PATH,
			// so resolve App Paths ourselves or the Run-dialog name silently
			// fails. Warn rather than attempt a doomed start.
			resolved, ok := resolveExe(spec)
			if !ok {
				slog.Warn("startProgram not found", "program", spec)
				return fmt.Errorf("startProgram not found: %s", spec)
			}
			target = resolved
		}
	}

	cmd := command(target, k)
	if err := e.Start(cmd); err != nil {
		return fmt.Errorf("start %s: %w", spec, err)
	}
	slog.Info("startProgram launched", "program", spec)
	return nil
}

// Launch starts the program described by spec using the real OS seam.
func Launch(spec string) error { return defaultEnv().Launch(spec) }
