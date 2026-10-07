// Zwift Click V2 BLE listener for Windows (WinRT via tinygo.org/x/bluetooth).
//
//	go mod init zwiftclick
//	go get tinygo.org/x/bluetooth
//	go run . [-v] [-scan 10s] [-addr D4:06:0F:A9:86:04,...] [-config config.yaml] [-ack=true]
//
// Button presses are logged and, if config.yaml maps them, typed as real
// keyboard keys (Windows keybd_event).
//
// Click V2 is a pair of devices (LEFT: arrows + minus, RIGHT: Y/Z/A/B + plus).
// This connects to every Zwift device it finds and logs button edges.
//
// Protocol notes (Click V2, verified against jimhoefnagels/ZwiftBridge which works):
//   - activation: write "RideOn"+02 03, then 00 08 00, then 00 08 10 (100ms apart);
//     unlocked device echoes "RideOn" on SyncTX, a locked one sends a 0xFF challenge
//   - keepalive: write 00 08 10 every 2s (device sleeps otherwise)
//   - button frames: 0x23 + protobuf; field 1 = uint32 bitmap, bit==0 means pressed
//   - LEFT needs a ~24h hardware unlock done by the Zwift app; "ff 04 00" keeps an
//     already-unlocked device unlocked
//
// Service discovery: WinRT often returns a partial service list right after connect,
// so discover ALL services (with retry) and match characteristics by UUID instead of
// filtering by service UUID.
//
// Close Zwift / Companion first: each controller accepts a single BLE connection.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

const (
	zwiftCompanyID = 0x094A

	svcID    = "00000001-19ca-4651-86e5-fa29dcdd09d1"
	asyncID  = "00000002-19ca-4651-86e5-fa29dcdd09d1" // notify: key presses
	syncRxID = "00000003-19ca-4651-86e5-fa29dcdd09d1" // write: handshake / keepalive
	syncTxID = "00000004-19ca-4651-86e5-fa29dcdd09d1" // indicate: handshake reply

	msgKeyPad = 0x23
)

var (
	adapter     = bluetooth.DefaultAdapter
	connectMu   sync.Mutex // Windows copes better with serialized connects
	verbose     bool
	sendAck     bool
	tapDebounce = 200 * time.Millisecond

	tapMu         sync.Mutex
	lastTapByName = map[string]time.Time{}

	rideOn = []byte("RideOn")
	ackSeq = []byte{0xFF, 0x04, 0x00}
)

// Click V2 button bits, decoded from ZwiftBridge's known-good frame table
// (bit == 0 in the frame means pressed). Not the full Zwift Ride layout.
var buttons = map[uint32]string{
	0x00001: "LEFT",  // left module, arrow left
	0x00002: "UP",    // left module, arrow up
	0x00004: "RIGHT", // left module, arrow right
	0x00008: "DOWN",  // left module, arrow down
	0x00010: "A",
	0x00020: "B",
	0x00040: "Y",
	0x00080: "Z",
	0x00100: "MIN",  // left module, minus
	0x01000: "PLUS", // right module, plus
}

var knownMask = func() (m uint32) {
	for k := range buttons {
		m |= k
	}
	return
}()

type target struct {
	addr  bluetooth.Address
	label string
}

