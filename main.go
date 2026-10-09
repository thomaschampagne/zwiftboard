// Zwift Click V2 BLE listener for Windows (WinRT via tinygo.org/x/bluetooth).
//
//	go run . [-v] [-scan 10s] [-addr D4:06:0F:A9:86:04,...] [-config config.yaml] [-p mywhoosh] [-ack=true] [-log zwiftboard.log]
//
// Button presses are logged and, if config.yaml maps them, typed as real
// keyboard keys (Windows keybd_event). Log level comes from config.yaml
// (loglevel: debug|info|warn|error); -v forces debug.
//
// Click V2 is a pair of devices (LEFT: arrows + minus, RIGHT: Y/Z/A/B + plus).
// Click V2 is a PAIR: both pods are connected (each gets its own session and
// keepalive). Connecting only the right pod, or merely detecting the left one,
// does not stop the right pod's ~65s idle drop; with both connected the link
// stays up. Only the right pod's buttons are normally mapped (Y Z A B PLUS).
// Startup checks Bluetooth (asks for it to be switched on and retries), then
// asks for both controllers and reports the pair state as it changes.
// Scanning runs in bursts until controllers appear and keeps listening for
// new ones (a second controller may be turned on much later); it never gives
// up after the -scan window. Each found controller gets a reconnecting
// session goroutine.
//
// Close Zwift / Companion first: each controller accepts a single BLE connection.
package main

import (
	"flag"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zwiftboard/internal/ble"
	"zwiftboard/internal/config"
	"zwiftboard/internal/keys"
	"zwiftboard/internal/tui"
	"zwiftboard/internal/zwift"
)

