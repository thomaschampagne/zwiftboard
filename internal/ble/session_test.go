package ble

import (
	"errors"
	"testing"
)

func TestEndSessionSkipsDisconnectWhenProbeFails(t *testing.T) {
	probes, disconnects := 0, 0
	endSession("t", func() error { probes++; return errors.New("device gone") },
		func() { disconnects++ })
	if probes != 1 || disconnects != 0 {
		t.Fatalf("probes=%d disconnects=%d, want 1/0", probes, disconnects)
	}
}

func TestEndSessionDisconnectsWhenProbeOK(t *testing.T) {
	disconnects := 0
	endSession("t", func() error { return nil }, func() { disconnects++ })
	if disconnects != 1 {
		t.Fatalf("disconnects=%d, want 1", disconnects)
	}
}