func main() {
	scanFor := flag.Duration("scan", 10*time.Second, "how long to scan for Zwift controllers")
	addrList := flag.String("addr", "", "comma-separated BLE addresses (e.g. D4:06:0F:A9:86:04) — skips scanning")
	configPath := flag.String("config", "config.yaml", "YAML file (cwd) mapping buttons to keyboard keys")
	flag.BoolVar(&verbose, "v", false, "log raw frames and unknown bits")
	flag.BoolVar(&sendAck, "ack", true, "send ff 04 00 to devices that echo RideOn (keeps unlock)")
	flag.DurationVar(&tapDebounce, "debounce", 200*time.Millisecond, "minimum gap between two taps of the same button")
	flag.Parse()

	keyBindings := loadKeyMap(*configPath)

	if err := adapter.Enable(); err != nil {
		log.Fatalf("enable BLE adapter: %v", err)
	}

	var targets []target
	if *addrList != "" {
		for _, s := range strings.Split(*addrList, ",") {
			s = strings.TrimSpace(s)
			mac, err := bluetooth.ParseMAC(s)
			if err != nil {
				log.Fatalf("bad -addr %q: %v", s, err)
			}
			a := bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: mac}}
			targets = append(targets, target{addr: a, label: "zwift/" + tail(s)})
		}
	} else {
		targets = discover(*scanFor)
	}
	if len(targets) == 0 {
		log.Fatal("no Zwift controllers found — wake them (press a button), close Zwift, retry")
	}

	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			for {
				if err := session(t, keyBindings); err != nil {
					log.Printf("[%s] %v", t.label, err)
				}
				time.Sleep(5 * time.Second)
			}
		}(t)
	}
	log.Println("running — Ctrl+C to quit")
	wg.Wait()
}

func discover(d time.Duration) []target {
	log.Printf("scanning %s for Zwift controllers (wake them by pressing a button)...", d)
	seen := map[string]target{}
	var order []string

	timer := time.AfterFunc(d, func() { _ = adapter.StopScan() })
	err := adapter.Scan(func(a *bluetooth.Adapter, r bluetooth.ScanResult) {
		id, isZwift := zwiftDeviceID(r)
		name := r.LocalName()
		if !isZwift && !strings.HasPrefix(strings.ToLower(name), "zwift") {
			return
		}
		key := r.Address.String()
		if _, dup := seen[key]; dup {
			return
		}
		label := fmt.Sprintf("%s/%s", shortName(name), tail(key))
		seen[key] = target{addr: r.Address, label: label}
		order = append(order, key)
		log.Printf("found %q addr=%s deviceID=%d rssi=%d", name, key, id, r.RSSI)
	})
	timer.Stop()
	if err != nil {
		log.Printf("scan: %v", err)
	}

	out := make([]target, 0, len(order))
	for _, k := range order {
		out = append(out, seen[k])
	}
	return out
}

