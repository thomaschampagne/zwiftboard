package ble

import (
	"errors"
	"log/slog"
	"sync"
)

// sessionWriter serializes handshake, keepalive and acknowledgement writes.
// CoreBluetooth callbacks must never wait for a write-with-response: its reply
// is delivered on that same event thread. One worker and a coalescing queue keep
// repeated challenges from creating an unbounded number of blocked goroutines.
type sessionWriter struct {
	mu       sync.Mutex
	closed   bool
	alive    func() error
	write    func([]byte) (int, error)
	ack      chan struct{}
	done     chan struct{}
	finished chan struct{}
}

func newSessionWriter(alive func() error, write func([]byte) (int, error)) *sessionWriter {
	w := &sessionWriter{alive: alive, write: write, ack: make(chan struct{}, 1), done: make(chan struct{}), finished: make(chan struct{})}
	go func() {
		defer close(w.finished)
		defer recoverLog("ack worker")
		for {
			select {
			case <-w.done:
				return
			case <-w.ack:
				if _, err := w.Write(ackSeq); err != nil {
					slog.Debug("challenge re-arm write failed", "error", err)
				}
			}
		}
	}()
	return w
}

func (w *sessionWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, errors.New("session closed")
	}
	if w.alive != nil {
		if err := w.alive(); err != nil {
			return 0, err
		}
	}
	return w.write(b)
}

func (w *sessionWriter) Ack() {
	select {
	case <-w.done:
		return
	default:
	}
	select {
	case w.ack <- struct{}{}:
	default:
	}
}

// Close drains any write already in flight and rejects all later callbacks
// before the platform teardown can release the characteristic or connection.
func (w *sessionWriter) Close() {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		if w.done != nil {
			close(w.done)
		}
	}
	w.mu.Unlock()
	if w.finished != nil {
		<-w.finished
	}
}
