# zwiftboard

**Your Zwift Click V2 controllers, as a keyboard remote for ANY PC cycling app.**

[![Go](https://img.shields.io/badge/go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Platform](https://img.shields.io/badge/platform-Windows%2010%2F11-blue)](#quick-start)
[![CI](https://img.shields.io/badge/CI-GitHub%20Actions-green)](.github/workflows)

Zwift Click V2 is an inexpensive pair of wireless BLE controllers — but they only
pair with the Zwift app. `zwiftboard` listens to them directly over Bluetooth,
decodes the button frames, and **types real keyboard keys on Windows**. Any app
that understands the keyboard works: MyWhoosh, Zwift, Rouvy, TrainerRoad,
Wahoo SYSTM, and whatever comes next.

- No Zwift account, no Zwift app, no 24h controller unlock — just the controllers and your PC's Bluetooth.
- Buttons map to keys through named profiles in a YAML file (5 platforms pre-configured).
- Runs as a small tray-less console app: start it, press controllers buttons, ride.

---

## Quick start

1. **Download** the latest release: grab `zwiftboard-windows-amd64.exe` and
   `config.yaml` from the [Releases](../../releases) page. Put both in the same
   folder.
2. **Close Zwift / the Companion app** — each controller accepts only one BLE
   connection at a time.
3. **Run it** and press any button on each controller while it scans:

   ```
   zwiftboard.exe                 # default profile: mywhoosh
   zwiftboard.exe -p zwift        # pick another profile
   zwiftboard.exe -v              # debug logging (raw frames, services)
   ```

   ```
   time=... level=INFO msg="profile loaded" profile=mywhoosh bindings=10
   time=... level=INFO msg=scanning controller=...
   time=... level=INFO msg="key mapping" button=PLUS key=i
   time=... level=INFO msg="state=pressed" button=PLUS key=i
   ```

Press a button during a scan burst to wake a controller. Scanning never gives
up: a controller switched on 10 minutes later still gets picked up.

## Usage

```
zwiftboard [flags]
```

| Flag              | Default       | Meaning                                                              |
| ----------------- | ------------- | -------------------------------------------------------------------- |
| `-p`, `--profile` | `mywhoosh`    | Profile from `config.yaml` (unknown name errors with the list)       |
| `-config`         | `config.yaml` | Config file path (relative to cwd)                                   |
| `-scan`           | `10s`         | Length of one scan burst; bursts repeat until all controllers are up |
| `-addr`           | _(scanning)_  | Comma-separated BLE MACs — skip scanning, connect straight to these  |
| `-debounce`       | `200ms`       | Minimum gap between two taps of the same button (mirrored pair)      |
| `-ack`            | `true`        | Send `ff 04 00` to keep an unlocked LEFT controller unlocked         |
| `-v`              | `false`       | Force `debug` log level (raw frames, service list)                   |

Log level otherwise comes from `loglevel:` in `config.yaml`
(`debug` | `info` | `warn` | `error`, default `info`).

### Included profiles

| Profile       | Click becomes…                                             | Source                                                                                                              |
| ------------- | ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `mywhoosh`    | I/K gears, space power-up, esc pause, tab camera, u U-turn | [MyWhoosh shortcuts](https://mywhooshinfo.com/blog/mywhoosh-keyboard-shortcuts)                                     |
| `zwift`       | arrows steer/U-turn, space power-up, pgup/pgdn FTP bias    | [Zwift shortcuts](https://support.zwift.com/en_us/keyboard-shortcuts-rkGrgwd4B)                                     |
| `rouvy`       | `.`/`,` gears, space pause, k kudos, e ERG                 | [Rouvy mapping](https://support.rouvy.com/hc/en-us/articles/47742964491665-Remote-controllers-and-control-mapping)  |
| `trainerroad` | arrows intensity/resistance, space pause, t mode           | [TrainerRoad shortcuts](https://support.trainerroad.com/hc/en-us/articles/202806120-TrainerRoad-Keyboard-Shortcuts) |
| `systm`       | up/down intensity, `` ` `` ERG, m mute                     | [SYSTM shortcuts](https://support.wahoofitness.com/hc/en-us/articles/4402734450322-Keyboard-Shortcuts)              |

Button names: `LEFT` `UP` `RIGHT` `DOWN` (left module), `A` `B` `Y` `Z`
(right module), `MIN` `PLUS`. Left module = arrows + minus, right module =
Y/Z/A/B + plus.

## Configuration

`config.yaml` sits next to the executable (or pass `-config`). One file:
a global `loglevel:` plus a `profiles:` map.

```yaml
loglevel: info # debug | info | warn | error

profiles:
  mywhoosh: # pick with -p mywhoosh
    focusProgramNameOnClick: null # optional, per profile (see below)
    PLUS: i # gear up
    MIN: k # gear down
    A: space # power-up
    B: esc # pause menu
    Y: u # U-turn
    Z: tab # camera

  my-zwift-setup: # add your own — any name works
    focusProgramNameOnClick: ZwiftApp
    A: space
    B: esc
```

**Key tokens:** `a-z`, `0-9`, `f1`–`f12`, named keys
(`up down left right enter space tab esc backspace delete insert home end
pageup pagedown shift ctrl alt capslock`), and single punctuation
``- = , . / ; ' [ ] ` \`` (quote punctuation that is also YAML syntax, as
above).

If the file is missing, zwiftboard still runs and logs every press — you just
don't get keystrokes.

### Focus a program on click

By default keys go to whichever window has focus, like a real keyboard. To make
every click land in the cycling app even if another window stole focus, tell
zwiftboard which program to bring forward first — per profile, since each game
window is different:

```yaml
profiles:
  mywhoosh:
    focusProgramNameOnClick: null        # disabled (default): keys → focused window
    focusProgramNameOnClick: MyWhoosh    # match by window title: "MyWhoosh"
  zwift:
    focusProgramNameOnClick: ZwiftApp    # or by .exe name (no path / .exe)
```

The value matches the window title (case-insensitive contains) or the program
`.exe` name. On each button press the matching window is restored (if
minimized) and brought to the foreground, then the key is tapped into it. If
no window matches, the tap is **dropped and a warning logged** — zwiftboard
never types into an unrelated app. Windows remembers the focused window, so
the game stays in front for the whole ride.

## How it works

```
┌────────────────────────────┐        ┌───────────────────────────────────────────┐
│   Zwift Click V2 pair      │        │              Windows PC                   │
│                            │        │                                           │
│   LEFT            RIGHT    │  BLE   │  zwiftboard.exe                           │
│   [arrows -]  [Y Z A B +]  │◄──────►│   1. scan bursts (Zwift vendor filter)    │
│                            │  GATT  │   2. connect + handshake, keepalive 2s    │
│   frame 0x23:              │ notify │   3. decode protobuf bitmap (0=pressed)   │
│   protobuf bitmap,         │ write  │   4. global debounce (pair MIRRORS        │
│   0 = pressed              │ indic. │      every press on both units)           │
│                            │        │   5. keys.Tap → focus target window, │
│                            │        │      then user32 keybd_event         │
└────────────────────────────┘        └───────────────────┬───────────────────────┘
                                                          │ simulated keystroke
                                        ┌─────────────────▼───────────────────────┐
                                        │  foreground app: MyWhoosh, Zwift,       │
                                        │  Rouvy, TrainerRoad, SYSTM, ...         │
                                        └─────────────────────────────────────────┘
```

1. **Scan** — endless 10s bursts (3s gap), filtered by Zwift's manufacturer ID
   `0x094A` or a `zwift*` name. `-addr` bypasses scanning entirely.
2. **Connect** — WinRT GATT, with a retry loop because Windows returns a
   partial service list right after connect. Characteristics are matched by
   UUID across _all_ services: Async `…0002` (notify), SyncRX `…0003` (write),
   SyncTX `…0004` (indicate) of service `00000001-19ca-4651-86e5-fa29dcdd09d1`.
3. **Handshake** — the ZwiftBridge activation trio (`RideOn 02 03`, `00 08 00`,
   `00 08 10`) then a `00 08 10` keepalive every 2s; without it the RIGHT
   controller deep-sleeps after ~56s and writes start failing.
4. **Decode** — button frames start with `0x23`; protobuf field 1 is a bitmap
   where **0 = pressed**. Bits: `LEFT 0x1 UP 0x2 RIGHT 0x4 DOWN 0x8 A 0x10
   B 0x20 Y 0x40 Z 0x80 MIN 0x100 PLUS 0x1000`.
5. **Tap** — the pair mirrors every press (both units send the same frame), so
   dedup is global per button name: a second claim within `-debounce`
   (200ms) is dropped as `duplicate=true`. If `focusProgramNameOnClick` is
   set, the configured window is brought to the foreground, then the key goes
   out via Windows `keybd_event`.

Each controller gets its own reconnecting session goroutine (5s backoff);
connects are serialized so they never overlap an active scan.

## Troubleshooting

| Symptom                                       | Fix                                                                                                                       |
| --------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| LEFT controller silent, logs `0xFF challenge` | Open Zwift once with the controller — LEFT stores a ~24h hardware unlock. `-ack=false` disables the ack if it misbehaves. |
| One press types the key twice                 | Raise `-debounce` (frames should mirror within tens of ms).                                                               |
| `found "" addr=D4:06:0F:…`                    | Normal — the advertisement carries no name; address is what matters.                                                      |
| Controller not found                          | Press any button to wake it during the scan burst; keep it awake.                                                         |
| Keys land in the wrong window                 | Set `focusProgramNameOnClick:` to the game's window title or `.exe` — zwiftboard brings it to the front and taps only into it. |
| App quits / stops responding after a while    | zwiftboard never exits on a runtime fault — a glitchy BLE event logs `recovered from panic` (a `WARN` with stack) and the session retry / scan loop keeps it alive. If buttons go silent instead, check the `session ended` lines: that is a lost connection recovering after 5s, not a crash. |
| Nothing works with Zwift open                 | Close Zwift / Companion first: one BLE connection per controller.                                                         |

Not a HID keyboard: Zwift itself won't see the Click as a Bluetooth keyboard
here — only the Windows foreground app receives the simulated keys.

## Development

Requires Go ≥ 1.27 (see `go.mod`). No BLE hardware needed to develop: tests
run on any OS, real scanning needs Windows.

```sh
go run .                          # run from source (Windows host)
go run . -v                       # debug: raw frames, service list

go test ./...                     # unit tests (run anywhere)
go vet ./...                      # static analysis
gofmt -l .                        # must print nothing
GOOS=windows go build ./...       # target build (also vets tap_windows.go)
```

Layout — a single binary, entry point at the repo root:

```
main.go               flags, logger setup, target registry, wiring
internal/zwift/       protocol facts: GATT UUIDs, button bits, frame decode
internal/ble/         scan (Watch), connect (Session), decode, tap dedup
internal/keys/        config token → VK code; Tap (keybd_event on Windows)
internal/config/      config.yaml: loglevel + profiles (returns errors)
```

See [AGENTS.md](AGENTS.md) for conventions and the hard-won protocol facts,
and [HANDOFF.md](HANDOFF.md) for the full debugging history and sources.

## Releases

CI runs `gofmt`, `go vet`, `go test`, and cross-builds Windows on
every push and pull request ([ci.yml](.github/workflows/ci.yml)).

Versions follow [semver](https://semver.org) and are generated from
[Conventional Commits](https://www.conventionalcommits.org) by
[release-please](https://github.com/googleapis/release-please)
([release.yml](.github/workflows/release.yml)): merging the automated release
PR creates the `vX.Y.Z` tag and a GitHub Release with:

- `zwiftboard-windows-amd64.exe`
- `config.yaml` (the reference profiles)

## Credits

Protocol reverse-engineering stands on the shoulders of
[ZwiftBridge](https://github.com/jimhoefnagels/ZwiftBridge) (handshake,
keepalive, button frames),
[qdomyos-zwift](https://github.com/cagnulein/qdomyos-zwift/pull/4743),
[Makinolo's Zwift Ride protocol notes](https://www.makinolo.com/blog/2024/07/26/zwift-ride-protocol/),
[RideToWoosh](https://github.com/p3dda/RideToWoosh) and
[zwiftplay](https://github.com/ajchellew/zwiftplay).
Keyboard shortcuts per game are taken from each vendor's official docs
(linked in [config.yaml](config.yaml)).
