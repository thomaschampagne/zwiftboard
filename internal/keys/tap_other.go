//go:build !windows && !darwin

package keys

// Tap/Down/Up/ReleaseVK are no-ops on unsupported platforms. Down/Up report true so the hold tracker sees the key
// "sent" (there is no window to drop into on the dev platform).
func Tap(b Binding) {}

func Down(b Binding) bool { return true }

func Up(b Binding) bool { return true }

func ReleaseVK(vk uint16) {}

// TargetWindowPresent is always true off Windows (no focus feature to warn about).
func TargetWindowPresent() bool { return true }
