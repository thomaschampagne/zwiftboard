package ble

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestNotificationGateRejectsLateFrameDuringTeardown(t *testing.T) {
	var held atomic.Bool
	var gate notificationGate
	callback := gate.Wrap(func([]byte) { held.Store(true) })
	callback([]byte{0x23})
	gate.Close()
	held.Store(false) // session releases its held-key reservations
	// A late frame during writer drain or native disconnect must not reserve
	// another key after the session's final release.
	callback([]byte{0x23})
	if held.Load() {
		t.Fatal("late notification reserved a key after final release")
	}
}

func TestNotificationGateDrainsActiveCallbackBeforeRelease(t *testing.T) {
	var held atomic.Bool
	entered, finish := make(chan struct{}), make(chan struct{})
	var gate notificationGate
	callback := gate.Wrap(func([]byte) { close(entered); <-finish; held.Store(true) })
	callbackDone := make(chan struct{})
	go func() { callback(nil); close(callbackDone) }()
	<-entered
	closing, closed := make(chan struct{}), make(chan struct{})
	go func() { close(closing); gate.Close(); held.Store(false); close(closed) }()
	<-closing
	select {
	case <-closed:
		close(finish)
		<-callbackDone
		t.Fatal("teardown released keys before active callback finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(finish)
	<-callbackDone
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("teardown did not drain callback")
	}
	if held.Load() {
		t.Fatal("active callback reserved a key after final release")
	}
}
