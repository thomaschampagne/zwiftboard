# LEFT-unlock Zwift sign-in Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sign in to Zwift once from the TUI, persist the session beside `config.yml`, and answer the LEFT pod's `FF 03` challenge with the server-signed reply so its button engine stays unlocked.

**Architecture:** A new `internal/unlock` package owns the token store, Keycloak login/refresh and the d-lock challenge call. It is exposed to BLE through a one-method `ChallengeSolver` interface; `nil` keeps today's empty-ack behaviour. The TUI gains a start-up auth gate (check stored session → prompt on first run/expiry → skip), and `liveFeed` waits for the gate before scanning.

**Tech Stack:** Go 1.27, `log/slog`, `net/http`, Bubble Tea v2, `testing` + `httptest`, `os.WriteFile`/`rename` for the token file.

**Spec:** `docs/superpowers/specs/2026-10-22-left-unlock-signin-design.md`

## Global Constraints

- Never log, persist or display the email, password, access token or refresh token. Only the error *code* from Keycloak may be logged.
- Token file: `zwift-token.json` in the config directory, mode 0600, atomic write.
- `nil` solver must reproduce today's behaviour byte-for-byte (`ff 04 00` on challenge and echo, 15s periodic ack).
- Fall back to `ff 04 00` on every solve failure; never block the BLE notification goroutine.
- Challenge payload = the bytes after the fixed `FF 03 00` prefix.
- `-plain` has no prompt; it logs a warning and uses any stored token.
- One logical commit per task.

---

### Task 1: Token store (`internal/unlock/token.go`)

**Files:**
- Create: `internal/unlock/token.go`
- Test: `internal/unlock/token_test.go`
- Modify: `.gitignore`

**Interfaces:**
- Produces: `unlock.NewStore(dir string) *Store`, `(*Store).Path() string`,
  `(*Store).Load() (Tokens, error)`, `(*Store).Save(Tokens) error`,
  `(*Store).Delete() error`, `unlock.Tokens{AccessToken, RefreshToken string; ExpiresAt time.Time}`,
  const `unlock.TokenFile = "zwift-token.json"`.

- [ ] **Step 1: failing tests** — round-trip save/load; missing file is `fs.ErrNotExist`; saved file is mode 0600; `Save` creates a missing directory; `Delete` is idempotent; `.gitignore` contains `zwift-token.json`.
- [ ] **Step 2:** `go test ./internal/unlock -run TestStore` → FAIL (package missing).
- [ ] **Step 3:** implement `Tokens` (JSON tags `access_token`, `refresh_token`, `expires_at`), `Store`, atomic save (`os.CreateTemp` in the dir, chmod 0600, write, close, rename), `Delete` ignoring `fs.ErrNotExist`.
- [ ] **Step 4:** `go test ./internal/unlock` → PASS.
- [ ] **Step 5:** commit `feat(unlock): persist the Zwift session beside the config`.

### Task 2: Keycloak login + refresh (`internal/unlock/client.go`)

**Files:**
- Create: `internal/unlock/client.go`
- Test: `internal/unlock/client_test.go`

**Interfaces:**
- Produces: `unlock.New(store *Store) *Client`, `(*Client).Load() error`,
  `(*Client).Ready() bool`, `(*Client).Ensure(ctx) error`,
  `(*Client).Login(ctx, email, password string) error`; sentinels
  `unlock.ErrNoToken`, `unlock.ErrAuth`.
- Test seam: unexported `newClient(store, tokenURL, dlockURL string, hc *http.Client, now func() time.Time)`.

