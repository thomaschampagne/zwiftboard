package tui

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zwiftboard/internal/zwift"
)

func TestPodStatesPrecedence(t *testing.T) {
	conn := map[zwift.Pod]bool{zwift.PodLeft: true}
	seen := map[zwift.Pod]bool{zwift.PodLeft: true, zwift.PodRight: true}
	got := PodStates(
		func(p zwift.Pod) bool { return conn[p] },
		func(p zwift.Pod, _ time.Duration) bool { return seen[p] },
	)
	if got.Left != PodConnected || got.Right != PodDetected {
		t.Fatalf("got %+v, want left connected, right detected", got)
	}
	got = PodStates(func(zwift.Pod) bool { return false }, func(zwift.Pod, time.Duration) bool { return false })
	if got.Left != PodOff || got.Right != PodOff {
		t.Fatalf("got %+v, want both off", got)
	}
}

func TestPollSendsOnlyOnChange(t *testing.T) {
	var mu sync.Mutex
	right := false
	var sent []tea.Msg
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Poll(ctx, func(m tea.Msg) { mu.Lock(); sent = append(sent, m); mu.Unlock() }, time.Millisecond,
			func(p zwift.Pod) bool { mu.Lock(); defer mu.Unlock(); return p == zwift.PodRight && right },
			func(zwift.Pod, time.Duration) bool { return false })
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	right = true
	mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("sent %d msgs, want 2 (initial + change): %v", len(sent), sent)
	}
}
