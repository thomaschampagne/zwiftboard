//go:build darwin

package launch

import (
	"strings"
	"testing"
)

func TestDarwinLaunchReportsUnsupported(t *testing.T) {
	for _, spec := range []string{"/Applications/TextEdit.app", "notepad.exe", "script.ps1", ""} {
		err := Launch(spec)
		if spec == "" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "not supported on macOS") {
			t.Errorf("Launch(%q) = %v, want unsupported error", spec, err)
		}
	}
}
