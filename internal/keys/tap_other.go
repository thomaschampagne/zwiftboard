//go:build !windows

package keys

// Tap is a no-op off Windows; BLE support here is Windows-only anyway.
func Tap(b Binding) {}
