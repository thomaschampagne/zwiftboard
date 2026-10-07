// Package ble connects to Zwift Click V2 controllers over Bluetooth LE
// (WinRT via tinygo.org/x/bluetooth) and turns button frames into key taps.
//
// Service discovery note: WinRT often returns a partial service list right
// after connect, so we discover ALL services (with retry) and match
// characteristics by UUID instead of filtering by service UUID.
//
// Close Zwift / Companion first: each controller accepts a single BLE connection.
package ble

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"

	"zwiftclickv2-keyboard/internal/keys"
	"zwiftclickv2-keyboard/internal/zwift"
)

var (
	adapter   = bluetooth.DefaultAdapter
	connectMu sync.Mutex // Windows copes better with serialized connects

	// Verbose gates raw frames, service lists and tap logging (-v).
	Verbose bool
	// SendAck sends ff 04 00 to devices that echo RideOn (keeps unlock) (-ack).
	SendAck = true
	// TapDebounce is the minimum gap between two taps of the same button.
	// Shared across controllers: the pair mirrors button state (-debounce).
	TapDebounce = 200 * time.Millisecond

	tapMu         sync.Mutex
	lastTapByName = map[string]time.Time{}

	rideOn = []byte("RideOn")
	ackSeq = []byte{0xFF, 0x04, 0x00}
)

// Target is a controller to connect to.
type Target struct {
	Addr  bluetooth.Address
	Label string
}

// Enable initializes the BLE adapter.
func Enable() error { return adapter.Enable() }

// NewAddress parses a BLE MAC string into an address.
func NewAddress(mac string) (bluetooth.Address, error) {
	m, err := bluetooth.ParseMAC(mac)
	if err != nil {
		return bluetooth.Address{}, err
	}
	return bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: m}}, nil
}

// Discover scans for d and returns the Zwift controllers seen.
func Discover(d time.Duration) []Target {
	log.Printf("scanning %s for Zwift controllers (wake them by pressing a button)...", d)
	seen := map[string]Target{}
	var order []string

	timer := time.AfterFunc(d, func() { _ = adapter.StopScan() })
	err := adapter.Scan(func(a *bluetooth.Adapter, r bluetooth.ScanResult) {
		id, isZwift := zwift.IsZwift(r)
		if !isZwift {
			return
		}
		name := r.LocalName()
		key := r.Address.String()
		if _, dup := seen[key]; dup {
			return
		}
		label := fmt.Sprintf("%s/%s", zwift.ShortName(name), zwift.Tail(key))
		seen[key] = Target{Addr: r.Address, Label: label}
		order = append(order, key)
		log.Printf("found %q addr=%s deviceID=%d rssi=%d", name, key, id, r.RSSI)
	})
	timer.Stop()
	if err != nil {
		log.Printf("scan: %v", err)
	}

	out := make([]Target, 0, len(order))
	for _, k := range order {
		out = append(out, seen[k])
	}
	return out
}

