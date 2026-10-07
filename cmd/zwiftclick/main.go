// Zwift Click V2 BLE listener for Windows (WinRT via tinygo.org/x/bluetooth).
//
//	go run ./cmd/zwiftclick [-v] [-scan 10s] [-addr D4:06:0F:A9:86:04,...] [-config config.yaml] [-ack=true]
//
// Button presses are logged and, if config.yaml maps them, typed as real
// keyboard keys (Windows keybd_event).
//
// Click V2 is a pair of devices (LEFT: arrows + minus, RIGHT: Y/Z/A/B + plus).
// This connects to every Zwift device it finds and logs button edges.
//
// Close Zwift / Companion first: each controller accepts a single BLE connection.
package main

import (
	"flag"
	"log"
	"strings"
	"sync"
	"time"

	"zwiftclickv2-keyboard/internal/ble"
	"zwiftclickv2-keyboard/internal/config"
	"zwiftclickv2-keyboard/internal/zwift"
)

func main() {
	scanFor := flag.Duration("scan", 10*time.Second, "how long to scan for Zwift controllers")
	addrList := flag.String("addr", "", "comma-separated BLE addresses (e.g. D4:06:0F:A9:86:04) — skips scanning")
	configPath := flag.String("config", "config.yaml", "YAML file (cwd) mapping buttons to keyboard keys")
	flag.BoolVar(&ble.Verbose, "v", false, "log raw frames and unknown bits")
	flag.BoolVar(&ble.SendAck, "ack", true, "send ff 04 00 to devices that echo RideOn (keeps unlock)")
	flag.DurationVar(&ble.TapDebounce, "debounce", 200*time.Millisecond, "minimum gap between two taps of the same button")
	flag.Parse()

	keyMap, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("%v", err)
	}

	if err := ble.Enable(); err != nil {
		log.Fatalf("enable BLE adapter: %v", err)
	}

	var targets []ble.Target
	if *addrList != "" {
		for _, s := range strings.Split(*addrList, ",") {
			s = strings.TrimSpace(s)
			addr, err := ble.NewAddress(s)
			if err != nil {
				log.Fatalf("bad -addr %q: %v", s, err)
			}
			targets = append(targets, ble.Target{Addr: addr, Label: "zwift/" + zwift.Tail(s)})
		}
	} else {
		targets = ble.Discover(*scanFor)
	}
	if len(targets) == 0 {
		log.Fatal("no Zwift controllers found — wake them (press a button), close Zwift, retry")
	}

	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t ble.Target) {
			defer wg.Done()
			for {
				if err := ble.Session(t, keyMap); err != nil {
					log.Printf("[%s] %v", t.Label, err)
				}
				time.Sleep(5 * time.Second)
			}
		}(t)
	}
	log.Println("running — Ctrl+C to quit")
	wg.Wait()
}
