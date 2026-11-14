//go:build windows

package keys

import (
	"testing"
	"unsafe"
)

// findTargetWindow used to register a fresh Windows callback on every call via
// windows.NewCallback. Go's runtime keeps every registration in a fixed
// 2000-entry table that is never freed, so once a focus target is configured
// (the shipped profiles set one) the process aborted with "too many callback
// functions" after ~2000 lookups — reached in ~33 min by the TUI's
// once-a-second presence poll, sooner with key presses. The callback is now
// registered once at package init, so lookups are free.
//
// Against the old code this aborts the test binary (throw, not panic); it must
// therefore be run on Windows, where the bug is observable.
func TestFindTargetWindowDoesNotExhaustCallbacks(t *testing.T) {
	const lookups = 2500 // past the 2000-slot runtime callback table
	for i := 0; i < lookups; i++ {
		hwnd, _, ok := findTargetWindow("definitely-not-a-running-program-xyzzy")
		if ok || hwnd != 0 {
			t.Fatalf("lookup %d matched by accident: hwnd=%d ok=%v", i, hwnd, ok)
		}
	}
}

// Windows INPUT is 40 bytes on 64-bit and 28 bytes on 32-bit Windows.
// SendInput rejects an incorrect cbSize, so preserve the union's ABI layout.
func TestSendInputStructureLayout(t *testing.T) {
	want := uintptr(28)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		want = 40
	}
	if got := unsafe.Sizeof(input{}); got != want {
		t.Fatalf("INPUT size = %d, want %d", got, want)
	}
	wantKeyboard := uintptr(16)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		wantKeyboard = 24
	}
	if got := unsafe.Sizeof(keyboardInput{}); got != wantKeyboard {
		t.Fatalf("KEYBDINPUT size = %d, want %d", got, wantKeyboard)
	}
}

func TestKeyboardInputPressAndRelease(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags uint32
	}{
		{name: "press", flags: 0},
		{name: "release", flags: keyEventKeyUp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := newKeyboardInput(0x25, tc.flags) // VK_LEFT
			ki := *(*keyboardInput)(unsafe.Pointer(&in.data))
			if in.typ != inputKeyboard || ki.vk != 0x25 || ki.flags != tc.flags || ki.scan != 0 {
				t.Fatalf("unexpected INPUT: type=%d, keyboard=%+v", in.typ, ki)
			}
		})
	}
}

func TestScanCodeEncoding(t *testing.T) {
	cases := []struct {
		name       string
		vk         uint16
		mappedScan uint32
		flags      uint32
		wantVK     uint16
		wantScan   uint16
		wantFlags  uint32
	}{
		{"W down", 0x57, 0x11, 0, 0, 0x11, keyEventScanCode},
		{"S up", 0x53, 0x1F, keyEventKeyUp, 0, 0x1F, keyEventScanCode | keyEventKeyUp},
		{"left arrow", 0x25, 0xE04B, 0, 0, 0x4B, keyEventScanCode | keyEventExtendedKey},
		{"right arrow up", 0x27, 0xE04D, keyEventKeyUp, 0, 0x4D, keyEventScanCode | keyEventExtendedKey | keyEventKeyUp},
		{"unknown mapping fallback", 0x57, 0, 0, 0x57, 0, 0},
		{"E1 prefix fallback", 0x13, 0xE145, 0, 0x13, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := newScanKeyboardInput(tc.vk, tc.mappedScan, tc.flags)
			ki := *(*keyboardInput)(unsafe.Pointer(&in.data))
			if in.typ != inputKeyboard || ki.vk != tc.wantVK || ki.scan != tc.wantScan || ki.flags != tc.wantFlags {
				t.Fatalf("got type=%d KEYBDINPUT=%+v, want vk=%#x scan=%#x flags=%#x", in.typ, ki, tc.wantVK, tc.wantScan, tc.wantFlags)
			}
		})
	}
}
