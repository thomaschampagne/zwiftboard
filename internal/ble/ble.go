package ble

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"

	"zwiftboard/internal/keys"
	"zwiftboard/internal/zwift"
)

var (
	adapter   = bluetooth.DefaultAdapter
	connectMu sync.Mutex // Windows copes better with serialized connects

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

// recoverLog absorbs a panic at a callback or goroutine boundary: it logs the
// stack clearly and lets the caller continue, so one bad frame or adapter flyer
// never takes the whole process down. Use with defer.
func recoverLog(where string) {
	if r := recover(); r != nil {
		slog.Warn("recovered from panic", "where", where, "panic", r, "stack", string(debug.Stack()))
	}
}

// Guarded runs fn in the same process-protecting way as recoverLog: it logs any
// panic (with stack) instead of letting it escape, so a bad BLE event or a
// WinRT fault — e.g. the PC's Bluetooth being switched off mid-run, which makes
// the adapter disappear and can panic a scan, an Enable, or a session — never
// takes the whole process down. Use to wrap an entire top-level task such as
// the Watch scan loop or adapter.Enable(); no panic can then survive it.
func Guarded(where string, fn func()) {
	defer recoverLog(where)
	fn()
}

// debugEnabled reports whether the active log level shows debug records.
func debugEnabled() bool {
	return slog.Default().Enabled(context.Background(), slog.LevelDebug)
}

// Target is a controller to connect to.
type Target struct {
	Addr  bluetooth.Address
	Label string
}

// Enable initializes the BLE adapter.
func Enable() error { return adapter.Enable() }

// scanGap is the pause between scan bursts.
const scanGap = 3 * time.Second

// scanFn/stopScanFn wrap the adapter calls: injected in tests so the scan
// logic is verifiable without BLE hardware (same DI pattern as ButtonHandler's
// tap parameter).
var (
	scanFn     = adapter.Scan
	stopScanFn = func() { _ = adapter.StopScan() }
)

// Watch scans for Zwift controllers forever, in bursts of burst length. For
// every controller not already registered (known reports that), onFound is
// called ONCE — after the burst ends, so the adapter is not scanning while
// sessions connect. Callers keep the process alive; Watch never returns.
func Watch(burst time.Duration, known func(addr string) bool, onFound func(Target)) {
	foundAny := false
	for {
		// One guarded iteration: a panic in a scan or in onFound (e.g. the
		// PC's Bluetooth was switched off mid-burst) costs one burst, never
		// the watch loop — we must keep looking for a controller that
		// appears minutes later (hard-won fact).
		func() {
			defer recoverLog("watch iteration")
			if !foundAny || debugEnabled() {
				slog.Info("scanning for Zwift controllers (wake them by pressing a button; keeps scanning until found)", "burst", burst)
			}
			pending, order, err := scanBurst(burst, known)
			if err != nil {
				slog.Warn("scan failed", "error", err)
			}

			for _, k := range order {
				onFound(pending[k])
			}
			if len(order) > 0 {
				foundAny = true
			}
		}()
		time.Sleep(scanGap)
	}
}

// collector accumulates one burst's discoveries. The scan callback runs on
// the platform's BLE event goroutine and may fire after Scan has already
// returned, so every access is locked; snapshot hands out copies so a late
// callback can only write the (now unread) originals, never race the reader —
// a bare concurrent map read and write is an uncatchable fatal error.
type collector struct {
	mu      sync.Mutex
	pending map[string]Target
	order   []string
}

// add records a freshly discovered controller; false when already pending.
func (c *collector) add(key string, t Target) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.pending[key]; dup {
		return false
	}
	c.pending[key] = t
	c.order = append(c.order, key)
	return true
}

// snapshot returns copies of the current state.
func (c *collector) snapshot() (map[string]Target, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pending := make(map[string]Target, len(c.pending))
	for k, v := range c.pending {
		pending[k] = v
	}
	return pending, append([]string(nil), c.order...)
}

