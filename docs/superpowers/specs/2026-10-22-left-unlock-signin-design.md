# Left-controller unlock: Zwift sign-in in the TUI

**Date:** 2026-10-22
**Status:** approved in chat (2026-10-22)

## Intent

The LEFT Click V2 pod only types while Zwift's ~24h account unlock is valid.
`ff 04 00` does not renew it (proven 2026-10-22), and the unlock is a
server-signed reply to a per-connection challenge. Today the app answers every
challenge with the empty ack, so an expired unlock silently kills the LEFT
pod's button engine while every other frame keeps flowing.

This change lets the user sign in to Zwift once, stores the resulting session
token next to `config.yml`, and answers each pod challenge with the real
server reply. The Zwift API stays a dependency for now; the logging added here
is the raw material for a later session aimed at removing it.

## Goals

- On first run (no token), prompt for email + password **in the TUI**, explain
  that the LEFT controller needs the Zwift-side unlock, and let the user skip.
- Skipping is honest and visible: only the RIGHT controller works normally,
  and the status screen says so.
- Store the session token next to `config.yml`, 0600, and refresh it silently.
- Answer the pod's `FF 03` challenge with
  `FF 04 00 <server reply>`; fall back to `ff 04 00` on every failure.
- Log the full challenge/request/response exchange at Debug level, with all
  credentials, tokens and usernames redacted, so a later debug session can
  study replay/forgery.

## Non-goals

- Removing the Zwift API dependency (the response is server-signed; no local
  forge is known).
- A `-login` CLI flag, a "switch account" flow, or a `-logout` command.
- Reconnecting the pods automatically after a late sign-in. The gate runs
  before BLE scanning starts, so sign-in always applies to the session that
  follows. A skipped user gets the prompt again on the next launch.
- Encrypting the token with DPAPI. The file lives in the user's LocalAppData.

## User flow

1. Startup, TUI mode, no token file: full-screen **Zwift sign-in** form before
   any Bluetooth scan. Copy explains: the LEFT controller needs the ~24h Zwift
   unlock; sign in once and zwiftboard renews it automatically; without it only
   the RIGHT controller works normally; email/password are used once and never
   stored.
2. `Enter` signs in (async, spinner on the button). Success: token saved, then
   BLE scanning starts and the status screen shows. `Esc` skips: BLE scanning
   starts, status screen shows a persistent banner
   "LEFT controller locked — Zwift sign-in required; only the RIGHT controller
   works normally."
3. Token file present: brief "checking Zwift session…" state. A successful
   refresh (or a still-valid access token) goes straight to the status screen.
   `invalid_grant` shows the form with "Your Zwift session expired — sign in
   again." A network error is not an auth failure: proceed with the stored
   token and let the first challenge retry.
4. `-plain`: no prompt (no UI). If no token, log a warning that only the RIGHT
   controller is guaranteed to work and that the TUI must be run once to sign
   in. Any token created by a TUI run is reused.
5. `-demo`: no gate (no BLE, no real auth).

## Architecture

New package `internal/unlock` owns everything about the Zwift session:

```go
// Tokens is the persisted sign-in. The refresh token is the long-lived
// credential; the access token is cached with its expiry.
type Tokens struct {
    AccessToken  string    `json:"access_token"`
    RefreshToken string    `json:"refresh_token"`
    ExpiresAt    time.Time `json:"expires_at"`
}

type Store struct{ dir string }        // NewStore(dir) *Store
func (s *Store) Path() string
func (s *Store) Load() (Tokens, error) // fs.ErrNotExist when absent
func (s *Store) Save(Tokens) error     // MkdirAll 0700, tmp+rename, 0600
func (s *Store) Delete() error

type Client struct { /* store, http.Client, URLs, now, mutex, tokens */ }
func New(store *Store) *Client
func (c *Client) Load() error                 // read disk, cache
func (c *Client) Ready() bool                 // a refresh token is loaded
func (c *Client) Ensure(ctx) error            // refresh only when expired
func (c *Client) Login(ctx, email, password string) error
func (c *Client) Solve(ctx, challenge []byte) ([]byte, error)
```

Errors: `ErrNoToken` (nothing stored), `ErrAuth` (Keycloak
`invalid_grant`/`invalid_client`, or a 401 that survives a refresh). Everything
else is a transient error.

`ble` consumes only:

```go
// ChallengeSolver answers the 0xFF 03 challenge with the server-signed reply.
type ChallengeSolver interface {
    Solve(ctx context.Context, challenge []byte) ([]byte, error)
}
func Session(t Target, km *KeyMap, solver ChallengeSolver) error
```

`nil` solver = current behaviour exactly (`ff 04 00` on challenge/echo and the
15s periodic ack). A non-nil solver replaces every empty ack with the real
reply, and an empty ack is only written when the solve fails.

