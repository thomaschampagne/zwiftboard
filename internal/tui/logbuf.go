package tui

import (
	"strings"
	"sync"
)

// LogBuffer keeps the last N log lines in memory for the log panel. It is an
// io.Writer so main can tee slog into it next to the log file; writes only
// take a short lock and never block on the UI, so logging stays cheap on the
// BLE goroutines.
type LogBuffer struct {
	mu      sync.Mutex
	max     int
	lines   []string
	partial string // text after the last newline, completed by the next Write
}

// NewLogBuffer returns a buffer holding at most max lines (min 1).
func NewLogBuffer(max int) *LogBuffer {
	if max < 1 {
		max = 1
	}
	return &LogBuffer{max: max}
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	parts := strings.Split(b.partial+string(p), "\n")
	b.partial = parts[len(parts)-1]
	for _, l := range parts[:len(parts)-1] {
		b.lines = append(b.lines, strings.TrimRight(l, "\r"))
	}
	if over := len(b.lines) - b.max; over > 0 {
		b.lines = append([]string(nil), b.lines[over:]...)
	}
	return len(p), nil
}

// Lines returns a copy of the buffered lines, oldest first.
func (b *LogBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.lines...)
}

// Window returns up to n lines ending offset lines before the newest
// line (offset 0 = the newest n lines). Used by the log panel scroll.
func (b *LogBuffer) Window(n, offset int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	total := len(b.lines)
	if offset > total {
		// Scrolled past the top: clamp to the oldest window.
		if n > total {
			n = total
		}
		return append([]string(nil), b.lines[:n]...)
	}
	end := total - offset
	if end < 0 {
		end = 0
	}
	start := end - n
	if start < 0 {
		start = 0
	}
	return append([]string(nil), b.lines[start:end]...)
}

// Len returns the number of buffered lines.
func (b *LogBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}
