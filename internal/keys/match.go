package keys

import "strings"

// windowMatches reports whether a visible top-level window belongs to the
// configured focus target. The process .exe name must start with target
// (case-insensitive prefix) so a short name like "MyWhoosh" matches
// "MyWhooshHD.exe"; the window title only needs to contain target. Both match
// kinds are accepted because game titles vary while the .exe name usually
// doesn't.
func windowMatches(procName, title, target string) bool {
	t := strings.ToLower(target)
	if strings.HasPrefix(strings.ToLower(procName), t) {
		return true
	}
	return strings.Contains(strings.ToLower(title), t)
}
