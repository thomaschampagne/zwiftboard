//go:build darwin && cgo

package keys

/*
#cgo LDFLAGS: -framework ApplicationServices
#include <ApplicationServices/ApplicationServices.h>
static bool postKeyboard(CGKeyCode code, bool down, bool repeat, CGEventFlags flags) {
 // A private source keeps our posted flags out of the hardware state table
 // sampled below. A fresh source has no stale modifier state to inherit.
 CGEventSourceRef source = CGEventSourceCreate(kCGEventSourceStatePrivate);
 if (!source) return false;
 CGEventRef event = CGEventCreateKeyboardEvent(source, code, down);
 if (!event) { CFRelease(source); return false; }
 CGEventSetFlags(event, flags);
 CGEventSetIntegerValueField(event, kCGKeyboardEventAutorepeat, repeat);
 CGEventPost(kCGHIDEventTap, event);
 CFRelease(event);
 CFRelease(source);
 // CGEventPost returns void: this confirms allocation/posting, not delivery.
 return true;
}
*/
import "C"

import (
	"log/slog"
	"time"
)

var darwinKeyboard = newQuartzKeyboard(
	func(e quartzEvent) bool {
		return bool(C.postKeyboard(C.CGKeyCode(e.code), C.bool(e.down), C.bool(e.repeat), C.CGEventFlags(e.flags)))
	},
	func() bool { return C.AXIsProcessTrusted() != 0 }, CurrentWindowTarget,
	func(d time.Duration, f func()) func() { timer := time.AfterFunc(d, f); return func() { timer.Stop() } },
	func() uint64 { return uint64(C.CGEventSourceFlagsState(C.kCGEventSourceStateHIDSystemState)) },
)

// Initialize checks Accessibility without prompting. Controller diagnostics
// remain available when macOS denies keyboard event injection.
func Initialize() {
	if !darwinKeyboard.trusted() {
		slog.Warn("keyboard injection disabled: grant Accessibility access in System Settings > Privacy & Security > Accessibility, then restart zwiftboard")
	}
	if !TargetWindowPresent() {
		slog.Warn("macOS focus targets are unsupported; keyboard injection blocked", "program", CurrentWindowTarget())
	}
}

// Shutdown cancels every repeat and releases held keys before refusing new downs.
func Shutdown() { darwinKeyboard.shutdown() }
func Tap(b Binding) {
	if Down(b) {
		Up(b)
	}
}
func Down(b Binding) bool       { return darwinKeyboard.down(b.VK) }
func Up(b Binding) bool         { return darwinKeyboard.up(b.VK) }
func ReleaseVK(vk uint16)       { darwinKeyboard.up(vk) }
func TargetWindowPresent() bool { return CurrentWindowTarget() == "" }

// InputWarning returns a live, non-prompting injection diagnostic for the TUI.
func InputWarning() string { return darwinKeyboard.inputWarning() }
