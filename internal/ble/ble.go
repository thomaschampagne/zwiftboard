package ble

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
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

	// lastSeenAt records the last scan sighting of every Zwift controller
	// (registered or not). A fresh sighting is the only honest signal that a
	// right pod is awake enough to accept a connect, so reconnection is gated
	// on it instead of hammering a sleeping pod by address (see SeenRecently).
	seenMu     sync.Mutex
	lastSeenAt = map[string]time.Time{}
	// sideSeenAt records the last scan sighting of each pod SIDE (left/right
	// from the Zwift manufacturer record), used for the observational pair status
	// in main.go. Nothing is gated on it: both pods are simply connected.
	sideSeenAt = map[zwift.Pod]time.Time{}
	// nowFn is the clock used by markSeen/SeenRecently; injectable for tests.
	nowFn = time.Now

	rideOn = []byte("RideOn")
	ackSeq = []byte{0xFF, 0x04, 0x00}
	keep   = []byte{0x00, 0x08, 0x10} // teardown / connect-failure probe (any write works)

	// resetPod is the pod-reset byte: a lone 0x18 written to sync-rx makes a
	// Click V2 pod reboot and re-advertise instead of falling into its ~65s
	// idle sleep. See keepaliveTick / IdleReset.
	resetPod = []byte{0x18}

	// IdleReset arms a proactive pod reset after this much button silence, so
	// the pod reboots ON OUR SCHEDULE, before its ~65s idle watchdog strands it
	// asleep for 30-40s; the scan-gated caller then reconnects within seconds.
	// 0 disables the reset (-idle-reset).
	IdleReset = 55 * time.Second

	// handshakeStart is the Click V2 activation frame ("RideOn" 02 03), written
	// once after connect. The periodic keepalive re-sends plain rideOn ("RideOn").
	// Nothing we write to sync-rx holds a LONE RIGHT pod: the pod drops the link
	// ~65s after its last OWN transmission (a button frame) no matter what —
	// every payload variant was observed failing identically on Windows. The
	// keepalive's real job is liveness: a successful write proves the session is
	// up, a failed one detects the disconnect, and reconnect is scan-gated
	// (main.go waits for the pod to advertise again). The keepalive that holds
	// the link belongs to the LEFT pod path (unlocked pair), which this
	// right-only build dropped.
	handshakeStart = append(append([]byte(nil), rideOn...), 0x02, 0x03)
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
	// Side is the pod side decoded from the advertisement's Zwift manufacturer
	// record (PodUnknown for manually listed -addr targets, which the user
	// manages explicitly).
	Side zwift.Pod
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

// scanBroken is set when the last scan burst errored or panicked, which is how
// a switched-off Bluetooth radio shows up at runtime (Enable only runs once at
// start). Observational: the UI reads it; nothing in a session waits on it.
var scanBroken atomic.Bool

func setScanHealthy(ok bool) { scanBroken.Store(!ok) }

// ScanHealthy reports whether the most recent scan burst ran cleanly. True
// until a burst fails, so a fresh start never claims Bluetooth is off.
func ScanHealthy() bool { return !scanBroken.Load() }

// scanBurst runs one burst of scanFn and returns the freshly discovered
// controllers (not known to known() yet) in discovery order.
func scanBurst(burst time.Duration, known func(addr string) bool) (map[string]Target, []string, error) {
	c := &collector{pending: map[string]Target{}}
	healthy := false // stays false if scanFn panics (recovered below, err stays nil)
	err := func() (err error) {
		// Covers a panicking scanFn call (same goroutine); the callback and
		// the StopScan timer each recover their own — they run elsewhere.
		defer recoverLog("scan")
		timer := time.AfterFunc(burst, func() {
			defer recoverLog("stop scan") // AfterFunc runs on its own goroutine
			stopScanFn()
		})
		defer timer.Stop()
		err = scanFn(func(a *bluetooth.Adapter, r bluetooth.ScanResult) {
			// This runs on the platform's event goroutine, where scanBurst's
			// defers can never reach it: a panic here would kill the process.
			defer recoverLog("scan callback")
			id, isZwift := zwift.IsZwift(r)
			if !isZwift {
				return
			}
			name := r.LocalName()
			key := r.Address.String()
			// Record the sighting even for a registered controller — this is how
			// reconnection learns the pod woke up after its ~65s idle sleep.
			markSeen(key)
			markSeenSide(zwift.PodSide(id))
			if known(key) {
				return
			}
			label := fmt.Sprintf("%s/%s", zwift.ShortName(name), zwift.Tail(key))
			if !c.add(key, Target{Addr: r.Address, Label: label, Side: zwift.PodSide(id)}) {
				return
			}
			disp := name
			if disp == "" {
				disp = label // advertisements without a name: fall back to label
			}
			slog.Info("found controller", "name", disp, "addr", key, "deviceID", id, "rssi", r.RSSI)
		})
		healthy = err == nil
		return err
	}()
	setScanHealthy(healthy)
	pending, order := c.snapshot()
	return pending, order, err
}

