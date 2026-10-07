- Fix found empty in logs:
  2026/10/07 21:05:52 scanning 10s for Zwift controllers (wake them by pressing a button)...
  2026/10/07 21:05:56 found "" addr=D4:06:0F:A9:86:04 deviceID=11 rssi=-72
  2026/10/07 21:05:57 found "" addr=D4:06:0F:93:10:21 deviceID=10 rssi=-6
- Add support for a `-p` / `--profile` CLI argument to select a specific configuration profile from the YAML config file.
  1. Update the YAML config structure to accept a list/map of profiles.
  2. Each profile defines its own mapping of SwiftClick v2 buttons to keyboard keys.
  3. When the user passes `-p <profile_name>` or `--profile <profile_name>`:
     - Check if the specified profile exists in the YAML config.
     - If it exists, apply its key/button mapping.
     - If it does not exist (or is missing), handle the error gracefully with a clear message.
     - Default profile is the MyWhoosh one (see game KB shortcuts for this and map with my zwift click controllers)
- Wait until connection. Do not quit after 10 sec. Listen any new controller that could connect (if I connect 1 controller at start, and maybe the second one 10 min later or more...)
- Put logging system with debug,info,wanr,error like (get level from config.yaml)
- Set a proper folder/project golang structure
- Write AGENTS.md

Self review each step on consistency/quality. Commit each step w/ conventionnal commit

- Write unit test
- Write README.md
 
