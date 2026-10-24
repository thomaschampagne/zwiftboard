package ble

import "sync"

// notificationGate owns delivery to one session. Close waits for a callback
// already executing, then rejects any late frames during writer drain and
// native disconnect. Callbacks must not synchronously call the BLE backend:
// acknowledgements are queued by sessionWriter instead.
type notificationGate struct {
	mu     sync.Mutex
	closed bool
}

func (g *notificationGate) Wrap(callback func([]byte)) func([]byte) {
	return func(frame []byte) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.closed {
			return
		}
		callback(frame)
	}
}

func (g *notificationGate) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
}
