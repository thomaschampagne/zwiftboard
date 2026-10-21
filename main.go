// Zwift Click V2 BLE listener for Windows (WinRT via tinygo.org/x/bluetooth).
//
//	go run . [-v] [-config path] [-p mywhoosh] [-log zwiftboard.log]
//
// Button presses are logged and, if config.yaml maps them, typed as real
// keyboard keys (Windows keybd_event). Log level comes from config.yaml
// (loglevel: debug|info|warn|error); -v forces debug.
//
// Click V2 is a pair of devices (LEFT: arrows + minus, RIGHT: Y/Z/A/B + plus).
// Click V2 is a PAIR: both pods are connected (each gets its own session and
// keepalive). Connecting only the right pod, or merely detecting the left one,
// does not stop the right pod's ~65s idle drop; with both connected the link
// stays up. Both pods' buttons are mappable (LEFT UP RIGHT DOWN MIN on the
// left pod, Y Z A B PLUS on the right); the shipped profiles map the right
// pod, the `notepad` profile maps both.
// Startup checks Bluetooth (asks for it to be switched on and retries), then
// asks for both controllers and reports the pair state as it changes.
// Scanning runs in bursts until controllers appear and keeps listening for
// new ones (a second controller may be turned on much later); it never gives
// up after the window. Each found controller gets a reconnecting
// session goroutine.
//
// Close Zwift / Companion first: each controller accepts a single BLE connection.
package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"zwiftboard/internal/ble"
	"zwiftboard/internal/config"
	"zwiftboard/internal/keys"
	"zwiftboard/internal/tui"
	"zwiftboard/internal/zwift"
)

//go:generate just gen-icon

// version is stamped at release time via -ldflags "-X main.version=...".
var version = "dev"

// Defaults after CLI flags were removed; kept for internal use. The reconnect
// window is ble.ReconnectWindow instead of a local copy, so the scan gate and
// the TUI's "detected" status share one value.
var scanFor = 10 * time.Second

func main() {
	configPath := flag.String("config", defaultConfigPath(), "YAML file with key mapping profiles (default: %LocalAppData%\\zwiftboard\\config.yml, created from the built-in default if missing; an explicit path must already exist)")
	var profile string
	flag.StringVar(&profile, "p", "", "config profile to use (default: defaultProfile from the config, else mywhoosh)")
	var verbose bool
	flag.BoolVar(&verbose, "v", false, "log raw frames and button events (forces log level debug)")
	logPath := flag.String("log", "", "also write log lines to this file (.log), truncated at startup")
	plain := flag.Bool("plain", false, "plain log output on stderr instead of the TUI status screen")
	demo := flag.Bool("demo", false, "TUI with mock toggle keys (b bluetooth, [ ] pods, f focus alert, 1-0 click), no Bluetooth — try the screen without hardware")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	// An explicit -config must point at a real file; the default path is
	// auto-created from the built-in default on first run.
	configExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			configExplicit = true
		}
	})
	configFile, err := resolveConfigFile(*configPath, configExplicit)
	if err != nil {
		slog.Error("config file not found — -config must point at an existing file", "path", *configPath, "error", err)
		os.Exit(1)
	}
	cfg, err := config.Load(configFile, profile)
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
	if cfg.FocusProgramNamePrefixOnClick == "" {
		slog.Info("focus program on click disabled — keys go to the focused window")
	} else {
		slog.Info("focus program on click", "program", cfg.FocusProgramNamePrefixOnClick)
	}
	if cfg.StartProgram != "" {
		slog.Info("start program configured", "program", cfg.StartProgram)
	}
	keys.SetWindowTarget(cfg.FocusProgramNamePrefixOnClick)

	if tuiMode {
		if *demo {
			runTUI(tuiConfig(cfg, true, logBuf, *configPath), nil)
		}
		km := ble.NewKeyMap(cfg.Bindings)
		names, _ := config.ProfileList(*configPath)
		state := &profileState{path: *configPath, names: names, idx: indexOf(names, cfg.Profile), cur: cfg, km: km}
		idx := vkIndex(cfg.Bindings)
		state.vkIdx.Store(&idx)
		tcfg := tuiConfig(cfg, false, logBuf, *configPath)
		tcfg.Profiles = names
		tcfg.ProfileIndex = state.idx
		tcfg.SwitchProfile = state.switchBy
		tcfg.Reload = state.reload
		tcfg.LaunchProgram = state.launchCurrent
		runTUI(tcfg, func(p *tea.Program) {
			state.mu.Lock()
			state.prog = p
			state.mu.Unlock()
			liveFeed(p, cfg, state, km)
		})
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
	launchProfile(cfg)

	km := ble.NewKeyMap(cfg.Bindings)
	startListening(km, scanFor, ble.ReconnectWindow)
	slog.Info("running — Ctrl+C to quit")
	select {}
}

// startListening starts the scanner and one reconnecting session goroutine per
// controller. Moved out of main verbatim so the TUI and -plain paths share the
// exact same BLE behavior.
func startListening(km *ble.KeyMap, scanFor, reconnect time.Duration) {
	// Registry of controllers we already have a session goroutine for. Each
	// goroutine releases its own entry once its controller has been gone for
	// registryIdleTTL (see sessionRegistry), so nothing leaks.
	reg := newSessionRegistry()

	// add starts one reconnecting session goroutine per controller.
	add := func(t ble.Target) {
		key := t.Addr.String()
		isFirst, n := reg.register(key)
		if !isFirst {
			return
		}
		// The pod side comes from the advertisement's Zwift manufacturer
		// record, so the log names it instead of guessing from the
		// discovery order (the first controller found is often the LEFT one).
		if n == 1 && t.Side == zwift.PodRight {
			// A lone RIGHT pod keeps blinking (advertising) — cosmetic, not a fault.
			slog.Info("controller connected — a lone right pod's LED may keep blinking (cosmetic)", "controller", t.Label, "pod", t.Side.String())
		} else {
			// n counts the controllers with a session loop, not the ones
			// connected right now: a loop outlives its pod by registryIdleTTL.
			slog.Info("controller connected", "controller", t.Label, "pod", t.Side.String(), "sessions", n)
		}
		go func() {
			waiting := false
			lastSeen := time.Now() // add fires on a fresh sighting
			for {
				if ble.SeenRecently(key, reconnect) {
					waiting = false
					var err error
					// ble.Guarded turns a panic in one controller's session into a
					// warn + retry, so a single bad event never kills the process.
					ble.Guarded("session "+t.Label, func() { err = ble.Session(t, km) })
					// Session blocks while connected; reset the idle clock at the
					// drop so a long connected run never looks "unseen" here.
					lastSeen = time.Now()
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
						lastSeen = time.Now()
					}
					continue
				}
				// Give up on a controller that has been gone for registryIdleTTL:
				// release its slot and exit, so neither the registry nor this 1Hz
				// loop accumulates for a controller that never returns. A later
				// sighting re-registers it and starts a fresh loop. The owner is
				// already leaving, so no second loop can own the address.
				if idleFor(lastSeen, time.Now()) {
					reg.forget(key)
					slog.Info("controller not seen for a while — releasing its reconnect slot (it is picked up again when it advertises)", "controller", t.Label)
					return
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

	// Watch ALWAYS runs: its scan feeds the reconnect gate (a controller must be
	// seen advertising before its session goroutine may connect). A fresh
	// sighting means the pod is awake, the connect succeeds, and nothing leaks.
	go ble.Watch(scanFor, reg.seen, add)
}
