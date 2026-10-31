//go:build !darwin

package keys

// Initialize and Shutdown are macOS injection lifecycle hooks. Existing
// platforms retain their existing session-level release policy.
func Initialize() {}
func Shutdown()   {}

// InputWarning is empty where no platform injection preflight is needed.
func InputWarning() string { return "" }
