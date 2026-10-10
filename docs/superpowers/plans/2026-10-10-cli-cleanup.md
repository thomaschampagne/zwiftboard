# BLE Flag Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove BLE tuning flags from the CLI and hard-code defaults in internal/ble, while preserving test coverage and keeping only user-meaningful flags (-p, -config, -v, -log, -plain, -demo, -version). The -profile alias is collapsed to -p only.

**Architecture:** Flag CLI surface shrunk; internal defaults become package-scope constants or remain as exported vars with hardcoded defaults; affected functions receive thresholds as parameters where needed; affected signatures updated; all internal tests updated to match.

**Tech Stack:** Go; internal/ble package (SendAck, IdleReset vars → consts or kept as exported vars with defaults); main.go CLI flags removed; README.md and AGENTS.md markdown updates.

**Spec:** User task 2 from _todo.md: "Cleanup cli options: only meaningful for user. Drop deprecated according current codebase." The spec references AGENTS.md hard-won facts and flag usage.

**Global Constraints:**
- go test ./... must pass (111 passed / 6 pkgs) after edits.
- go vet ./... clean.
- gofmt -l . must flag only pre-existing CRLF noise (no new warnings).
- GOOS=windows go build ./... must stay clean (required for tap_windows.go vet).
- internal/ble test `TestKeepaliveTick` must continue to pass; its IdleReset mutation pattern must be replaced with param-passing.
- No new exported package variables that expose knobs to external callers beyond what already exists.
- The `-profile` Go flag alias is removed; only `-p` remains; the `--profile` duplicate registration dropped.

--- 

### Task 1: internal/ble — change SendAck and IdleReset to unexported consts + parameterize keepaliveTick

**Files to modify:**
- `internal/ble/ble.go`:
  1. Change `var SendAck = true` → `const sendAck = true` (unexported). Update references: line 397 `if !SendAck` → `if !sendAck`; line 429 `if SendAck` → `if sendAck`.
  2. Change `var IdleReset = 55 * time.Second` → `const idleReset = 55 * time.Second` (unexported, typed `time.Duration`). Update reference in keepaliveTick: line 256 `if IdleReset > 0 ...` → `if idleReset > 0 ...`. And line 474 `IdleReset` → `idleReset`.
  3. Change keepaliveTick signature from `func keepaliveTick(resetSent bool, lastActivity, now time.Time) (reset, ping bool)` to `func keepaliveTick(resetSent bool, lastActivity, now time.Time, idleAfter time.Duration) (reset, ping bool)`. Function body uses `idleAfter` param instead of package var.
- `internal/ble/asyncnotify_test.go` (`TestKeepaliveTick`):
  1. Remove `saved := IdleReset; defer func() { IdleReset = saved }()` mutation pattern.
  2. Rewrite test body to pass thresholds directly to keepaliveTick: five assertions with `keepaliveTick(false, active, now, 55*time.Second)`, `keepaliveTick(false, idle, now, 55*time.Second)`, `keepaliveTick(true, idle, now, 55*time.Second)`, `keepaliveTick(false, idle, now, 0)` and corresponding want/failed asserts. Ensure test compiles and passes.

**Verification:**
- `go test ./internal/ble/...` — green. Specifically `go test -run TestKeepaliveTick` passes with the rewritten test body. `go test ./...` all ble tests pass.

### Task 2: main.go — drop -scan, -reconnect, -addr, -ack, -idle-reset flags + adjust signatures; remove -profile alias