### Startup gate

`runTUI` still starts its `start` goroutine immediately; `start` blocks on a
ready channel that the TUI closes when the gate resolves (signed in or
skipped). `liveFeed` therefore begins scanning only after the gate. This is a
startup ordering gate, not a session gate: once scanning starts, nothing in the
session waits on the TUI (the observational rule in AGENTS.md holds).

The gate state lives in the TUI model:

```go
type AuthState int
const (
    AuthNone AuthState = iota // no gate (signed in, -plain, -demo)
    AuthChecking              // stored session being refreshed
    AuthPrompt                // ask for email + password
)

type AuthConfig struct {
    State   AuthState
    Message string // shown above the form (e.g. "session expired")
    Check   func() error            // runs off the UI goroutine in AuthChecking
    Login   func(email, password string) error
    Done    func()                  // exactly once, after sign-in or skip
}
```

TUI messages: `authCheckDoneMsg{err}`, `authLoginDoneMsg{err}`. The password
field renders masked; `Tab` switches fields; `Esc` skips; `Ctrl+C` quits. The
email/password strings are never logged or written.

### Challenge handling in `ble`

- `asyncNotify` passes the whole `FF 03` frame to the session's `onChallenge`
  instead of immediately writing the ack.
- Payload = bytes after the fixed `FF 03 00` prefix (same slice the game sends
  to its REST call).
- Latest-wins queue: one solve in flight; a newer challenge replaces a pending
  one; stale replies are never written.
- Solve with a 10s timeout; on success write `FF 04 00 <reply>`, on failure log
  a warning and write `ff 04 00`.
- With a solver, suppress the empty ack on the RideOn echo and the 15s periodic
  ack. Keep both when the solver is nil.

### HTTP

- Token: `POST https://secure.zwift.com/auth/realms/zwift/protocol/openid-connect/token`,
  form `grant_type=password|refresh_token`, `client_id=Zwift Game Client`,
  plus `username`/`password` or `refresh_token`.
- Unlock: `POST https://us-or-rly101.zwift.com/api/d-lock-service/device/authenticate`,
  `Authorization: Bearer <access>`, `Content-Type`/`Accept`
  `application/x-protobuf-lite`, body = challenge payload, response body =
  reply bytes.
- 401 → refresh once → retry once.
- 15s HTTP timeout; the BLE side gives up at 10s.

## Logging

All at Debug (`-v`), one line per step, never a credential:

| Event | Fields |
|---|---|
| `zwift sign-in: token request` | grant_type, status, duration_ms, `error` code (never the form body, email or password) |
| `zwift sign-in: token stored` | path, expires_at |
| `dlock challenge` | controller, len, hex |
| `dlock request` | method, url, body_len, body hex, headers (Authorization redacted) |
| `dlock response` | status, duration_ms, body_len, body hex, headers (Set-Cookie, Www-Authenticate redacted) |
| `dlock solve failed` | status/code, error |
| `zwift sign-in: session expired` | no token values |

Startup/shutdown lifecycle stays Info. The full exchange (challenge ↔ request ↔
response) is the corpus for the later offline/replay analysis.

## Security

- Email and password exist only as local variables during the login request;
  the password field is masked; the request body is never logged.
- Token file: 0600, atomic write, next to `config.yml`
  (`%LocalAppData%\zwiftboard\zwift-token.json`).
- Request headers are logged through a redactor that blanks `Authorization`,
  `Cookie` and `Set-Cookie`.

## Failure modes

| Failure | Behaviour |
|---|---|
| No token, user skips | `ff 04 00` fallback; banner says LEFT is locked; prompt returns next launch |
| No network at challenge | Warn, `ff 04 00`, RIGHT controller unaffected |
| Refresh token rejected | `ErrAuth`; TUI login form at startup; at runtime warn + fallback |
| API 403/5xx | Warn with status, `ff 04 00` |
| Response empty | Treat as failure, `ff 04 00` |

## Testing

No hardware required:

- Store: round-trip, missing file, 0600, directory creation.
- Login: form fields and client id, token persistence, `invalid_grant` → ErrAuth,
  password never appears in captured logs.
- Refresh: expiry-driven, rotated refresh token persisted, invalid_grant →
  ErrAuth, 401 → one refresh + one retry.
- Solve: exact URL/headers/body, reply passthrough, empty reply rejected,
  403/network errors.
- BLE: `FF 03 00 <payload>` → solver receives `<payload>`; reply framed as
  `FF 04 00 <reply>`; failure → `ff 04 00`; latest-wins queue; non-challenge
  frames untouched.
- TUI: no token → prompt; skip → Done once + banner; success → Done once;
  `invalid_grant` message; masked password; refresh failure → form.
- Wiring: gate blocks BLE start until Done; `-plain` without token logs the
  warning and passes a nil solver.
