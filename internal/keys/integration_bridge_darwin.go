//go:build darwin && cgo && integration

package keys

import "time"

// IntegrationEvent is the Quartz output boundary used by tagged integration
// tests. This file is excluded from normal builds and contains no OS posting.
type IntegrationEvent struct {
	Code         uint16
	Down, Repeat bool
}

// InstallIntegrationKeyboard replaces only OS dependencies; Down, Up,
// ReleaseVK and Shutdown still exercise the real Quartz state machine.
// Tests using this process-global seam must not run in parallel. Restore only
// after their notification delivery has stopped and holds have been released.
func InstallIntegrationKeyboard(post func(IntegrationEvent) bool, after func(time.Duration, func()) func()) func() {
	old := darwinKeyboard
	darwinKeyboard = newQuartzKeyboard(func(e quartzEvent) bool {
		return post(IntegrationEvent{Code: e.code, Down: e.down, Repeat: e.repeat})
	}, func() bool { return true }, CurrentWindowTarget, after, func() uint64 { return 0 })
	return func() { darwinKeyboard.shutdown(); darwinKeyboard = old }
}