func session(t target, keys map[string]keyBinding) error {
	connectMu.Lock()
	dev, async, syncRx, syncTx, err := connect(t)
	connectMu.Unlock()
	if err != nil {
		return err
	}
	defer dev.Disconnect()

	if err := async.EnableNotifications(buttonHandler(t.label, keys, keyTap)); err != nil {
		return fmt.Errorf("subscribe async: %w", err)
	}

	acked := false
	if syncTx != nil {
		err := syncTx.EnableNotifications(func(b []byte) {
			if verbose {
				log.Printf("[%s] sync-tx % X", t.label, b)
			}
			if len(b) == 0 {
				return
			}
			switch b[0] {
			case 'R':
				if !acked {
					acked = true
					log.Printf("[%s] unlocked (RideOn echoed)", t.label)
					if sendAck {
						go func() { _, _ = writeChar(syncRx, ackSeq) }()
					}
				}
			case 0xFF:
				log.Printf("[%s] locked (crypto challenge) — open Zwift once to unlock, buttons may stay silent", t.label)
			}
		})
		if err != nil {
			log.Printf("[%s] sync-tx subscribe failed (non-fatal): %v", t.label, err)
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
	log.Printf("[%s] connected, handshake sent, listening", t.label)

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

func connect(t target) (dev bluetooth.Device, async, syncRx, syncTx *bluetooth.DeviceCharacteristic, err error) {
	dev, err = adapter.Connect(t.addr, bluetooth.ConnectionParams{})
	if err != nil {
		return
	}
	fail := func(e error) { _ = dev.Disconnect(); err = e }

	svcs, e := discoverServices(dev)
	if e != nil {
		fail(fmt.Errorf("service discovery: %w", e))
		return
	}
	if verbose {
		list := make([]string, len(svcs))
		for i := range svcs {
			list[i] = svcs[i].UUID().String()
		}
		log.Printf("[%s] services: %s", t.label, strings.Join(list, " "))
	}

	// Match characteristics by UUID across every service: WinRT may return a
	// partial list right after connect, and the service UUID is not needed.
	wantIDs := []string{asyncID, syncRxID, syncTxID}
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
	async, syncRx, syncTx = found[asyncID], found[syncRxID], found[syncTxID]
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

// claimTap reserves the right to tap key for button name. Shared by ALL
// controllers: the Click V2 pair mirrors button state (one press produces the
// same frame from both units within milliseconds), so the dedup window must be
// global, not per-device. Returns false if the same button was claimed within
// tapDebounce.
func claimTap(name string) bool {
	tapMu.Lock()
	defer tapMu.Unlock()
	now := time.Now()
	if t0, ok := lastTapByName[name]; ok && now.Sub(t0) < tapDebounce {
		return false
	}
	lastTapByName[name] = now
	return true
}

func buttonHandler(label string, keys map[string]keyBinding, tap func(keyBinding)) func([]byte) {
	prev := uint32(0xFFFFFFFF) // all released
	return func(b []byte) {
		if len(b) == 0 {
			return
		}
		if verbose {
			log.Printf("[%s] raw % X", label, b)
		}
		if b[0] != msgKeyPad {
			return // 0x15 / 0x19 idle & status frames
		}
		cur, ok := bitmap(b[1:])
		if !ok {
			return
		}
		changed := cur ^ prev
		if !verbose {
			changed &= knownMask
		}
		for bit := 0; bit < 32; bit++ {
			m := uint32(1) << bit
			if changed&m == 0 {
				continue
			}
			name, known := buttons[m]
			if !known {
				name = fmt.Sprintf("BIT%d", bit)
			}
			state := "released"
			if cur&m == 0 {
				state = "pressed"
			}
			suffix := ""
			if state == "pressed" {
				if bnd, mapped := keys[name]; mapped {
					// claimTap is GLOBAL across controllers: the pair mirrors
					// button state, so one physical press arrives as the same
					// frame from both units (and possibly retransmitted).
					// One claim per button inside the window = one key.
					if claimTap(name) {
						tap(bnd)
						suffix = " -> " + bnd.token
					} else {
						suffix = " -> " + bnd.token + " (duplicate)"
					}
				}
			}
			log.Printf("[%s] %-10s %s%s", label, name, state, suffix)
		}
		prev = cur
	}
}

// bitmap extracts protobuf field 1 (varint) = key bitmap, skipping other fields.
func bitmap(p []byte) (uint32, bool) {
	for i := 0; i < len(p); {
		tag, n := uvarint(p[i:])
		if n == 0 {
			return 0, false
		}
		i += n
		num, wt := tag>>3, tag&7
		switch wt {
		case 0:
			v, n := uvarint(p[i:])
			if n == 0 {
				return 0, false
			}
			i += n
			if num == 1 {
				return uint32(v), true
			}
		case 1:
			i += 8
		case 2:
			l, n := uvarint(p[i:])
			if n == 0 {
				return 0, false
			}
			i += n + int(l)
		case 5:
			i += 4
		default:
			return 0, false
		}
	}
	return 0, false
}

func uvarint(b []byte) (uint64, int) {
	var x uint64
	for i, c := range b {
		if i == 10 {
			return 0, 0
		}
		x |= uint64(c&0x7f) << (7 * uint(i))
		if c < 0x80 {
			return x, i + 1
		}
	}
	return 0, 0
}

// Manufacturer data: company 0x094A, first data byte = Zwift device ID.
func zwiftDeviceID(r bluetooth.ScanResult) (byte, bool) {
	for _, m := range r.ManufacturerData() {
		if m.CompanyID == zwiftCompanyID && len(m.Data) > 0 {
			return m.Data[0], true
		}
	}
	return 0, false
}

func shortName(n string) string {
	if n == "" {
		return "zwift"
	}
	return strings.ReplaceAll(n, " ", "")
}

func tail(addr string) string {
	if len(addr) > 5 {
		return addr[len(addr)-5:]
	}
	return addr
}