// keepaliveTick decides one keepalive-ticker action for a live session.
// While the pod has been active recently it returns a plain liveness ping
// (the RideOn write that doubles as disconnect detection). After IdleReset of
// button silence it returns a pod reset INSTEAD, exactly once: writing 0x18
// reboots the pod on our schedule, before its ~65s idle watchdog can strand
// it asleep for 30-40s — the pod re-advertises within seconds and the
	// scan-gated caller reconnects. Observed on Windows: with the LEFT pod detected nearby the
// right pod STILL dropped ~67-72s after its last button, so the reset is sent
// regardless of the left pod.
func keepaliveTick(resetSent bool, lastActivity, now time.Time) (reset, ping bool) {
	if IdleReset > 0 && !resetSent && now.Sub(lastActivity) >= IdleReset {
		return true, false
	}
	return false, true
}

// markSeen records that addr was advertising just now. Every Zwift device is
// recorded, known or not: the sighting is what connect() latches onto, so a
// known-but-sleeping controller can be reconnected the moment its radio wakes.
func markSeen(key string) {
	seenMu.Lock()
	lastSeenAt[key] = nowFn()
	seenMu.Unlock()
}

// SeenRecently reports whether addr was seen advertising within the window.
// Keys are the canonical r.Address.String() strings, the same form main.go's
// registry uses, so look them up directly.
func SeenRecently(addr string, within time.Duration) bool {
	seenMu.Lock()
	at, ok := lastSeenAt[addr]
	seenMu.Unlock()
	return ok && nowFn().Sub(at) <= within
}

// connected counts live sessions per pod side (observational only: nothing
// is gated on it — a connected pod may stop advertising). main.go reports
// "ready" once both pods hold a session.
var (
	connMu    sync.Mutex
	connected = map[zwift.Pod]int{}
)

func markConnected(p zwift.Pod, up bool) {
	connMu.Lock()
	defer connMu.Unlock()
	if up {
		connected[p]++
	} else if connected[p] > 0 {
		connected[p]--
	}
}

// Connected reports whether a session to a pod of side p is live.
func Connected(p zwift.Pod) bool {
	connMu.Lock()
	defer connMu.Unlock()
	return connected[p] > 0
}

// ClearSighting forgets a controller's last advertisement. Called when a
// session ends: reconnection must then wait for a sighting STRICTLY AFTER the
// drop. A pod may advertise throughout its connected session, so without this
// the gate would stay open for ~30s after the link died and retry the sleeping
// pod by address — leaking a GattSession per attempt again.
func ClearSighting(addr string) {
	seenMu.Lock()
	delete(lastSeenAt, addr)
	seenMu.Unlock()
}

// markSeenSide records that a pod of side p was advertising just now, so the
// status can tell which controllers are detected. Each side is tracked even
// when its pod has no session yet. The LEFT pod is the pair's BLE anchor and
// is connected like the right one (connecting only the right pod drops it).
func markSeenSide(p zwift.Pod) {
	seenMu.Lock()
	sideSeenAt[p] = nowFn()
	seenMu.Unlock()
}

// SideSeenRecently reports whether a pod of side p was seen advertising within
// the window (observational; see pairStatus in main.go).
func SideSeenRecently(p zwift.Pod, within time.Duration) bool {
	seenMu.Lock()
	at, ok := sideSeenAt[p]
	seenMu.Unlock()
	return ok && nowFn().Sub(at) <= within
}

