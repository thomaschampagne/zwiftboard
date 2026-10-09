//go:build !windows

package keys

// Tap/Down/Up/ReleaseVK are no-ops off Windows; BLE support here is
// Windows-only anyway.
func Tap(b Binding) {}

func Down(b Binding) {}

func Up(b Binding) {}

func ReleaseVK(vk uint16) {}

// SetWindowTarget is a no-op off Windows (window focusing is Windows-only).
func SetWindowTarget(program string) {}

// TargetWindowPresent is always true off Windows (no focus feature to warn about).
func TargetWindowPresent() bool { return true }
