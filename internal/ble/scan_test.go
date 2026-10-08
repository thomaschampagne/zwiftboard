package ble

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"tinygo.org/x/bluetooth"
)

// fakePayload implements bluetooth.AdvertisementPayload so tests can fabricate
// a ScanResult without BLE hardware.
type fakePayload struct{ name string }

func (f fakePayload) LocalName() string                  { return f.name }
func (f fakePayload) HasServiceUUID(bluetooth.UUID) bool { return false }
func (f fakePayload) ServiceUUIDs() []bluetooth.UUID     { return nil }
func (f fakePayload) Bytes() []byte                      { return nil }
func (f fakePayload) ManufacturerData() []bluetooth.ManufacturerDataElement {
	return nil
}
func (f fakePayload) ServiceData() []bluetooth.ServiceDataElement { return nil }

func zwiftResult(addr string) bluetooth.ScanResult {
	mac, err := bluetooth.ParseMAC(addr)
	if err != nil {
		panic(err)
	}
	return bluetooth.ScanResult{
		Address:              bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: mac}},
		RSSI:                 -50,
		AdvertisementPayload: fakePayload{name: "Zwift Click"},
	}
}

type scanCallback = func(*bluetooth.Adapter, bluetooth.ScanResult)

func stubScan(t *testing.T, fn func(cb scanCallback) error) {
	t.Helper()
	old := scanFn
	scanFn = fn
	t.Cleanup(func() { scanFn = old })
}

func stubStop(t *testing.T, fn func()) {
	t.Helper()
	old := stopScanFn
	stopScanFn = fn
	t.Cleanup(func() { stopScanFn = old })
}

// The scan callback runs on the platform's event goroutine, NOT on the
// goroutine scanBurst runs on: a panic there escapes every defer in scanBurst
// and kills the process unless the callback recovers its own.
func TestScanBurstCallbackPanicRecovered(t *testing.T) {
	stubScan(t, func(cb scanCallback) error {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb(nil, zwiftResult("D4:06:0F:A9:86:04"))
		}()
		wg.Wait()
		return nil
	})
	pending, order, err := scanBurst(time.Second, func(string) bool { panic("known boom") })
	if err != nil || len(order) != 0 || len(pending) != 0 {
		t.Fatalf("pending=%d order=%d err=%v, want 0/0/nil", len(pending), len(order), err)
	}
}

func TestScanBurstScanCallPanicRecovered(t *testing.T) {
	stubScan(t, func(cb scanCallback) error { panic("adapter flyer") })
	pending, order, err := scanBurst(time.Second, func(string) bool { return false })
	if err != nil || len(order) != 0 || len(pending) != 0 {
		t.Fatalf("pending=%d order=%d err=%v, want 0/0/nil", len(pending), len(order), err)
	}
}

// A late callback firing after Scan returned must not race the reader of the
// results (concurrent map read and map write is an uncatchable fatal error),
// and must not mutate a snapshot already handed out. Run under -race.
func TestScanBurstLateCallbackRace(t *testing.T) {
	var wg sync.WaitGroup
	stubScan(t, func(cb scanCallback) error {
		cb(nil, zwiftResult("D4:06:0F:A9:86:04")) // synchronous first sighting
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				// distinct keys: late callbacks that actually WRITE, the
				// unsynchronized access a reader must never race with
				cb(nil, zwiftResult(fmt.Sprintf("D4:06:0F:A9:86:%02X", i)))
			}(i)
		}
		return nil // returns while the 50 goroutines may still be running
	})
	pending, order, err := scanBurst(time.Second, func(string) bool { return false })
	if err != nil || len(order) < 1 || len(pending) != len(order) {
		t.Fatalf("pending=%d order=%d err=%v, want equal counts >=1, nil err", len(pending), len(order), err)
	}
	wg.Wait()
	if len(pending) != len(order) {
		t.Fatalf("snapshot mutated by late callbacks: pending=%d order=%d, want equal", len(pending), len(order))
	}
}

// The StopScan timer callback runs on its own goroutine after scanBurst may
// have returned: a panic there escapes everything unless it recovers itself.
func TestStopScanPanicRecovered(t *testing.T) {
	stopped := make(chan struct{})
	stubStop(t, func() {
		close(stopped)
		panic("stop boom")
	})
	stubScan(t, func(cb scanCallback) error {
		time.Sleep(30 * time.Millisecond) // keep the scan open past burst=1ms
		return nil
	})
	if _, _, err := scanBurst(time.Millisecond, func(string) bool { return false }); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	// The timer goroutine read stopScanFn: sync with it before t.Cleanup
	// restores the variable, or the swap races that read.
	select {
	case <-stopped:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("StopScan timer never fired")
	}
}
