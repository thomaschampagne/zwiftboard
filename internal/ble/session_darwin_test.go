package ble

import (
	"errors"
	"testing"
)

func TestDarwinLivenessUsesConnectionState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		up        bool
		err       error
		wantError bool
	}{
		{"connected", true, nil, false},
		{"disconnected", false, nil, true},
		{"state error", true, errors.New("state unavailable"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := connectionState(func() (bool, error) { return tc.up, tc.err }); (err != nil) != tc.wantError {
				t.Fatalf("err=%v wantError=%v", err, tc.wantError)
			}
		})
	}
}

func TestDarwinTeardownCancelsWithoutWritableCharacteristic(t *testing.T) {
	disconnected := false
	teardownSession("discovery failed", func() error {
		t.Fatal("Darwin must not write a liveness probe")
		return errors.New("missing characteristic")
	}, func() { disconnected = true })
	if !disconnected {
		t.Fatal("CoreBluetooth connection not cancelled")
	}
}

func TestDarwinConnectRejectsEmptyDeviceWithoutNativeCalls(t *testing.T) {
	err := checkConnection("00000000-0000-0000-0000-000000000000", "12345678-1234-5678-9abc-d4060fa98604", func() (bool, error) { t.Fatal("state called on empty native device"); return false, nil }, func() { t.Fatal("disconnect called on empty native device") })
	if err == nil {
		t.Fatal("empty device from Connect accepted")
	}
}

func TestDarwinConnectRejectsDisconnectedDeviceAndCancels(t *testing.T) {
	cancelled := false
	if err := checkConnection("pod", "pod", func() (bool, error) { return false, nil }, func() { cancelled = true }); err == nil || !cancelled {
		t.Fatalf("err=%v cancelled=%v", err, cancelled)
	}
}

func TestDarwinConnectAcceptsMatchingLiveDevice(t *testing.T) {
	if err := checkConnection("pod", "pod", func() (bool, error) { return true, nil }, func() { t.Fatal("live device cancelled") }); err != nil {
		t.Fatal(err)
	}
}
