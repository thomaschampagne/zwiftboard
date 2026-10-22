package ble

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// solveTimeout bounds one challenge solve. The HTTP client is slower (15s);
// the pod should never wait on a dead network. On timeout the empty ack goes
// out, exactly like a failed solve.
const solveTimeout = 10 * time.Second

// ChallengeSolver answers a pod's 0xFF 03 unlock challenge with the
// server-signed reply. internal/unlock implements it; nil means "no Zwift
// sign-in", and the session keeps the old empty-ack behaviour.
type ChallengeSolver interface {
	Solve(ctx context.Context, challenge []byte) ([]byte, error)
}

// challengePayload returns the protobuf payload of a challenge frame: the game
// reads exactly the bytes after the fixed FF 03 00 prefix and POSTs them to its
// d-lock endpoint, and that is what the reply answers. Frames too short to hold
// a payload are not challenges worth solving.
func challengePayload(frame []byte) []byte {
	if len(frame) < 4 {
		return nil
	}
	return frame[3:]
}

// solveFrame builds the sync-rx reply for one challenge: ff 04 00 followed by
// the server's bytes. A nil solver, an empty payload or a failed solve yields
// the plain ff 04 00 ack — the old behaviour is the floor, never a regression.
func solveFrame(ctx context.Context, solver ChallengeSolver, challenge []byte) []byte {
	frame := append([]byte(nil), ackSeq...)
	if solver == nil || len(challenge) == 0 {
		return frame
	}
	reply, err := solver.Solve(ctx, challenge)
	if err != nil {
		slog.Warn("pod unlock solve failed — answering with the empty ack (the LEFT controller may stay locked)", "error", err)
		return frame
	}
	return append(frame, reply...)
}

// latestSolve runs solves one at a time and keeps only the newest queued
// challenge. The pod repeats its challenge at connect; an older challenge's
// reply must never be written after a newer one, and a slow network must not
// stack goroutines.
type latestSolve struct {
	mu      sync.Mutex
	running bool
	pending []byte
	queued  bool
}

// push queues a challenge and starts the worker when idle.
func (q *latestSolve) push(challenge []byte, run func([]byte)) {
	q.mu.Lock()
	q.pending = challenge
	q.queued = true
	if q.running {
		q.mu.Unlock()
		return
	}
	q.running = true
	q.mu.Unlock()

	go func() {
		for {
			q.mu.Lock()
			ch, ok := q.pending, q.queued
			q.pending, q.queued = nil, false
			if !ok {
				q.running = false
				q.mu.Unlock()
				return
			}
			q.mu.Unlock()
			run(ch)
		}
	}()
}
