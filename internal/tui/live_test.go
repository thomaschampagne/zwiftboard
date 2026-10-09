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

func TestPollSendsBTAndPodsOnlyOnChange(t *testing.T) {
	var mu sync.Mutex
	right := false
	var sent []tea.Msg
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Poll(ctx, func(m tea.Msg) { mu.Lock(); sent = append(sent, m); mu.Unlock() }, time.Millisecond, Sources{
			BT:        func() bool { return true },
			Connected: func(p zwift.Pod) bool { mu.Lock(); defer mu.Unlock(); return p == zwift.PodRight && right },
			Seen:      func(zwift.Pod, time.Duration) bool { return false },
		})
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
	if len(sent) != 3 {
		t.Fatalf("sent %d msgs, want 3 (BT + pods initial, then pod change): %v", len(sent), sent)
	}
}

func TestPollReportsBluetoothLoss(t *testing.T) {
	var mu sync.Mutex
	bt := true
	var sent []tea.Msg
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Poll(ctx, func(m tea.Msg) { mu.Lock(); sent = append(sent, m); mu.Unlock() }, time.Millisecond, Sources{
			BT:        func() bool { mu.Lock(); defer mu.Unlock(); return bt },
			Connected: func(zwift.Pod) bool { return false },
			Seen:      func(zwift.Pod, time.Duration) bool { return false },
		})
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	bt = false
	mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if last, ok := sent[len(sent)-1].(BTMsg); !ok || bool(last) {
		t.Fatalf("last msg = %v, want BTMsg(false)", sent[len(sent)-1])
	}
}

func TestPollReportsFocusWindowChanges(t *testing.T) {
	var mu sync.Mutex
	found := true
	var sent []tea.Msg
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Poll(ctx, func(m tea.Msg) { mu.Lock(); sent = append(sent, m); mu.Unlock() }, time.Millisecond, Sources{
			BT:        func() bool { return true },
			Connected: func(zwift.Pod) bool { return false },
			Seen:      func(zwift.Pod, time.Duration) bool { return false },
			Focus:     func() bool { mu.Lock(); defer mu.Unlock(); return found },
		})
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	found = false
	mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	focus := 0
	for _, m := range sent {
		if _, ok := m.(FocusMsg); ok {
			focus++
		}
	}
	if focus != 2 {
		t.Fatalf("FocusMsg sent %d times, want 2 (initial + change)", focus)
	}
	if last := sent[len(sent)-1]; last != FocusMsg(false) {
		t.Fatalf("last msg = %v, want FocusMsg(false)", last)
	}
}
