//go:build !windows

package keys

// Tap/Down/Up/ReleaseVK are no-ops off Windows; BLE support here is
// Windows-only anyway. Down/Up report true so the hold tracker sees the key
// "sent" (there is no window to drop into on the dev platform).
func Tap(b Binding) {}

func Down(b Binding) bool { return true }

func Up(b Binding) bool { return true }

func ReleaseVK(vk uint16) {}

// SetWindowTarget is a no-op off Windows (window focusing is Windows-only).
func SetWindowTarget(program string) {}

// TargetWindowPresent is always true off Windows (no focus feature to warn about).
func TargetWindowPresent() bool { return true }
