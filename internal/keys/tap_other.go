//go:build !windows && !darwin

package keys

// Tap/Down/Up/ReleaseVK are no-ops on unsupported platforms. Down/Up report
// true so the hold tracker remains usable for protocol and UI tests.
func Tap(b Binding) {}

func Down(b Binding) bool { return true }

func Up(b Binding) bool { return true }

func ReleaseVK(vk uint16) {}

// TargetWindowPresent is always true on unsupported platforms.
func TargetWindowPresent() bool { return true }