func main() {
	scanFor := flag.Duration("scan", 10*time.Second, "length of one scan burst; scanning repeats until controllers are found and keeps listening for new ones")
	reconnect := flag.Duration("reconnect", 30*time.Second, "connect only to a controller seen advertising within this window (a sleeping right pod is never hammered by address)")
	addrList := flag.String("addr", "", "comma-separated BLE addresses (e.g. D4:06:0F:A9:86:04) — manages exactly these; each must visibly advertise before it is connected (a pod's radio wakes ~30-60s after sleeping)")
	configPath := flag.String("config", defaultConfigPath(), "YAML file with key mapping profiles (default: next to the program; created from the built-in default if missing)")
	var profile string
	flag.StringVar(&profile, "p", config.DefaultProfile, "config profile to use")
	flag.StringVar(&profile, "profile", config.DefaultProfile, "config profile to use (same as -p)")
	var verbose bool
	flag.BoolVar(&verbose, "v", false, "log raw frames and taps (forces log level debug)")
	flag.BoolVar(&ble.SendAck, "ack", true, "send ff 04 00 to devices that echo RideOn (keeps unlock)")
	flag.DurationVar(&ble.TapDebounce, "debounce", 200*time.Millisecond, "minimum gap between two taps of the same button")
	flag.DurationVar(&ble.IdleReset, "idle-reset", 55*time.Second, "after this much button silence, reboot the pod (write 0x18, OpenBikeControl's periodic reset) before its ~65s idle sleep; 0 disables")
	logPath := flag.String("log", "", "also write log lines to this file (.log), truncated at startup")
	plain := flag.Bool("plain", false, "plain log output on stderr instead of the TUI status screen")
	demo := flag.Bool("demo", false, "TUI with mock toggle keys (b l r 1-5), no Bluetooth — try the screen without hardware")
	flag.Parse()

	ensureConfig(*configPath)
	cfg, err := config.Load(*configPath, profile)
	level := cfg.Level
	if verbose {
		level = slog.LevelDebug
	}
	// Tee to the -log file when asked: same TextHandler, same level and
	// records on both writers. Handler-internal locking keeps concurrent
	// session goroutines from interleaving lines; writes are unbuffered, so
	// no flush/close handling is needed.
	//
	// TUI mode (default) draws on the terminal, so the logger goes to the file
	// only (default zwiftboard.log). Fatal startup errors are the exception: they
	// use errLog (stderr) so they stay visible without the screen.
	tuiMode := !*plain || *demo
	if tuiMode && *logPath == "" {
		*logPath = "zwiftboard.log"
	}
	logBuf := tui.NewLogBuffer(200)
	var w io.Writer = os.Stderr
	if *logPath != "" {
		f, ferr := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if ferr != nil {
			slog.Error("open log file", "path", *logPath, "error", ferr)
			os.Exit(1)
		}
		if tuiMode {
			// File + the in-app log panel (never stderr: the screen owns it).
			w = io.MultiWriter(f, logBuf)
		} else {
			w = io.MultiWriter(os.Stderr, f)
		}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})))
	errLog := slog.Default() // -plain: already on stderr and in the file
	if tuiMode {
		errLog = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if err != nil {
		errLog.Error("bad config", "error", err)
		os.Exit(1)
	}
	if cfg.Missing {
		slog.Warn("no key mapping — button logging only", "file", *configPath)
	} else {
		slog.Info("profile loaded", "profile", cfg.Profile, "bindings", len(cfg.Bindings))
		for _, name := range config.ButtonNames() {
			if b, ok := cfg.Bindings[name]; ok {
				slog.Info("key mapping", "button", name, "key", b.Token)
			}
		}
	}
	if cfg.FocusProgramNameOnClick == "" {
		slog.Info("focus program on click disabled — keys go to the focused window")
	} else {
		slog.Info("focus program on click", "program", cfg.FocusProgramNameOnClick)
	}
	keys.SetWindowTarget(cfg.FocusProgramNameOnClick)

	if tuiMode {
		// Validate -addr now so a typo is reported on stderr, not lost in the
		// log file behind the full-screen UI.
		if *addrList != "" {
			for _, s := range strings.Split(*addrList, ",") {
				if _, err := ble.NewAddress(strings.TrimSpace(s)); err != nil {
					errLog.Error("bad -addr", "value", s, "error", err)
					os.Exit(1)
				}
			}
		}
		if *demo {
			runTUI(tuiConfig(cfg, true, logBuf, *configPath), nil)
		}
		runTUI(tuiConfig(cfg, false, logBuf, *configPath), func(p *tea.Program) { liveFeed(p, cfg, scanFor, reconnect, addrList) })
	}

	// -plain: Bluetooth must be on. Enable fails (or panics, absorbed by Guarded) while
	// the PC's radio is off; instead of exiting, say what to do and keep
	// retrying so the app proceeds the moment the radio is switched on.
	for enabled := false; !enabled; {
		ble.Guarded("enable BLE adapter", func() {
			if err := ble.Enable(); err != nil {
				slog.Error("Bluetooth is OFF or unavailable — turn it ON (Windows: Settings > Bluetooth & devices); retrying in 5s", "error", err)
			} else {
				enabled = true
			}
		})
		if !enabled {
			time.Sleep(5 * time.Second)
		}
	}
	slog.Info("Bluetooth is on — switch on BOTH Click controllers (LEFT and RIGHT); both are connected and kept alive together")

	startListening(cfg, scanFor, reconnect, addrList)
	if *addrList == "" {
		go pairStatus()
	}
	slog.Info("running — Ctrl+C to quit")
	select {}
}