// scanBurst runs one burst of scanFn and returns the freshly discovered
// controllers (not known to known() yet) in discovery order.
func scanBurst(burst time.Duration, known func(addr string) bool) (map[string]Target, []string, error) {
	c := &collector{pending: map[string]Target{}}
	err := func() (err error) {
		// Covers a panicking scanFn call (same goroutine); the callback and
		// the StopScan timer each recover their own — they run elsewhere.
		defer recoverLog("scan")
		timer := time.AfterFunc(burst, func() {
			defer recoverLog("stop scan") // AfterFunc runs on its own goroutine
			stopScanFn()
		})
		defer timer.Stop()
		return scanFn(func(a *bluetooth.Adapter, r bluetooth.ScanResult) {
			// This runs on the platform's event goroutine, where scanBurst's
			// defers can never reach it: a panic here would kill the process.
			defer recoverLog("scan callback")
			id, isZwift := zwift.IsZwift(r)
			if !isZwift {
				return
			}
			name := r.LocalName()
			key := r.Address.String()
			if known(key) {
				return
			}
			label := fmt.Sprintf("%s/%s", zwift.ShortName(name), zwift.Tail(key))
			if !c.add(key, Target{Addr: r.Address, Label: label}) {
				return
			}
			disp := name
			if disp == "" {
				disp = label // advertisements without a name: fall back to label
			}
			slog.Info("found controller", "name", disp, "addr", key, "deviceID", id, "rssi", r.RSSI)
		})
	}()
	pending, order := c.snapshot()
	return pending, order, err
}

