// Zwift Click V2 BLE listener for Windows (WinRT via tinygo.org/x/bluetooth).
//
//	go run . [-v] [-scan 10s] [-addr D4:06:0F:A9:86:04,...] [-config config.yaml] [-p mywhoosh] [-ack=true]
//
// Button presses are logged and, if config.yaml maps them, typed as real
// keyboard keys (Windows keybd_event). Log level comes from config.yaml
// (loglevel: debug|info|warn|error); -v forces debug.
//
// Click V2 is a pair of devices (LEFT: arrows + minus, RIGHT: Y/Z/A/B + plus).
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
	"strings"
	"sync"
	"time"

	"zwiftboard/internal/ble"
	"zwiftboard/internal/config"
	"zwiftboard/internal/zwift"
)

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

	if err := ble.Enable(); err != nil {
		slog.Error("enable BLE adapter", "error", err)
		os.Exit(1)
	}

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
		regMu.Unlock()
		go func() {
			for {
				if err := ble.Session(t, cfg.Bindings); err != nil {
					slog.Warn("session ended", "controller", t.Label, "error", err)
				}
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
		go ble.Watch(*scanFor, registered, add)
	}
	slog.Info("running — Ctrl+C to quit")
	select {}
}
