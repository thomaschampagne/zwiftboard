//go:build !windows

package launch

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// defaultEnv on non-Windows is inert: the app ships for Windows, and tests
// inject their own Env instead of reaching the real OS.
func defaultEnv() Env {
	return Env{
		Exists:  func(string) bool { return true },
		Running: func(string) bool { return false },
		Start:   func(*exec.Cmd) error { return nil },
	}
}

// resolveExe off Windows only consults PATH: App Paths is a Windows registry
// mechanism. Kept so launch.go compiles on the dev platform.
func resolveExe(name string) (string, bool) {
	p, err := exec.LookPath(name)
	return p, err == nil
}

// expandEnv mirrors Windows %VAR% expansion for portable tests. On Windows the
// native ExpandEnvironmentStrings implementation in process_windows.go is used.
var envVariable = regexp.MustCompile(`%([^%]+)%`)

func expandEnv(s string) string {
	return envVariable.ReplaceAllStringFunc(s, func(v string) string {
		name := v[1 : len(v)-1]
		if value, ok := os.LookupEnv(name); ok {
			return value
		}
		if value, ok := os.LookupEnv(strings.ToUpper(name)); ok {
			return value
		}
		return v
	})
}