- [ ] **Step 1: failing tests**
  - `TestLoadMissingToken` → `ErrNoToken`.
  - `TestLoginPostsPasswordGrantAndStoresTokens`: httptest server asserts method POST, `grant_type=password`, `client_id=Zwift Game Client`, `username`, `password`; returns access/refresh/expires_in; token file exists and `Ready()` is true.
  - `TestLoginRejected` (400 `{"error":"invalid_grant"}`) → `errors.Is(err, ErrAuth)`, nothing stored.
  - `TestLoginNeverLogsCredentials`: capture `slog` output with a debug handler; assert it does not contain the email or password, and does not contain the form-encoded body.
  - `TestRefreshRotatesTokens`: stale `ExpiresAt`, server returns a new refresh token, `Ensure` stores it.
  - `TestRefreshRejected` → `ErrAuth`, `Ready()` false afterwards.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** implement. `tokenRequest` logs only `grant_type`, status, duration and the error code from `{"error": ...}`; never the body. Map 400/401 token responses to `%w`-wrapped `ErrAuth`. `Ensure` is a no-op while `now+60s < ExpiresAt`. `Load` caches tokens; `Ready` requires a refresh token and no prior `ErrAuth`.
- [ ] **Step 4:** run → PASS.
- [ ] **Step 5:** commit `feat(unlock): sign in to Zwift and refresh the session`.

### Task 3: d-lock challenge solve (`internal/unlock/client.go`)

**Files:**
- Modify: `internal/unlock/client.go`
- Test: `internal/unlock/client_test.go`

**Interfaces:**
- Produces: `(*Client).Solve(ctx, challenge []byte) ([]byte, error)`.

- [ ] **Step 1: failing tests**
  - `TestSolvePostsChallenge`: asserts `POST`, path `/api/d-lock-service/device/authenticate`, `Authorization: Bearer <cached access>`, `Content-Type`/`Accept: application/x-protobuf-lite`, body equals the challenge; returns the response body.
  - `TestSolveRefreshesOn401ThenRetries`: first call 401, token endpoint returns a new access token, second call returns the reply; exactly one refresh.
  - `TestSolveRejectsEmptyReply`.
  - `TestSolveHTTPError` (403) returns an error and no bytes.
  - `TestSolveLogsExchangeWithoutToken`: debug capture contains challenge hex and response hex, not the access token.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** implement expiry check + refresh, 401 → one refresh + one retry, 15s client timeout, redacted header dump (`Authorization`, `Cookie`, `Set-Cookie` → `<redacted>`), hex bodies capped at 4 KiB.
- [ ] **Step 4:** run → PASS.
- [ ] **Step 5:** commit `feat(unlock): answer the pod challenge via the d-lock service`.

### Task 4: BLE challenge hook

**Files:**
- Modify: `internal/ble/ble.go` (`Session`, `asyncNotify`, ack conditions), `internal/ble/asyncnotify_test.go`
- Test: `internal/ble/unlock_test.go` (new)

**Interfaces:**
- Produces: `ble.ChallengeSolver` interface; `Session(t Target, km *KeyMap, solver ChallengeSolver) error`; `challengePayload(frame []byte) []byte`; `unlockFrame(ctx, solver, challenge) []byte`; `latestSolve`.
- Consumes: `unlock.Client` (via the interface, wired in Task 6).

- [ ] **Step 1: failing tests**
  - `TestChallengePayloadStripsPrefix`: `FF 03 00 0A 21 …` → `0A 21 …`; shorter frames → nil.
  - `TestUnlockFrame`: fake solver returns `AA BB` → `FF 04 00 AA BB`; error → `FF 04 00`; nil solver → `FF 04 00`.
  - `TestAsyncNotifyPassesChallengePayload`: `FF 03 …` invokes the callback with the whole frame; `FF 05 …` and `0x23 …` do not.
  - `TestLatestSolveKeepsNewest`: while the first run blocks, push two more; only the first and the last are run.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** implement the interface and helpers; `asyncNotify(label, onChallenge, activity, bh)`; in `Session`, `nil` → old rearm; non-nil → latest-wins goroutine with a 10s context, writes the reply and anchors the ack; suppress the RideOn-echo and periodic empty acks when `solver != nil`; a failed reply write only logs (the keepalive tick ends the session).
- [ ] **Step 4:** run `go test ./internal/ble` → PASS.
- [ ] **Step 5:** commit `feat(ble): answer the pod challenge with the signed reply`.

### Task 5: TUI sign-in gate

