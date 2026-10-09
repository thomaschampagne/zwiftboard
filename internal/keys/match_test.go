package keys

import "testing"

func TestWindowMatches(t *testing.T) {
	cases := []struct {
		name, proc, title, target string
		want                      bool
	}{
		{"exe prefix", "MyWhooshHD", "MyWhoosh", "MyWhoosh", true},
		{"exe prefix case-insensitive", "MYWHOOSHHD", "x", "mywhoosh", true},
		{"exe exact", "ZwiftApp", "Zwift", "ZwiftApp", true},
		{"title contains", "Notepad", "Untitled - Notepad", "Notepad", true},
		{"target longer than exe", "MyWhoosh", "x", "MyWhooshHD", false},
		{"no match", "ZwiftApp", "Zwift", "MyWhoosh", false},
		{"empty", "", "", "MyWhoosh", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := windowMatches(c.proc, c.title, c.target); got != c.want {
				t.Errorf("windowMatches(proc=%q, title=%q, target=%q) = %v, want %v",
					c.proc, c.title, c.target, got, c.want)
			}
		})
	}
}
