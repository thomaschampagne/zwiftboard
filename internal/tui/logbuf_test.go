package tui

import (
	"fmt"
	"testing"
)

func TestLogWindowOffsets(t *testing.T) {
	b := NewLogBuffer(50)
	for i := 0; i < 20; i++ {
		b.Write([]byte(fmt.Sprintf("line%02d\n", i)))
	}
	last := b.Window(10, 0)
	if len(last) != 10 || last[0] != "line10" || last[9] != "line19" {
		t.Fatalf("Window(10,0) = %q", last)
	}
	up := b.Window(10, 5)
	if len(up) != 10 || up[0] != "line05" || up[9] != "line14" {
		t.Fatalf("Window(10,5) = %q", up)
	}
	if got := b.Window(10, 100); len(got) != 10 || got[0] != "line00" {
		t.Fatalf("Window(10,100) = %q", got)
	}
	if got := b.Window(10, 15); len(got) != 5 || got[0] != "line00" {
		t.Fatalf("Window(10,15) = %q", got)
	}
}
