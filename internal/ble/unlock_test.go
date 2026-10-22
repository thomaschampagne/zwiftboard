package ble

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeSolver struct {
	reply []byte
	err   error
	got   []byte
}

func (f *fakeSolver) Solve(_ context.Context, challenge []byte) ([]byte, error) {
	f.got = append([]byte(nil), challenge...)
	return f.reply, f.err
}

func TestChallengePayloadStripsPrefix(t *testing.T) {
	// The game forwards exactly the bytes after the fixed FF 03 00 prefix to
	// its REST call; the pod expects that payload as the wire body.
	frame := []byte{0xFF, 0x03, 0x00, 0x0A, 0x21, 0x02, 0xAB}
	if got := challengePayload(frame); len(got) != 4 || got[0] != 0x0A || got[3] != 0xAB {
		t.Fatalf("challengePayload = % X, want 0A 21 02 AB", got)
	}
	for _, short := range [][]byte{nil, {0xFF}, {0xFF, 0x03}, {0xFF, 0x03, 0x00}} {
		if got := challengePayload(short); got != nil {
			t.Fatalf("challengePayload(% X) = % X, want nil", short, got)
		}
	}
}

func TestSolveFrame(t *testing.T) {
	// Success: the signed reply follows the ff 04 00 prefix. Failure or no
	// solver: the plain empty ack, exactly today's behaviour.
	challenge := []byte{0x0A, 0x21}
	reply := []byte{0xAA, 0xBB}
	ok := &fakeSolver{reply: reply}
	if got := solveFrame(context.Background(), ok, challenge); len(got) != 5 || got[0] != 0xFF || got[1] != 0x04 || got[2] != 0x00 || got[3] != 0xAA || got[4] != 0xBB {
		t.Fatalf("solveFrame = % X, want FF 04 00 AA BB", got)
	}
	if len(ok.got) != 2 || ok.got[0] != 0x0A {
		t.Fatalf("solver got % X, want the challenge payload", ok.got)
	}

	bad := &fakeSolver{err: errors.New("offline")}
	if got := solveFrame(context.Background(), bad, challenge); len(got) != 3 {
		t.Fatalf("failed solve = % X, want the empty ack", got)
	}
	if got := solveFrame(context.Background(), nil, challenge); len(got) != 3 {
		t.Fatalf("nil solver = % X, want the empty ack", got)
	}
}

func TestLatestSolveKeepsNewest(t *testing.T) {
	// While one solve is in flight, a burst of challenges must collapse to the
	// newest one: the pod repeats its challenge at connect, and a stale reply
	// must never be written after a newer one.
	q := &latestSolve{}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var mu sync.Mutex
	var got []byte
	var runs int
	run := func(b []byte) {
		mu.Lock()
		runs++
		got = append(got, b[0])
		n := runs
		mu.Unlock()
		if n == 1 {
			close(started)
			<-release
		}
		if n == 2 {
			close(done)
		}
	}
	q.push([]byte{1}, run)
	<-started
	q.push([]byte{2}, run)
	q.push([]byte{3}, run)
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second solve never ran")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("solves = %v, want [1 3]", got)
	}
}
