//go:build windows

package keys

import (
	"log/slog"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procSendInput           = user32.NewProc("SendInput")
	procMapVirtualKey       = user32.NewProc("MapVirtualKeyW")
	procGetWindowText       = user32.NewProc("GetWindowTextW")
	procIsIconic            = user32.NewProc("IsIconic")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
)

const (
	inputKeyboard       = 1      // INPUT_KEYBOARD
	keyEventExtendedKey = 0x0001 // KEYEVENTF_EXTENDEDKEY
	keyEventKeyUp       = 0x0002 // KEYEVENTF_KEYUP
	keyEventScanCode    = 0x0008 // KEYEVENTF_SCANCODE
	mapVKtoVSCEx        = 4      // MAPVK_VK_TO_VSC_EX
	vkMenu              = 0x12   // VK_MENU, used to release the foreground lock
	swRestore           = 9      // SW_RESTORE

	// keyRepeatDelay/keyRepeatInterval mimic a real keyboard: Windows' shortest
	// repeat delay and its default repeat rate. A synthetic SendInput keydown is
	// a single event with NO OS auto-repeat — games that poll the key state see
	// the hold, but text apps (Notepad, chat) need the repeated WM_KEYDOWN a
	// physical key produces, so Down re-sends the press on this schedule.
	keyRepeatDelay    = 250 * time.Millisecond
	keyRepeatInterval = 33 * time.Millisecond
)

// Windows INPUT contains a union of MOUSEINPUT, KEYBDINPUT, and HARDWAREINPUT.
// Model the union using its largest member (MOUSEINPUT) so the structure has
// the correct size and alignment on both 32-bit and 64-bit Windows. Write a
// KEYBDINPUT into the beginning of that union for keyboard events.
type mouseInput struct {
	dx        int32
	dy        int32
	mouseData uint32
	flags     uint32
	time      uint32
	extraInfo uintptr
}

type keyboardInput struct {
	vk        uint16
	scan      uint16
	flags     uint32
	time      uint32
	extraInfo uintptr
}

type input struct {
	typ  uint32
	data mouseInput // size and alignment of the Windows INPUT union
}

func newKeyboardInput(vk uint16, flags uint32) input {
	in := input{typ: inputKeyboard}
	*(*keyboardInput)(unsafe.Pointer(&in.data)) = keyboardInput{vk: vk, flags: flags}
	return in
}

// newScanKeyboardInput constructs a physical-scan-code event. Chromium uses
// the scan code to populate KeyboardEvent.code. Virtual-key-only SendInput
// events can have a zero scan code, which some web apps cannot match reliably.
// MapVirtualKeyW(MAPVK_VK_TO_VSC_EX) returns 0xE0 in the high byte for keys
// like the arrow keys; Windows requires KEYEVENTF_EXTENDEDKEY for those.
//
// Fallback to virtual keys when a scan-code mapping is unavailable or uses
// the uncommon E1 prefix (which cannot be represented by this INPUT layout).
func newScanKeyboardInput(vk uint16, mappedScan uint32, flags uint32) input {
	prefix := (mappedScan >> 8) & 0xff
	scan := uint16(mappedScan & 0xff)
	if scan == 0 || prefix == 0xE1 {
		return newKeyboardInput(vk, flags)
	}
	if prefix == 0xE0 {
		flags |= keyEventExtendedKey
	}
	in := input{typ: inputKeyboard}
	*(*keyboardInput)(unsafe.Pointer(&in.data)) = keyboardInput{
		vk: 0, scan: scan, flags: flags | keyEventScanCode,
	}
	return in
}

// sendKeyboardInput sends a physical scan-code press or release where possible.
// SendInput returning 1 means Windows accepted an event for injection; it does
// NOT prove that the foreground app handled it. UIPI can block elevated apps.
func sendKeyboardInput(vk uint16, flags uint32) bool {
	mapped, _, _ := procMapVirtualKey.Call(uintptr(vk), mapVKtoVSCEx)
	in := newScanKeyboardInput(vk, uint32(mapped), flags)
	sent, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
	if sent != 1 {
		slog.Warn("SendInput failed", "vk", vk, "mapped_scan", mapped, "flags", flags, "error", err)
		return false
	}
	return true
}

// repeatStop cancels a held key's repeat goroutine (closed to stop).
var (
	repeatMu   sync.Mutex
	repeatStop = map[uint16]chan struct{}{}
)