// Session connects, handshakes and listens on one controller until the link
// breaks; the caller retries.
func Session(t Target, keyMap map[string]keys.Binding) error {
	connectMu.Lock()
	dev, async, syncRx, syncTx, err := connect(t)
	connectMu.Unlock()
	if err != nil {
		return err
	}
	defer dev.Disconnect()

	if err := async.EnableNotifications(ButtonHandler(t.Label, keyMap, keys.Tap)); err != nil {
		return fmt.Errorf("subscribe async: %w", err)
	}

	acked := false
	if syncTx != nil {
		err := syncTx.EnableNotifications(func(b []byte) {
			if Verbose {
				log.Printf("[%s] sync-tx % X", t.Label, b)
			}
			if len(b) == 0 {
				return
			}
			switch b[0] {
			case 'R':
				if !acked {
					acked = true
					log.Printf("[%s] unlocked (RideOn echoed)", t.Label)
					if SendAck {
						go func() { _, _ = writeChar(syncRx, ackSeq) }()
					}
				}
			case 0xFF:
				log.Printf("[%s] locked (crypto challenge) — open Zwift once to unlock, buttons may stay silent", t.Label)
			}
		})
		if err != nil {
			log.Printf("[%s] sync-tx subscribe failed (non-fatal): %v", t.Label, err)
		}
	}

	// Activation sequence from ZwiftBridge (verified on Click V2 hardware).
	activation := [][]byte{
		append(append([]byte(nil), rideOn...), 0x02, 0x03), // "RideOn" 02 03
		{0x00, 0x08, 0x00},
		{0x00, 0x08, 0x10},
	}
	for _, m := range activation {
		if _, err := writeChar(syncRx, m); err != nil {
			return fmt.Errorf("handshake: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Printf("[%s] connected, handshake sent, listening", t.Label)

	// Keepalive: device sleeps (~56s idle) without periodic 00 08 10. A failed
	// write doubles as disconnect detection.
	keep := []byte{0x00, 0x08, 0x10}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for range tick.C {
		if _, err := writeChar(syncRx, keep); err != nil {
			return fmt.Errorf("disconnected: %w", err)
		}
	}
	return nil
}

// writeChar writes without response, falling back to write-with-response for
// characteristics that do not support the former.
func writeChar(c *bluetooth.DeviceCharacteristic, b []byte) (int, error) {
	if c == nil {
		return 0, errors.New("characteristic missing")
	}
	n, err := c.WriteWithoutResponse(b)
	if err == nil {
		return n, nil
	}
	n2, err2 := c.Write(b)
	if err2 == nil {
		return n2, nil
	}
	return 0, fmt.Errorf("write-without-response: %v; write: %w", err, err2)
}

func connect(t Target) (dev bluetooth.Device, async, syncRx, syncTx *bluetooth.DeviceCharacteristic, err error) {
	dev, err = adapter.Connect(t.Addr, bluetooth.ConnectionParams{})
	if err != nil {
		return
	}
	fail := func(e error) { _ = dev.Disconnect(); err = e }

	svcs, e := discoverServices(dev)
	if e != nil {
		fail(fmt.Errorf("service discovery: %w", e))
		return
	}
	if Verbose {
		list := make([]string, len(svcs))
		for i := range svcs {
			list[i] = svcs[i].UUID().String()
		}
		log.Printf("[%s] services: %s", t.Label, strings.Join(list, " "))
	}

	// Match characteristics by UUID across every service: WinRT may return a
	// partial list right after connect, and the service UUID is not needed.
	wantIDs := []string{zwift.AsyncUUID, zwift.SyncRXUUID, zwift.SyncTXUUID}
	found := map[string]*bluetooth.DeviceCharacteristic{}
	var seen []string
	for i := range svcs {
		seen = append(seen, strings.ToLower(svcs[i].UUID().String()))
		chars, ce := svcs[i].DiscoverCharacteristics(nil)
		if ce != nil {
			continue
		}
		for j := range chars {
			cu := strings.ToLower(chars[j].UUID().String())
			for _, id := range wantIDs {
				if cu == id {
					if _, dup := found[id]; !dup {
						found[id] = &chars[j]
					}
				}
			}
		}
		if len(found) == len(wantIDs) {
			break
		}
	}
	async, syncRx, syncTx = found[zwift.AsyncUUID], found[zwift.SyncRXUUID], found[zwift.SyncTXUUID]
	if async == nil || syncRx == nil {
		fail(fmt.Errorf("required characteristics missing (services seen: %s)", strings.Join(seen, " ")))
		return
	}
	return
}

// discoverServices enumerates all GATT services. The first uncached listing
// right after connect often comes back partial on Windows, so retry.
func discoverServices(dev bluetooth.Device) ([]bluetooth.DeviceService, error) {
	var last error
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(750 * time.Millisecond)
		}
		svcs, err := dev.DiscoverServices(nil)
		if err != nil {
			last = err
			continue
		}
		if len(svcs) > 0 {
			return svcs, nil
		}
		last = errors.New("no services returned")
	}
	return nil, last
}