**Files to modify:**
- `main.go`:
  1. Remove flag registrations for `-scan`, `-reconnect`, `-addr`, `-ack`, `-idle-reset`. Keep registrations: `-config`, `-p`, `-v`, `-log`, `-plain`, `-demo`, `-version`. Also remove the duplicate `-profile` registration (keep only `-p` with `config.DefaultProfile`).
  2. Replace the `scanFor` and `reconnect` vars derived from flags with package-level defaults (keep as `var` with hardcoded values commented "hardcoded default, CLI removed" — or simply use const literals passed wherever `*scanFor` / `*reconnect` was dereferenced). For minimal diff, keep `var scanFor = 10 * time.Second` and `var reconnect = 30 * time.Second` with comments, remove the `flag.Duration` lines. All downstream code (`startListening`, `liveFeed`, `ble.SeenRecently(key, *reconnect)`, `ble.Watch(*scanFor, ...)`) continues to use the same vars unchanged, so no further signature changes needed in startListening/liveFeed.
  3. Remove the `-addr` validation block (lines 128-137 in main.go: the `if *addrList != ""` loop with `ble.NewAddress` + error check). Also remove `strings` import if no longer used elsewhere (check: `strings` used at line 250 (`strings.TrimSpace`) and 251 (`strings.Split`) inside startListening addr branch; after removal, verify `strings` import can be removed; actually `strings` also used in main comment lines; let's keep import for now — but verify go tooling).
  4. Simplify `startListening` signature: the `addrList *string` param can be dropped since addr mode is gone. Change call site (line 161) from `startListening(cfg, scanFor, reconnect, addrList)` to `startListening(cfg, scanFor, reconnect)`. Inside startListening, always take the `else` branch (scanning mode) `go ble.Watch(*scanFor, registered, add)` (remove the `if *addrList != "" { ... } else { ... }` conditional).
  5. Simplify `liveFeed` signature: drop `scanFor`, `reconnect`, `addrList *string` params; change to `func liveFeed(p *tea.Program, cfg config.Config)`. Update call sites (lines 138-141) to pass `liveFeed(p, cfg)` instead of the curried form with extra args. The `cfg.FocusProgramNamePrefixOnClick` focus caching logic stays; `tui.Poll` source `src` still includes `ble.ScanHealthy`, `ble.Connected`, `ble.SideSeenRecently` — no change needed.
  6. Update the top-level doc comment (lines 3-4) from `go run . [-v] [-scan 10s] [-addr D4:06:0F:A9:86:04,...] [-config path] [-p mywhoosh] [-ack=true] [-log zwiftboard.log]` to `go run . [-v] [-config path] [-p mywhoosh] [-log zwiftboard.log]`. Also update the `-scan` and `-addr` mentions in the surrounding comment block (lines 16-19).
  7. Remove `-profile` duplicate flag var registration (lines 52-53). Keep only `flag.StringVar(&profile, "p", config.DefaultProfile, "config profile to use")`.
  8. Ensure `logPath`, `plain`, `demo`, `version` flags remain registered; their registrations unchanged.

**Verification:**
- `go test ./...` — green (111 passed / 6 pkgs). Confirm tuirun_test.go and defaultconfig_test.go still compile (they test editorCommand/vkIndex and migration; unaffected).
- `go vet ./...` — clean.
- `gofmt -l .` — expect only pre-existing CRLF noise; our edits must not introduce new formatting issues.
- `GOOS=windows go build ./...` — must stay clean.

**README.md updates:**
- Remove `-scan`, `-addr`, `-ack` rows from the Usage table (lines 56-58). Keep `-p`, `-config`, `-v`, `-log`, `-plain`, `-demo` rows.
- Add `-version` row for completeness (or leave if absent; the plan notes adding it as a user-friendly flag). The table currently lacks `-version`; add a row: `| `-version` | `false` | Print version and exit |`.
- Adjust `-p --profile` label to just `-p` (since `--profile` alias removed). Change line 54 from `| `-p`, `--profile` | `mywhoosh` …` to `| `-p` | `mywhoosh` …`.
- Fix line 82: remove or rewrite the `-addr` troubleshooting note ("`-addr` pod sides are unknown, so a pod shows 'not detected' until connected."). Since addr mode is gone, rewrite to omit or replace with a note about scanning.
- Fix line 182: troubleshooting section "`-addr` bypasses scanning entirely." Remove or replace with scanning-only note. Since we only have scanning now, rewrite that line to remove `-addr` reference.
- Ensure `-profile` alias text gone from any other README prose.

**AGENTS.md updates:**
- Line 90: "seen advertising within `-reconnect` (default 30s)" → "seen advertising within the reconnect window (default 30s)".
- Line 95: "`-idle-reset`, default 55s" → "(default 55s)" (since flag removed, it's now an internal constant).
- Line 109: "disable with `-ack=false`" → "always answered" (since `-ack` flag removed, ack now always on; keep text about `ff 04 00` keeps an unlocked device unlocked but note it's no longer user-configurable via flag).
- Add brief note in Layout or Hard-won facts that -scan, -reconnect, -addr, -ack, -idle-reset are now internal defaults only.
- Adjust any other flag references (e.g., line 84 paragraph mentions `-reconnect` — rewrite to remove the flag name; keep the mechanism description).

**Verification of markdown:**
- Run `go test ./...`.
- Ensure README renders correctly (no broken tables).
- Ensure AGENTS.md prose flows naturally.

**Commit:**
`refactor(cli): drop BLE tuning flags and -profile alias; clean CLI surface and docs`