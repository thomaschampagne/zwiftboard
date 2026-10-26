//go:build darwin

package launch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func defaultEnv() Env {
	return Env{Exists: fileExists, Running: processRunning, Start: startDetached}
}

func resolveExe(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	if !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name, true
	}
	candidates := []string{
		filepath.Join("/Applications", name+".app"),
		filepath.Join(os.Getenv("HOME"), "Applications", name+".app"),
	}
	for _, p := range candidates {
		if fileExists(p) {
			return p, true
		}
	}
	return "", false
}

func platformCommand(spec string) *exec.Cmd {
	if strings.HasSuffix(strings.ToLower(spec), ".app") {
		return exec.Command("open", spec)
	}
	if !strings.Contains(spec, string(filepath.Separator)) {
		return exec.Command("open", "-a", spec)
	}
	return exec.Command(spec)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func processRunning(name string) bool {
	name = strings.TrimSuffix(filepath.Base(name), ".app")
	return exec.Command("pgrep", "-x", name).Run() == nil
}

func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
