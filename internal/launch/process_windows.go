//go:build windows

package launch

import (
	"os"
	"os/exec"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func defaultEnv() Env {
	return Env{Exists: fileExists, Running: processRunning, Start: startDetached}
}

// resolveExe turns a bare executable name into a launchable path: PATH first
// (what exec.Command would use), then Windows' "App Paths" registry keys. App
// Paths is how the shell resolves a name typed at the Run dialog; CreateProcess
// does NOT consult it, so the lookup has to be explicit or a normally-installed
// app (e.g. MyWhooshHD.exe) would not be found.
func resolveExe(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	return appPath(name)
}

// appPath reads the shell's App Paths key for name, checking the per-user key
// before the machine key (the order the shell uses).
func appPath(name string) (string, bool) {
	const sub = `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, sub+name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		p, _, err := k.GetStringValue("")
		k.Close()
		if err == nil && p != "" {
			// App Paths stores some entries with %VAR% references (e.g.
			// %ProgramFiles%); CreateProcess would see them literally.
			return expandEnv(p), true
		}
	}
	return "", false
}

// expandEnv expands %VAR% references using the Win32 environment, returning the
// input unchanged on any error.
func expandEnv(s string) string {
	src, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return s
	}
	n, err := windows.ExpandEnvironmentStrings(src, nil, 0)
	if err != nil || n == 0 {
		return s
	}
	buf := make([]uint16, n)
	if _, err := windows.ExpandEnvironmentStrings(src, &buf[0], n); err != nil {
		return s
	}
	return windows.UTF16ToString(buf)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// processRunning reports whether any running process image is name (a bare
// .exe name, no extension), case-insensitively.
func processRunning(name string) bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err := windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		exe := strings.TrimSuffix(strings.ToLower(windows.UTF16ToString(entry.ExeFile[:])), ".exe")
		if exe == strings.ToLower(name) {
			return true
		}
	}
	return false
}

// startDetached launches cmd and reaps it in a goroutine so zwiftboard never
// blocks on the child and never leaves a zombie.
func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func platformCommand(spec string) *exec.Cmd {
	return exec.Command(spec)
}
