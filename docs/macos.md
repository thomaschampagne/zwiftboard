# macOS CLI

The macOS port uses CoreBluetooth through `tinygo.org/x/bluetooth` and Quartz
keyboard events. Build on macOS with the Go version in `go.mod`, Xcode Command
Line Tools (`xcode-select --install`), and `just`:

```sh
just ci-macos
./dist/zwiftboard
```

`CGO_ENABLED=1` is required. `just build-macos` builds the host architecture;
CI runs native Apple Silicon and Intel jobs. macOS release downloads, app
bundles, signing/notarization, automatic app launching, and window focusing
are not included in this change. Windows release assets and Scoop are unchanged.

## First run

1. Close Zwift and Companion; the controllers accept one BLE connection each.
2. Turn Bluetooth on and wake **both** Click V2 pods.
3. Run the built binary from a stable path, rather than `go run` (which creates
   a temporary executable). Allow Bluetooth access when macOS requests it.
4. In **System Settings > Privacy & Security > Accessibility**, allow the
   terminal application or `zwiftboard` as identified by macOS. Add the built
   executable using the `+` picker if needed, then restart the terminal/program.
   If approving the executable alone leaves input blocked, also check the
   terminal application used to launch it. In local testing, enabling Terminal
   and restarting the executable resolved this case.
5. Open TextEdit, create an empty document, and focus it. The initial `keyboard`
   profile maps PLUS=p, A=a, B=b, Y=y, Z=z. Both pods stay connected; only the
   right pod supplies these mapped buttons.

The config is created at `~/Library/Application Support/zwiftboard/config.yml`
(unless `LOCALAPPDATA` is explicitly set, preserving the existing config-path
behavior). New macOS configs select `keyboard`; existing or migrated configs
are never rewritten. Pass `-config /path/to/config.yml` to use an existing file,
`-p keyboard` to select that profile, or `-plain -v` for diagnostic logs.
An older config may not contain the new profile: add it from `config.yaml` or
clear focus/launch settings on a profile you already use.

The executable embeds `NSBluetoothAlwaysUsageDescription`. Accessibility is
checked without repeatedly prompting; controller logging remains available
when injection is denied. The status screen shows an input-blocked warning.
No Input Monitoring permission is requested by this implementation.

## Mappings and unsupported options

Config tokens and Windows virtual-key identifiers stay compatible. On macOS,
letters, digits, punctuation, navigation, F1–F12, and modifiers map to **US
physical keyboard positions**. Text on other layouts can differ. `ctrl` means
Control, not Command; `alt` means Option. `insert` has no supported mapping.
Physical modifiers are combined with controller modifiers. Caps Lock toggles
without repeat; other modifiers do not repeat. Ordinary keys repeat after
250 ms, then every 33 ms until release. These are repeated key-down events;
the receiving application decides whether they repeat an action. Text input
may also use macOS's accent menu instead of inserting repeated letters.

Set `focusProgramNamePrefixOnClick: null` to send input to the foreground app.
A nonempty focus target **blocks injection** on macOS; it is never silently
ignored. `startProgram` reports that launching is unsupported; open the target
application yourself. Keep it in focus while using the controller.

## MyWhoosh

Reuse the existing `mywhoosh` bindings with both
`focusProgramNamePrefixOnClick: null` and `startProgram: null`. Connect the
Click pods to zwiftboard and the trainer to MyWhoosh. Open MyWhoosh manually
and keep it focused during the ride.

The right-pod mappings are PLUS=K (gear down), B=I (gear up), A=right arrow,
Z=left arrow, and Y=U (hide/show menu in the tested version). Holding a shift
button changed gear once in local testing, matching a held physical I/K key.
Steering worked with MyWhoosh focused; no shift-specific repeated taps are
emulated.

## Physical results (2026-10-21)

User-observed results on Apple Silicon with a Click V2 pair:

- Both pods connected; all five mapped letters and simultaneous input worked
  in TextEdit. The initial P mapping passed hold/repeat/release testing.
- MyWhoosh shifting, left/right steering, and menu toggling worked.
- Input remained usable after ten minutes idle and after reconnecting.
- Turning Bluetooth off during a held right arrow stopped cursor movement
  when the disconnect was detected. After Bluetooth was restored and the pods
  reconnected, movement resumed while the button was still physically held.

Quit and profile reload/switch during a hold, modifier combinations, and
Bluetooth permission denial/recovery remain untested physically. Native Intel
and Windows execution and hosted CI remain pending. MyWhoosh version and pod
firmware were not recorded; these observations apply to the tested setup.

## Testing without a controller

`just integration-macos` exercises config loading, real 0x23 frame decoding,
notification gating, mirrored-pod deduplication, key resolution, and the Quartz
state machine together. It checks holds and repeat, final release, disconnect
with late frames, profile-map changes, shutdown, and blocked-focus recovery.
The controlled clock also fires cancelled callbacks to check stale-timer safety.
Both macOS CI architectures run this suite through `just ci-macos`.

Only the clock, permission response, physical modifier state, and final OS event
sink are replaced. The test bridge is compiled only with the `integration` tag;
normal executables do not expose it. Tests assemble session input components
without a Bluetooth transport: they do not test native discovery/handshakes,
the complete Session function, TUI orchestration, macOS event delivery, or
MyWhoosh. Physical validation remains necessary for those boundaries.

```sh
just ci-macos
python3 scripts/check-macos-metadata.py dist/zwiftboard
./dist/zwiftboard -version
./dist/zwiftboard -demo
```

Tests inject event sinks, clocks, connection state and write functions. They do
not post real keyboard events or explicitly enable/scan Bluetooth. The pinned
TinyGo dependency creates CoreBluetooth managers at import time, including in
tests and `-version`/`-demo`; macOS may therefore show a Bluetooth prompt even
though those modes perform no application-level scanning. No Accessibility
prompt is requested. This dependency behavior is not patched or vendored here.

TinyGo's macOS `Scan` also does not reliably report radio-off/permission errors;
the Bluetooth indicator reflects scan-call health and can remain on while the
radio is off. Startup errors include permission guidance. Sessions check native
connection state before writes, because successfully queuing a write without
response is not evidence that a controller is connected. If recovery does not
occur after re-enabling Bluetooth, restart and capture `-plain -v` logs.

## Physical validation checklist

Automated checks cannot establish controller firmware behavior, permission
ownership, application receipt, or game key-state polling. Record macOS version,
CPU architecture, pod firmware if known, and target application with results.

- Deny then grant Bluetooth; verify startup guidance and recovery.
- Deny then grant Accessibility; verify input stays blocked until authorized.
- Connect both pods; press all five mapped buttons and check for duplicate input.
- Tap, hold and release a letter in TextEdit; verify repeat stops on release.
- Test arrows and game polling, F keys, Control/Option/Shift, and Caps Lock;
  include a physical modifier held while pressing a controller button.
- Disconnect one pod during a hold, then both; ensure no permanently stuck key.
- Reload or switch profiles during a hold, including a blocked focus profile.
- Quit using `q`, Ctrl+C, and SIGTERM while holding a button; verify cleanup.
- Leave both pods idle for at least ten minutes; verify the next press works.
- Switch Bluetooth off/on and sleep/wake the pods; verify scan-gated reconnect.

An abrupt kill (`SIGKILL`), OS crash, or revoked Accessibility permission can
prevent cleanup events from reaching macOS. Report physical results separately
from build/unit-test results; Intel runtime requires an Intel machine.