// focusTarget brings the configured window to the foreground so the key lands
// there; false when no matching window exists (caller drops the press).
func focusTarget() bool {
	target := CurrentWindowTarget()
	if target == "" {
		return true
	}
	hwnd, focused, ok := findTargetWindow(target)
	if !ok {
		slog.Warn("focus program not found — tap dropped", "program", target)
		return false
	}
	if !focused {
		focusWindow(hwnd)
	}
	return true
}

// Tap presses and releases a key using scan-code SendInput.
func Tap(b Binding) {
	Down(b)
	Up(b)
}

// Down presses a scan-code-backed key and leaves it held (a button is being held).
// It also starts keyboard auto-repeat: without it a synthetic press is a
// single event and repeated presses in a text app (Notepad, chat) never
// happen while the button is held. It reports whether the key actually went
// out — false when the configured focus window is missing — so the caller
// (ble.Holds) does not record a hold that never reached a window.
func Down(b Binding) bool {
	if !focusTarget() {
		return false
	}
	if !sendKeyboardInput(b.VK, 0) {
		return false
	}
	startRepeat(b.VK)
	return true
}

// Up releases a key previously pressed with Down (button released).
// It reports whether SendInput accepted the release; the bool keeps Down/Up
// symmetric for the hold tracker.
func Up(b Binding) bool {
	stopRepeat(b.VK)
	return sendKeyboardInput(b.VK, keyEventKeyUp)
}

// ReleaseVK force-releases a virtual key. ble.ReleaseAll uses it to free
// every key still held when a session dies or the process exits, so a dropped
// controller can never leave a key stuck down.
func ReleaseVK(vk uint16) {
	stopRepeat(vk)
	sendKeyboardInput(vk, keyEventKeyUp)
}

// startRepeat idempotently re-sends vk as a press after keyRepeatDelay and
// every keyRepeatInterval until stopped — the WM_KEYDOWN repeat stream a
// physical keyboard produces. Safe to call for an already-repeating key.
func startRepeat(vk uint16) {
	repeatMu.Lock()
	if _, ok := repeatStop[vk]; ok {
		repeatMu.Unlock()
		return
	}
	stop := make(chan struct{})
	repeatStop[vk] = stop
	repeatMu.Unlock()
	go func() {
		select {
		case <-time.After(keyRepeatDelay):
		case <-stop:
			return
		}
		t := time.NewTicker(keyRepeatInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if !sendKeyboardInput(vk, 0) {
					return // avoid repeating a blocked injection every 33ms
				}
			}
		}
	}()
}

// stopRepeat cancels a started repeat (no-op when not repeating).
func stopRepeat(vk uint16) {
	repeatMu.Lock()
	stop, ok := repeatStop[vk]
	delete(repeatStop, vk)
	repeatMu.Unlock()
	if ok {
		close(stop)
	}
}

// Window-enumeration state for findTargetWindow. The callback is registered
// ONCE (enumCallback): Go's Windows runtime keeps every NewCallback
// registration in a fixed 2000-entry table and never frees it, so the previous
// per-call registration exhausted the table — and leaked the capturing closure
// — after ~2000 lookups (every keypress, plus once a second from the TUI
// presence poll, aborted the process with "too many callback functions"). The
// search target/result travel through package vars because the EnumWindows
// callback cannot carry a Go closure; enumMu serializes concurrent lookups.
var (
	enumMu   sync.Mutex
	enumProg string
	enumHWND uintptr
)

var enumCallback = windows.NewCallback(func(h, _ uintptr) uintptr {
	if r, _, _ := procIsWindowVisible.Call(h); r == 0 {
		return 1 // keep enumerating; we only want visible windows
	}
	var pid uint32
	_, _ = windows.GetWindowThreadProcessId(windows.HWND(h), &pid)
	if windowMatches(windowProcessName(pid), windowTitle(h), enumProg) {
		enumHWND = h
		return 0
	}
	return 1
})

// findTargetWindow returns the handle of a visible top-level window whose
// process .exe name starts with program or whose title contains it
// (case-insensitive), plus whether it is already focused. Both match kinds are
// accepted because game titles vary while the .exe name usually doesn't.
func findTargetWindow(program string) (hwnd uintptr, focused bool, ok bool) {
	enumMu.Lock()
	enumProg, enumHWND = program, 0
	_ = windows.EnumWindows(enumCallback, unsafe.Pointer(nil))
	hwnd = enumHWND
	enumProg = ""
	enumMu.Unlock()
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
	if !sendKeyboardInput(vkMenu, 0) {
		return
	}
	sendKeyboardInput(vkMenu, keyEventKeyUp)
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
	target := CurrentWindowTarget()
	if target == "" {
		return true
	}
	_, _, ok := findTargetWindow(target)
	return ok
}
