//go:build !windows && !darwin

package launch

import "os/exec"

// defaultEnv on unsupported platforms is inert; tests inject their own Env.
func defaultEnv() Env {
	return Env{
		Exists:  func(string) bool { return true },
		Running: func(string) bool { return false },
		Start:   func(*exec.Cmd) error { return nil },
	}
}

// resolveExe on unsupported platforms only consults PATH.
func resolveExe(name string) (string, bool) {
	p, err := exec.LookPath(name)
	return p, err == nil
}

func platformCommand(spec string) *exec.Cmd {
	return exec.Command(spec)
}