// endSession tears down a connection safely: it Disconnects ONLY if the
// device still answers the probe. When the PC's Bluetooth is switched off,
// tinygo's ConnectionStatusChanged handler has already run Device.Disconnect()
// and closed the GATT session; a second Disconnect on the freed session is a
// use-after-free that kills the process with an unrecoverable AV (0xc0000005,
// crash-after-disable-bt-windows.log). Writes fail gracefully on a dead
// session — service discovery does NOT (listing services AVs on the freed
// COM object, observed on Windows), so the probe is always a write, never a
// discover. probe/disconnect are injectable for tests.
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
		// Probe with a keepalive write, not service discovery: on a dead session
		// (BT off) DiscoverServices itself AVs on the freed COM object (0xc0000005,
		// same family as the Disconnect crash — observed during the podwatch run),
		// while writes fail gracefully. So probe with the same write the keepalive
		// loop queues and Disconnect only when it answers. A transient write
		// failure on a LIVE device just defers the release of the controller's
		// single connection slot to the device's own sleep timeout (~56s) — losing
		// the slot for a minute beats an unrecoverable crash.
		endSession("session "+t.Label, func() error {
			if syncRx == nil {
				return errors.New("no sync-rx characteristic to probe")
			}
			_, err := writeChar(syncRx, keep)
			return err
		}, func() { _ = dev.Disconnect() })
	}()
	// A session ends when a keepalive write fails (a real disconnect) — release
	// the keys still held so a dropped controller can't leave them stuck down.
	// The held count is shared with the other pod's session, so a key that
	// sibling still sees pressed stays down until that sibling's own release.
	hk := holdKeys(keys.Down, keys.Up)
	defer hk.ReleaseAll()

	// Activation sequence (verified on Click V2 hardware).
	activation := [][]byte{
		handshakeStart, // "RideOn" 02 03
		{0x00, 0x08, 0x00},
		{0x00, 0x08, 0x10},
	}

	// challengeRearm answers the 0xFF crypto challenge that arrives on async
	// (not sync-tx): a short ff 04 00 ack keeps an already-unlocked pod's button
	// stream from going silent.
	challengeRearm := func() {
		if !SendAck {
			return
		}
		if _, err := writeChar(syncRx, ackSeq); err != nil {
			slog.Debug("challenge re-arm write failed", "controller", t.Label, "error", err)
		}
	}

	handler := ButtonHandler(t.Label, keyMap, hk.Press, hk.Release)
	// lastActivity tracks the pod's last own transmission (any received frame).
	// It gates the proactive pod reset so an active session is never rebooted.
	lastActivity := time.Now()
	noteActivity := func() { lastActivity = time.Now() }
	if err := async.EnableNotifications(asyncNotify(t.Label, challengeRearm, noteActivity, handler)); err != nil {
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
			noteActivity()
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
	markConnected(t.Side, true)
	defer markConnected(t.Side, false)

	// Keepalive every 3s: re-send the raw "RideOn" opcode frame to sync-rx.
	// On a LONE RIGHT pod it does NOT hold the link — the pod still drops ~65s
	// after its last own transmission; every payload (RideOn alone,
	// "RideOn"+02 03, 00 08 10, 00 08 00, with and without jitter) fails
	// identically on Windows. The write is kept as the LIVENESS probe: Idle
	// button silence is normal (0x23 frames only stream on presses), so the
	// only trustworthy disconnect signal is a failed keepalive write, and that
	// is why the session is not ended on frame silence.
	//
	// After IdleReset of silence the tick sends a single pod RESET (0x18)
	// instead: the pod reboots on our schedule before its ~65s idle watchdog
	// can strand it asleep for 30-40s, re-advertises within seconds, and the
	// scan-gated caller reconnects. Button activity restarts the idle clock, so
	// a session in use is never rebooted.
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	resetSent := false
	for range tick.C {
		if reset, _ := keepaliveTick(resetSent, lastActivity, time.Now()); reset {
			resetSent = true
			slog.Debug("idle — sending pod reset (0x18) to reboot before the ~65s sleep watchdog", "controller", t.Label, "idle", IdleReset)
			if _, err := writeChar(syncRx, resetPod); err != nil {
				return fmt.Errorf("reset: %w", err)
			}
			continue
		}
		if _, err := writeChar(syncRx, rideOn); err != nil {
			return fmt.Errorf("disconnected: %w", err)
		}
	}
	return nil
}

// asyncNotify wraps one controller's async (keypad) notification stream — the
// characteristic that carries 0x23 button frames AND the Zwift 0xFF crypto
// challenge (delivered here, NOT on sync-tx). A 0xFF 03 challenge triggers the
// re-arm ack (ff 04 00) so an already-unlocked pod keeps its button stream
// alive; the routine 0xFF 05 battery/status frame is deliberately ignored.
// Every frame still reaches bh unchanged for decode/logging; any frame counts
// as pod activity (it proves the pod's own radio woke enough to transmit),
// which is what keeps the idle-reset from firing during a close session.
func asyncNotify(label string, rearm func(), activity func(), bh func([]byte)) func([]byte) {
	return func(b []byte) {
		defer recoverLog("async frame")
		if len(b) == 0 {
			return
		}
		activity()
		if b[0] == 0xFF && len(b) > 1 && b[1] == 0x03 {
			slog.Debug("crypto challenge from click — acking to keep the button stream alive", "controller", label, "frame", fmt.Sprintf("% X", b))
			rearm()
		}
		bh(b)
	}
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
	// live device the probe (a keepalive write) succeeds and Disconnect frees
	// the controller's single connection slot; when BT died mid-connect the
	// write fails and Disconnect — which would AV on the freed session — is
	// skipped. Service discovery is NOT a safe probe: on a freed session it
	// AVs (0xc0000005, observed on Windows). When no characteristic is in hand
	// there is nothing safe to write to, so skip Disconnect and let the device
	// release the slot on its own link timeout.
	fail := func(e error) {
		probe := syncRx
		if probe == nil {
			probe = async
		}
		if probe != nil {
			endSession("connect "+t.Label,
				func() error {
					_, we := writeChar(probe, keep)
					return we
				}, func() { _ = dev.Disconnect() })
		} else {
			slog.Debug("no characteristic available to probe — skipping disconnect (slot releases on the device's own link timeout)", "controller", t.Label)
		}
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
