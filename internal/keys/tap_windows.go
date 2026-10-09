//go:build windows

package keys

import (
	"log/slog"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procKeybdEvent          = user32.NewProc("keybd_event")
	procGetWindowText       = user32.NewProc("GetWindowTextW")
	procIsIconic            = user32.NewProc("IsIconic")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
)

const (
	keyEventKeyUp = 0x0002 // KEYEVENTF_KEYUP
	vkMenu        = 0x12   // VK_MENU, used to release the foreground lock
	swRestore     = 9      // SW_RESTORE
)

// windowTarget is the program (window title or .exe name) brought to the
// foreground before every tap; set by SetWindowTarget from config
// (focusProgramNamePrefixOnClick). Empty means Taps go to whichever window has
// focus, as before.
var windowTarget string

// SetWindowTarget configures the program whose window is focused before each
// Tap. Empty disables auto-focus.
func SetWindowTarget(program string) {
	windowTarget = strings.TrimSpace(program)
}

// Tap presses and releases a virtual key via keybd_event. With a focus target
// configured, the matching window is brought to the foreground first so the
// key lands there; when nothing matches, the tap is dropped and a warning is
// logged instead of typing into an unrelated focused app.
func Tap(b Binding) {
	if windowTarget != "" {
		hwnd, focused, ok := findTargetWindow(windowTarget)
		if !ok {
			slog.Warn("focus program not found — tap dropped", "program", windowTarget)
			return
		}
		if !focused {
			focusWindow(hwnd)
		}
	}
	procKeybdEvent.Call(uintptr(b.VK), 0, 0, 0)
	procKeybdEvent.Call(uintptr(b.VK), 0, keyEventKeyUp, 0)
}

// findTargetWindow returns the handle of a visible top-level window whose
// process .exe name starts with program or whose title contains it
// (case-insensitive), plus whether it is already focused. Both match kinds are
// accepted because game titles vary while the .exe name usually doesn't.
func findTargetWindow(program string) (hwnd uintptr, focused bool, ok bool) {
	cb := windows.NewCallback(func(h uintptr, _ uintptr) uintptr {
		if r, _, _ := procIsWindowVisible.Call(h); r == 0 {
			return 1 // keep enumerating; we only want visible windows
		}
		var pid uint32
		_, _ = windows.GetWindowThreadProcessId(windows.HWND(h), &pid)
		if windowMatches(windowProcessName(pid), windowTitle(h), program) {
			hwnd = h
			return 0
		}
		return 1
	})
	_ = windows.EnumWindows(cb, unsafe.Pointer(nil))
	if hwnd == 0 {
		return 0, false, false
	}
	return hwnd, windows.GetForegroundWindow() == windows.HWND(hwnd), true
}

// focusWindow brings hwnd to the foreground. The Windows foreground lock gives
// that right to whichever process last received input — we generate every key
// event, so SetForegroundWindow usually succeeds; when it is still refused,
// the classic menu-key nudge releases the lock.
func focusWindow(hwnd uintptr) {
	if r, _, _ := procIsIconic.Call(hwnd); r != 0 { // minimized: restore first
		procShowWindow.Call(hwnd, swRestore)
	}
	if r, _, _ := procSetForegroundWindow.Call(hwnd); r != 0 {
		return
	}
	procKeybdEvent.Call(vkMenu, 0, 0, 0)
	procKeybdEvent.Call(vkMenu, 0, keyEventKeyUp, 0)
	procSetForegroundWindow.Call(hwnd)
}

// windowTitle returns the window text ("" for captionless windows).
func windowTitle(h uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := procGetWindowText.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// windowProcessName returns the base .exe name (no path, no ".exe") of the
// process owning the given PID, or "" when it cannot be read.
func windowProcessName(pid uint32) string {
	if pid == 0 {
		return ""
	}
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(proc)
	size := uint32(512)
	buf := make([]uint16, size)
	if err := windows.QueryFullProcessImageName(proc, 0, &buf[0], &size); err != nil {
		return ""
	}
	name := windows.UTF16ToString(buf[:size])
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSuffix(name, ".exe")
}

// TargetWindowPresent reports whether the configured focus window exists
// (true when no target is set). The TUI uses it to warn that clicks are being
// dropped; it is the same lookup Tap performs, read-only.
func TargetWindowPresent() bool {
	if windowTarget == "" {
		return true
	}
	_, _, ok := findTargetWindow(windowTarget)
	return ok
}