// startListening starts the scanner and one reconnecting session goroutine per
// controller. Moved out of main verbatim so the TUI and -plain paths share the
// exact same BLE behavior.
func startListening(cfg config.Config, scanFor, reconnect *time.Duration, addrList *string) {
	// Registry of controllers we already have a session goroutine for.
	var regMu sync.Mutex
	registry := map[string]bool{}
	registered := func(addr string) bool {
		regMu.Lock()
		defer regMu.Unlock()
		return registry[addr]
	}

	// add starts one reconnecting session goroutine per controller.
	add := func(t ble.Target) {
		regMu.Lock()
		if registry[t.Addr.String()] {
			regMu.Unlock()
			return
		}
		registry[t.Addr.String()] = true
		n := len(registry)
		regMu.Unlock()
		if n == 1 {
			// Right-only build: a lone RIGHT pod is expected; its LED may keep
			// blinking (the LEFT anchor pod is unused) — cosmetic, not a fault.
			slog.Info("controller connected — right pod supported (Y/Z/A/B/+); a lone right pod's LED may keep blinking (cosmetic)", "controller", t.Label)
		} else if n >= 2 {
			// Extra controllers may still be discovered, but only the right
			// pod's buttons are mapped (the LEFT pod is unsupported).
			slog.Info("more than one controller connected — only the right pod's buttons are mapped (left pod has just to stay on)", "controllers", n)
		}
		go func() {
			key := t.Addr.String()
			waiting := false
			for {
				if ble.SeenRecently(key, *reconnect) {
					waiting = false
					var err error
					// ble.Guarded turns a panic in one controller's session into a
					// warn + retry, so a single bad event never kills the process.
					ble.Guarded("session "+t.Label, func() { err = ble.Session(t, cfg.Bindings) })
					if err != nil {
						slog.Warn("session ended", "controller", t.Label, "error", err)
						// The link is down — require a sighting newer than the
						// drop before reconnecting. The pod may have advertised
						// all session (keeping the gate open), so a stale gate
						// would retry the now-sleeping pod by address and leak
						// GattSessions again; clearing it makes the reconnect
						// wait for the pod's own fresh advertisement.
						ble.ClearSighting(key)
						// Give the drop a moment rather than hot-looping the gate.
						time.Sleep(5 * time.Second)
					}
					continue
				}
				// The scan gate is closed. Connect only to a controller the
				// scanner has seen advertising within the window: a right pod
				// sleeps ~65s after its last button but keeps its BLE connection
				// slot busy, and calling Connect() on a sleeping pod by address
				// leaks a Windows GattSession (SetMaintainConnection=true) per
				// attempt — hundreds during an outage wedge the BLE stack and
				// made reconnects stall ~54 minutes. On a fresh sighting the pod
				// is awake, the connect succeeds, and nothing leaks.
				if !waiting {
					waiting = true
					slog.Info("controller asleep — waiting for it to advertise (right pod sleeps ~65s after the last button; a button press or a ~40s radio re-awake brings it back); no reconnect attempts while it sleeps", "controller", t.Label)
				}
				time.Sleep(time.Second)
			}
		}()
	}

	// Watch ALWAYS runs: its scan is what feeds the reconnect gate (a controller
	// must be seen advertising before its session goroutine may connect — see
	// bluetoothSessionLoop). In -addr mode the gate feed is its only job: known
	// is always true, so nothing found is ever "new" and no stray session
	// goroutine is spawned — the app still manages exactly the listed addresses,
	// and each one still needs to be seen advertising (the pod's radio wakes
	// within ~30-60s of a sleep) before its connect happens.
	if *addrList != "" {
		for _, s := range strings.Split(*addrList, ",") {
			s = strings.TrimSpace(s)
			addr, err := ble.NewAddress(s)
			if err != nil {
				slog.Error("bad -addr", "value", s, "error", err)
				os.Exit(1)
			}
			add(ble.Target{Addr: addr, Label: "zwift/" + zwift.Tail(s)})
		}
		// Watch guards each iteration itself (recoverLog "watch iteration"
		// inside the loop): the old recover here was dead code — a nested
		// `go ble.Watch` made the outer defer unreachable.
		go ble.Watch(*scanFor, func(string) bool { return true }, func(ble.Target) {})
	} else {
		go ble.Watch(*scanFor, registered, add)
	}
}

// pairStatus logs the pair state whenever it changes: per side, "not detected",
// "detected" (advertising, session pending) or "connected". Purely
// observational — nothing is gated on it, so it can never stall a session.
func pairStatus() {
	state := func(p zwift.Pod) string {
		switch {
		case ble.Connected(p):
			return "connected"
		case ble.SideSeenRecently(p, 30*time.Second):
			return "detected"
		default:
			return "not detected"
		}
	}
	last := ""
	for {
		l, r := state(zwift.PodLeft), state(zwift.PodRight)
		cur := "left " + l + ", right " + r
		if cur != last {
			last = cur
			switch {
			case l == "connected" && r == "connected":
				slog.Info("ready — both controllers connected, listening", "state", cur)
			case l == "not detected" || r == "not detected":
				slog.Info("waiting — switch on both controllers (press a button to wake a sleeping pod)", "state", cur)
			default:
				slog.Info("both controllers detected — connecting", "state", cur)
			}
		}
		time.Sleep(2 * time.Second)
	}
}
