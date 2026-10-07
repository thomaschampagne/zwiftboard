// Zwift Click V2 BLE listener for Windows (WinRT via tinygo.org/x/bluetooth).
//
//	go run . [-v] [-scan 10s] [-addr D4:06:0F:A9:86:04,...] [-config config.yaml] [-p mywhoosh] [-ack=true]
//
// Button presses are logged and, if config.yaml maps them, typed as real
// keyboard keys (Windows keybd_event). Log level comes from config.yaml
// (loglevel: debug|info|warn|error); -v forces debug.
//
// Click V2 is a pair of devices (LEFT: arrows + minus, RIGHT: Y/Z/A/B + plus)
// that work as ONE pair: the LEFT pod is the BLE anchor and the RIGHT pod
// mirrors its state to the LEFT over a private RF link. Both pods are used —
// connect them both; with only the RIGHT pod connected its LED keeps blinking
// (advertising mode) instead of going solid.
// Scanning runs in bursts until controllers appear and keeps listening for
// new ones (a second controller may be turned on much later); it never gives
// up after the -scan window. Each found controller gets a reconnecting
// session goroutine.
//
// Close Zwift / Companion first: each controller accepts a single BLE connection.
package main

import (
	"flag"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"zwiftboard/internal/ble"
	"zwiftboard/internal/config"
	"zwiftboard/internal/keys"
	"zwiftboard/internal/zwift"
)

// guarded runs fn, logging a panic (with stack) instead of crashing, so a
// single bad controller event never takes the process down. Keep at top level
// (not inside main) to stay reachable from goroutine closures.
func guarded(where string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("recovered from panic", "where", where, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	fn()
}

func main() {
	scanFor := flag.Duration("scan", 10*time.Second, "length of one scan burst; scanning repeats until controllers are found and keeps listening for new ones")
	addrList := flag.String("addr", "", "comma-separated BLE addresses (e.g. D4:06:0F:A9:86:04) — skips scanning")
	configPath := flag.String("config", "config.yaml", "YAML file (cwd) with key mapping profiles")
	var profile string
	flag.StringVar(&profile, "p", config.DefaultProfile, "config profile to use")
	flag.StringVar(&profile, "profile", config.DefaultProfile, "config profile to use (same as -p)")
	var verbose bool
	flag.BoolVar(&verbose, "v", false, "log raw frames and taps (forces log level debug)")
	flag.BoolVar(&ble.SendAck, "ack", true, "send ff 04 00 to devices that echo RideOn (keeps unlock)")
	flag.DurationVar(&ble.TapDebounce, "debounce", 200*time.Millisecond, "minimum gap between two taps of the same button")
	flag.Parse()

	cfg, err := config.Load(*configPath, profile)
	level := cfg.Level
	if verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	if err != nil {
		slog.Error("bad config", "error", err)
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

	// The Click V2 ships as two pods and works as a pair: the LEFT pod is the
	// BLE anchor, the RIGHT pod mirrors its state to the LEFT over a private RF
	// link. Both pods are used — with only the RIGHT pod connected its LED
	// keeps blinking (advertising mode) instead of going solid. Log it once so
	// a blinking LED is not mistaken for a fault.
	slog.Info("click pair: connect BOTH pods (LEFT + RIGHT) — LEFT is the pair's anchor; both pods together give a solid LED, a lone RIGHT pod keeps blinking")

	// Guarding Enable keeps the startup convention (error => logged Error +
	// non-zero exit) while absorbing a panic. When the PC's Bluetooth is off
	// Enable fails here and the process exits cleanly; next launch will rescan.
	ble.Guarded("enable BLE adapter", func() {
		if err := ble.Enable(); err != nil {
			slog.Error("enable BLE adapter", "error", err)
			os.Exit(1)
		}
	})

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
			// The Click V2 is a two-pod pair; the LEFT pod is the BLE anchor.
			// With only the RIGHT pod connected it stays in advertising mode
			// (LED keeps blinking) even though its frames decode fine, so tell
			// the user the pair isn't complete yet.
			slog.Info("one pod connected — Click V2 is a two-pod pair: connect the second pod too (LEFT is the pair's anchor; with only the RIGHT pod its LED keeps blinking)", "controller", t.Label)
		} else if n >= 2 {
			slog.Info("both pods connected — Click pair complete (LEDs go solid)", "controllers", n)
		}
		go func() {
			for {
				// guarded turns a panic in one controller's session into a
				// warn + retry, so a single bad event never kills the process.
				guarded("session "+t.Label, func() {
					if err := ble.Session(t, cfg.Bindings); err != nil {
						slog.Warn("session ended", "controller", t.Label, "error", err)
					}
				})
				time.Sleep(5 * time.Second)
			}
		}()
	}

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
	} else {
		// Watch loops forever with no recover of its own: a panic-escaping
		// scan (e.g. the PC's Bluetooth was switched off) would otherwise take
		// the whole process down. Guard the spawn so the loop can never crash it.
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Warn("recovered from panic", "where", "watch loop", "panic", r, "stack", string(debug.Stack()))
				}
			}()
			go ble.Watch(*scanFor, registered, add)
		}()
	}
	slog.Info("running — Ctrl+C to quit")
	select {}
}
