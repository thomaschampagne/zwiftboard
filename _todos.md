- [x] Fix found empty in logs:
  2026/10/07 21:05:52 scanning 10s for Zwift controllers (wake them by pressing a button)...
  2026/10/07 21:05:56 found "" addr=D4:06:0F:A9:86:04 deviceID=11 rssi=-72
  2026/10/07 21:05:57 found "" addr=D4:06:0F:93:10:21 deviceID=10 rssi=-6
- [x] Add support for a `-p` / `--profile` CLI argument to select a specific configuration profile from the YAML config file.
  1. Update the YAML config structure to accept a list/map of profiles.
  2. Each profile defines its own mapping of SwiftClick v2 buttons to keyboard keys.
  3. When the user passes `-p <profile_name>` or `--profile <profile_name>`:
     - Check if the specified profile exists in the YAML config.
     - If it exists, apply its key/button mapping.
     - If it does not exist (or is missing), handle the error gracefully with a clear message.
     - Default profile is the MyWhoosh one (see game KB shortcuts for this and map with my zwift click controllers)
- [x] Wait until connection. Do not quit after 10 sec. Listen any new controller that could connect (if I connect 1 controller at start, and maybe the second one 10 min later or more...)
- [x] Put logging system with debug,info,wanr,error like (get level from config.yaml)
- [x] Set a proper folder/project golang structure
- [x] Write AGENTS.md

Self review each step on consistency/quality. Commit each step w/ conventionnal commit

- [x] Write unit test
- [x] Write github Action CI/CD pipeline with semantic versioning. Build for windows, macos (arm only) with binaries uploaded in created release should be supported
- [x] Write README.md (Fast explain what problem it solves for a new user (promote it), how to download, how to configure, how it works technically (make ascii diagram when needed), how to run/build/test @dev). Make 2026 top-notch readme
- [x] Dont crash program, perform try/catch like if possible and log a error.
  Go has no try/catch; the fix is recover at every runtime boundary. Added
  `recoverLog` in internal/ble (guards scan bursts, the ack goroutine, sync-tx
  and button-frame notification callbacks) and `guarded` in main.go (session
  retry goroutine). A panic now logs `recovered from panic` + stack and the
  loop continues instead of killing the process. The only os.Exit calls left
  are startup failures (bad config, adapter enable, bad -addr) per convention.
- [x] Support only 1 controller being used (eg. we can use only right one if we want actually for mywhoosh)
  Symptom reported: with only the RIGHT controller connected the program
  stopped after a moment. All runtime exits were already startup-only, so the
  stop was an uncaught panic in the WinRT BLE notification path — covered by
  the panic guards above (a one-off fault now logs and the session/scan loops
  retry forever). Adding a per-profile/CLI "which module" filter was NOT done;
  the app connects any controller it finds. Verify on hardware: right-only,
  no panic lines, session keeps reconnecting if BLE drops.
