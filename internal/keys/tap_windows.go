//go:build windows

package keys

import "golang.org/x/sys/windows"

var (
	user32         = windows.NewLazySystemDLL("user32.dll")
	procKeybdEvent = user32.NewProc("keybd_event")
)

const keyEventKeyUp = 0x0002 // KEYEVENTF_KEYUP

// Tap presses and releases a virtual key via keybd_event.
func Tap(b Binding) {
	procKeybdEvent.Call(uintptr(b.VK), 0, 0, 0)
	procKeybdEvent.Call(uintptr(b.VK), 0, keyEventKeyUp, 0)
}