**Files:**
- Create: `internal/tui/login.go`
- Test: `internal/tui/login_test.go`
- Modify: `internal/tui/model.go`, `internal/tui/view.go`

**Interfaces:**
- Produces: `tui.AuthState` (`AuthNone`, `AuthChecking`, `AuthPrompt`),
  `tui.AuthConfig{State, Message string; Check func() error; Login func(email, password string) error; Done func()}`,
  `Config.Auth AuthConfig`.
- Consumes: nothing from earlier tasks (the callbacks are injected).

- [ ] **Step 1: failing tests**
  - `TestAuthPromptSkipCallsDoneOnceAndBanners`: Esc → `AuthNone`, `Done` once; `View` contains `LEFT controller locked`.
  - `TestAuthPromptTypesEmailAndMasksPassword`: typed email visible, password renders as bullets and the raw password is absent from `View`.
  - `TestAuthLoginSubmitRunsLoginCmd`: Enter returns a cmd; invoking it yields `authLoginDoneMsg`; feeding it back calls `Done` once and closes the gate.
  - `TestAuthLoginRejectedStaysOnForm`: error shown, still `AuthPrompt`, `Done` not called.
  - `TestAuthCheckingResolves`: `checkCmd()` yields `authCheckDoneMsg{nil}` → gate closes; `Done` once.
  - `TestAuthCheckingFailureShowsForm`: check error → `AuthPrompt`, message rendered.
  - `TestAuthGateSwallowsStatusShortcuts`: `q` types into the email field (no `tea.Quit`); `Ctrl+C` quits.
  - `TestAuthSkipBanner` visible before Bluetooth is on.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** implement `authModel` in `login.go`; `Init` batches `tickCmd` with `checkCmd` when checking; `Update` routes keys through the gate first; `View` returns `authView()` while gated and `authAlert()` (skip banner) otherwise; `resolveAuth` calls `Done` exactly once.
- [ ] **Step 4:** run → PASS.
- [ ] **Step 5:** commit `feat(tui): ask for the Zwift sign-in that unlocks the LEFT controller`.

### Task 6: Wiring (TUI gate before BLE, plain fallback)

**Files:**
- Modify: `main.go`, `tuirun.go`
- Test: `main_test.go` (new, if a pure helper is extracted), plus existing tests

**Interfaces:**
- Produces: `startListening(km, scanFor, reconnect, solver ble.ChallengeSolver)`; `liveFeed(..., solver)`; `tuiAuth(client *unlock.Client) tui.AuthConfig`; the tokens live in `filepath.Dir(configFile)`.

- [ ] **Step 1:** failing test for the pure helper `tuiAuth`? The gate's behaviour is already covered by Task 5; instead assert `auth.Solver(client)` returns nil when `!Ready()` and the client when ready (small unexported helper with a test).
- [ ] **Step 2:** implement: build the store/client from `filepath.Dir(configFile)`; `Load`; TUI auth config with `Check` (Ensure; `ErrAuth` → "your Zwift session expired — sign in again"; transient → warn + nil), `Login` (map `ErrAuth` to "wrong email or password"), `Done` closing a ready channel; pass `start` that waits on ready then `liveFeed`; `-plain` warns when not ready and passes a nil solver; demo untouched.
- [ ] **Step 3:** `go test ./...` → PASS.
- [ ] **Step 4:** commit `feat: gate Bluetooth on the Zwift sign-in and wire the solver`.

### Task 7: Docs, protocol comment, full verification

**Files:**
- Modify: `AGENTS.md`, `internal/zwift/protocol.go`, `README.md` if it documents startup
- Test: `just sanity`

- [ ] **Step 1:** document in AGENTS.md: the sign-in gate, the token file, `nil` solver semantics, the API path, and that the exchange is logged for the future API-removal work. Update the stale `ff 04 00` comment in `protocol.go`.
- [ ] **Step 2:** run `just sanity` (lint + tests + build) and show the output.
- [ ] **Step 3:** commit `docs: describe the Zwift sign-in unlock path`.
- [ ] **Step 4:** `requesting-code-review` pass, then `finishing-a-development-branch` instructions.
