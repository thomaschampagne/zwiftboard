package ble

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionWriterSerializesAndCloses(t *testing.T) {
	var active atomic.Int32
	var writes atomic.Int32
	w := &sessionWriter{write: func([]byte) (int, error) {
		if active.Add(1) != 1 {
			t.Error("overlapping characteristic writes")
		}
		defer active.Add(-1)
		writes.Add(1)
		return 1, nil
	}}
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() { _, _ = w.Write([]byte{1}) })
	}
	wg.Wait()
	w.Close()
	if _, err := w.Write([]byte{1}); err == nil {
		t.Fatal("late callback wrote after close")
	}
	if writes.Load() != 100 {
		t.Fatalf("writes=%d", writes.Load())
	}
}

func TestSessionWriterRejectsDisconnectedDeviceBeforeQueuedWrite(t *testing.T) {
	wrote := false
	w := &sessionWriter{alive: func() error { return errors.New("disconnected") }, write: func([]byte) (int, error) { wrote = true; return 1, nil }}
	if _, err := w.Write([]byte{1}); err == nil || wrote {
		t.Fatalf("err=%v wrote=%v", err, wrote)
	}
}

func TestAckDoesNotBlockNotificationAndStopsAtClose(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	w := newSessionWriter(nil, func([]byte) (int, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return 3, nil
	})
	w.Ack()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("ack never reached backend")
	}
	// The backend is blocked, as a synchronous fallback can be. Notification
	// handling must still return promptly and repeated challenges stay bounded.
	returned := make(chan struct{})
	go func() {
		for range 100 {
			w.Ack()
		}
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("notification blocked behind write")
	}
	close(release)
	w.Close()
	count := calls.Load()
	if count > 2 {
		t.Fatalf("repeated challenges queued %d writes; want at most in-flight plus one pending", count)
	}
	w.Ack()
	if _, err := w.Write([]byte{1}); err == nil {
		t.Fatal("write after close succeeded")
	}
	if calls.Load() != count {
		t.Fatal("write after close reached backend")
	}
}
