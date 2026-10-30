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
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"zwiftboard/internal/ble"
	"zwiftboard/internal/config"
	"zwiftboard/internal/keys"
	"zwiftboard/internal/tui"
	"zwiftboard/internal/unlock"
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
	// A broken config file is recoverable, not a crash: with a TUI and no -demo
	// it asks the user (single confirmable yes/no) to back it up and start from
	// the built-in default. The default config.yaml ships valid profiles, so a
	// restore always yields a usable config. -plain and -demo have no recovery
	// dialog: they keep failing fast (the user must fix the file themselves).
	recoverNeeded := err != nil && !cfg.Missing
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
	// A broken config file is recoverable in the status screen: the recovery
	// dialog backs the file up and restores the shipped default on a yes. The
	// -plain and -demo paths have no dialog, so they still fail fast.
	canRecover := recoverNeeded && tuiMode && !*demo
	if err != nil {
		if !canRecover {
			errLog.Error("bad config", "error", err)
			os.Exit(1)
		}
		slog.Warn("config failed to load — the status screen offers to restore the built-in default", "file", configFile, "error", err)
	}
	if cfg.Missing {
		slog.Warn("no key mapping — button logging only", "file", *configPath)
	} else if err != nil {
		slog.Warn("config unusable — button logging only until it is fixed or restored", "file", configFile)
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

	// The Zwift session lives beside config.yml. A missing or rejected token
	// only costs the LEFT pod: everything else works without it, and the TUI
	// offers sign-in on demand via the status-screen "z" (or when the session expired).
	var client *unlock.Client
	var authErr error
	if !*demo {
		client = unlock.New(unlock.NewStore(filepath.Dir(configFile)))
		authErr = client.Load()
		switch {
		case authErr == nil:
			slog.Info("Zwift sign-in found — the LEFT controller's time-limited unlock will be renewed")
		case errors.Is(authErr, unlock.ErrNoToken):
			slog.Info("no Zwift sign-in yet — the LEFT controller's time-limited unlock cannot be renewed until you sign in; press z to sign in")
		default:
			slog.Warn("Zwift sign-in file unreadable — press z to sign in again", "error", authErr)
		}
	}

	if tuiMode {
		if *demo {
			runTUI(tuiConfig(cfg, true, logBuf, *configPath), nil)
		}
		km := ble.NewKeyMap(cfg.Bindings)
		names, _ := config.ProfileList(*configPath)
		state := &profileState{path: *configPath, names: names, idx: indexOf(names, cfg.Profile), cur: cfg, km: km}
		idx := vkIndex(cfg.Bindings)
		state.vkIdx.Store(&idx)
		auth := tuiAuth(client, authErr, func() {
			// A successful sign-in renews the LEFT pod's time-limited unlock:
			// recycle its live session so it re-challenges and gets the
			// server-signed reply. The session tears down through its normal
			// probe-before-disconnect teardown, clears its sighting, waits
			// for a fresh advertisement, and reconnects with the fresh-token
			// solver. The RIGHT pod is never ended by a sign-in.
			ble.EndSessionsForSide(zwift.PodLeft)
		})
		tcfg := tuiConfig(cfg, false, logBuf, *configPath)
		tcfg.Profiles = names
		tcfg.ProfileIndex = state.idx
		tcfg.SwitchProfile = state.switchBy
		tcfg.Reload = state.reload
		tcfg.LaunchProgram = state.launchCurrent
		tcfg.Auth = auth
		if canRecover {
			// The screen opens on the recovery dialog: the broken file is named,
			// backed up and replaced by the shipped default on a yes, left alone
			// on a no. A yes also swaps the live key map and profile state so BLE
			// types the restored profile's keys right away.
			tcfg.NoMapping = true // until restored: no usable bindings
			tcfg.RecoveryPath = configFile
			tcfg.RecoveryErr = err.Error()
			tcfg.RecoveryStart = func(path string) (tui.Config, bool) {
				rebuilt, ok := recoverConfig(path)
				if !ok {
					return tui.Config{}, false
				}
				restoredNames, restoredIdx := state.reset(rebuilt)
				next := tuiConfig(rebuilt, false, logBuf, path)
				next.Profiles = restoredNames
				next.ProfileIndex = restoredIdx
				next.SwitchProfile = state.switchBy
				next.Reload = state.reload
				next.LaunchProgram = state.launchCurrent
				next.Auth = auth
				return next, true
			}
		}
		// No start-up gate: the screen opens immediately and sign-in is
		// always on-demand via "z". BLE starts right away with the current
		// solver (nil without a stored session, the client once signed in).
		runTUI(tcfg, func(p *tea.Program) {
			state.mu.Lock()
			state.prog = p
			state.mu.Unlock()
			liveFeed(p, state, km, func() ble.ChallengeSolver {
				return solverFor(client)
			})
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
	if solverFor(client) == nil {
		slog.Warn("LEFT controller unlock: no Zwift sign-in — only the RIGHT controller is guaranteed to work (press z on the TUI status screen to sign in)")
	}
	startListening(km, scanFor, ble.ReconnectWindow, func() ble.ChallengeSolver {
		return solverFor(client)
	})
	slog.Info("running — Ctrl+C to quit")
	select {}
}

// startListening starts the scanner and one reconnecting session goroutine per
// controller. Moved out of main verbatim so the TUI and -plain paths share the
// exact same BLE behavior.
func startListening(km *ble.KeyMap, scanFor, reconnect time.Duration, solver func() ble.ChallengeSolver) {
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
					ble.Guarded("session "+t.Label, func() { err = ble.Session(t, km, solver()) })
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
