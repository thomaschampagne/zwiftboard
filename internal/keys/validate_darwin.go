package keys

import "fmt"

// Insert has no equivalent in the supported macOS physical-key mapping.
// Reject it while loading the profile so an unusable binding is never silent.
func validateNamedKey(vk uint16) error {
	if vk == 0x2d {
		return fmt.Errorf("key %q is unsupported on macOS", "insert")
	}
	return nil
}
