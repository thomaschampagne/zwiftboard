package keys

import "testing"

func TestResolve(t *testing.T) {
	cases := map[string]uint16{
		"a": 0x41, "z": 0x5A, "0": 0x30, "9": 0x39,
		"up": 0x26, "PAGEUP": 0x21, "pagedown": 0x22, "f1": 0x70, "f12": 0x7B,
		"-": 0xBD, "esc": 0x1B,
	}
	for in, want := range cases {
		got, err := Resolve(in)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", in, err)
		}
		if got.VK != want {
			t.Errorf("Resolve(%q) = %#x, want %#x", in, got.VK, want)
		}
	}
	for _, bad := range []string{"", "nope", "f0", "f13", "ab", "f1x"} {
		if _, err := Resolve(bad); err == nil {
			t.Errorf("Resolve(%q): want error", bad)
		}
	}
}