// endSession tears down a connection safely: it Disconnects ONLY if the
// device still answers the probe. When the PC's Bluetooth is switched off,
// tinygo's ConnectionStatusChanged handler has already run Device.Disconnect()
// and closed the GATT session; a second Disconnect on the freed session is a
// use-after-free that kills the process with an unrecoverable AV (0xc0000005,
// crash-after-disable-bt-windows.log). Writes and service discovery fail
// gracefully on a dead session — only Disconnect crashes — so probe first and
// let the library own the teardown when the device is gone. probe/disconnect
// are injectable for tests.
func endSession(where string, probe func() error, disconnect func()) {
	if err := probe(); err != nil {
		slog.Debug("device no longer answers — skipping disconnect", "where", where, "probe", err)
		return
	}
	disconnect()
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
	defer func() {
		// Probe with one-shot service discovery, not a keepalive write: on a
		// live session discovery returns cached services immediately, while a
		// write can fail transiently on a live device and would wrongly skip
		// Disconnect — leaking the controller's single connection slot. On a
		// dead session (BT off) discovery fails gracefully, so the AV-prone
		// Disconnect is skipped.
		endSession("session "+t.Label, func() error {
			_, err := dev.DiscoverServices(nil)
			return err
		}, func() { _ = dev.Disconnect() })
	}()

	// Activation sequence from ZwiftBridge (verified on Click V2 hardware).
	activation := [][]byte{
		append(append([]byte(nil), rideOn...), 0x02, 0x03), // "RideOn" 02 03
		{0x00, 0x08, 0x00},
		{0x00, 0x08, 0x10},
	}

	// rearm pokes a controller back into streaming button frames without
	// dropping the link. challengeRearm (short ff 04 00 ack) answers the 0xFF
	// question that arrives on async and keeps an unlocked pod from turning
	// silent; the full silenceRearm adds the activation trio once the stream
	// has already stopped. The ff 04 00 ack alone is not the solved crypto
	// response, so this is a best-effort — the silence watchdog below is what
	// guarantees recovery.
	challengeRearm := func() {
		if !SendAck {
			return
		}
		if _, err := writeChar(syncRx, ackSeq); err != nil {
			slog.Debug("challenge re-arm write failed", "controller", t.Label, "error", err)
		}
	}
	silenceRearm := func() {
		if SendAck {
			if _, err := writeChar(syncRx, ackSeq); err != nil {
				slog.Debug("silence re-arm ack write failed", "controller", t.Label, "error", err)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		for _, m := range activation {
			if _, err := writeChar(syncRx, m); err != nil {
				slog.Debug("silence re-arm write failed", "controller", t.Label, "error", err)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// pod watches the 0x23 stream: the LEFT pod can stop sending button frames
	// while the BLE link stays up (Zwift's crypto watchdog), and without it the
	// session would never notice, so the left arrows/MIN would stay dead until
	// restart. The keepalive loop below escalates silence to a reconnect.
	// asyncNotify also answers 0xFF challenges (they arrive on async, not
	// sync-tx) and records button frames.
	pod := newPodWatcher(time.Now())
	handler := ButtonHandler(t.Label, keyMap, keys.Tap)
	if err := async.EnableNotifications(asyncNotify(time.Now, pod, challengeRearm, handler)); err != nil {
		return fmt.Errorf("subscribe async: %w", err)
	}

	acked := false
	if syncTx != nil {
		err := syncTx.EnableNotifications(func(b []byte) {
			defer recoverLog("sync-tx frame " + t.Label)
			slog.Debug("sync-tx", "controller", t.Label, "frame", fmt.Sprintf("% X", b))
			if len(b) == 0 {
				return
			}
			// Only the RideOn echo is indicated on sync-tx (0x52...). The 0xFF
			// crypto challenge arrives on the async characteristic and is
			// handled by asyncNotify above.
			if b[0] == 'R' && !acked {
				acked = true
				slog.Info("unlocked (RideOn echoed)", "controller", t.Label)
				if SendAck {
					go func() {
						defer recoverLog("ack write " + t.Label)
						_, _ = writeChar(syncRx, ackSeq)
					}()
				}
			}
		})
		if err != nil {
			slog.Warn("sync-tx subscribe failed (non-fatal)", "controller", t.Label, "error", err)
		}
	}

	for _, m := range activation {
		if _, err := writeChar(syncRx, m); err != nil {
			return fmt.Errorf("handshake: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	slog.Info("connected, handshake sent, listening", "controller", t.Label)

	// Keepalive: device sleeps (~56s idle) without periodic 00 08 10. A failed
	// write doubles as disconnect detection. Each tick also re-checks the
	// silence watchdog: a pod that stopped streaming button frames while the
	// link is otherwise fine is re-armed in place, then reconnected (a fresh
	// session re-runs the handshake and re-enters its healthy window).
	keep := []byte{0x00, 0x08, 0x10}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for range tick.C {
		if _, err := writeChar(syncRx, keep); err != nil {
			return fmt.Errorf("disconnected: %w", err)
		}
		switch pod.decide(time.Now(), silenceRearmAfter, silenceReconnectAfter, silenceRearmGap) {
		case actRearm:
			slog.Warn("button stream silent — re-arming controller in place", "controller", t.Label)
			silenceRearm()
		case actReconnect:
			return fmt.Errorf("button stream silent for %v — ending session so the controller reconnects and re-arms", silenceReconnectAfter)
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
	// fail tears the fresh connection down through endSession's probe: on a
	// live device (e.g. a transient discovery error) the probe succeeds and
	// Disconnect frees the controller's single connection slot; when BT died
	// mid-connect the probe fails and Disconnect — which would AV on the
	// freed session — is skipped. One-shot discovery: no retry loop here.
	fail := func(e error) {
		endSession("connect "+t.Label, func() error {
			_, de := dev.DiscoverServices(nil)
			return de
		}, func() { _ = dev.Disconnect() })
		err = e
	}

	svcs, e := discoverServices(dev)
	if e != nil {
		fail(fmt.Errorf("service discovery: %w", e))
		return
	}
	if debugEnabled() {
		list := make([]string, len(svcs))
		for i := range svcs {
			list[i] = svcs[i].UUID().String()
		}
		slog.Debug("services", "controller", t.Label, "uuids", strings.Join(list, " "))
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
