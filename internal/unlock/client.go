package unlock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// clientID is the public Keycloak client the Zwift game app uses (verified
	// against the live realm; other guesses answer "invalid_client").
	clientID = "Zwift Game Client"

	defaultTokenURL = "https://secure.zwift.com/auth/realms/zwift/protocol/openid-connect/token"
	defaultDLockURL = "https://us-or-rly101.zwift.com/api/d-lock-service/device/authenticate"

	// refreshLeeway renews the access token before it actually expires: the
	// challenge call must never race a token that dies mid-flight.
	refreshLeeway = 60 * time.Second

	httpTimeout = 15 * time.Second
)

// ErrNoToken means no usable sign-in is stored.
var ErrNoToken = errors.New("no Zwift sign-in stored")

// ErrAuth means Zwift rejected the credentials or the stored refresh token.
// The user must sign in again; retrying is pointless.
var ErrAuth = errors.New("zwift sign-in rejected")

// authErrorCodes are the only Keycloak error codes that mean "these
// credentials/session will never work". A 403 from a proxy or a 5xx is NOT an
// auth failure, even though it may arrive with an error body: the stored
// session must survive it.
var authErrorCodes = map[string]bool{
	"invalid_grant":       true,
	"invalid_client":      true,
	"unauthorized_client": true,
}

// Client owns the Zwift session: the stored tokens, their refresh, and the
// d-lock call that answers a pod challenge. One client is shared by the TUI
// (sign-in) and the BLE sessions (solve), so its state is mutex-guarded.
type Client struct {
	store              *Store
	hc                 *http.Client
	tokenURL, dlockURL string
	now                func() time.Time

	mu     sync.Mutex
	tokens Tokens
	loaded bool
	dead   bool // the refresh token was rejected; Ready must stay false
}

// New returns a client using the production endpoints.
func New(store *Store) *Client {
	return newClient(store, defaultTokenURL, defaultDLockURL, &http.Client{Timeout: httpTimeout}, time.Now)
}

// newClient is the test seam: endpoints, HTTP client and clock are injected.
func newClient(store *Store, tokenURL, dlockURL string, hc *http.Client, now func() time.Time) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: httpTimeout}
	}
	if now == nil {
		now = time.Now
	}
	return &Client{store: store, hc: hc, tokenURL: tokenURL, dlockURL: dlockURL, now: now}
}

// Load reads the stored session into memory. ErrNoToken is the normal
// first-run answer.
func (c *Client) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadLocked()
}

func (c *Client) loadLocked() error {
	t, err := c.store.Load()
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNoToken
	}
	if err != nil {
		return err
	}
	c.tokens = t
	c.loaded = true
	c.dead = false
	if t.RefreshToken == "" {
		return ErrNoToken
	}
	return nil
}

// Ready reports whether a refresh token is loaded and still trusted. It is the
// wiring's signal to hand the client to BLE as a challenge solver.
func (c *Client) Ready() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loaded && !c.dead && c.tokens.RefreshToken != ""
}

func (c *Client) accessValidLocked() bool {
	return c.tokens.AccessToken != "" && c.now().Add(refreshLeeway).Before(c.tokens.ExpiresAt)
}

// Login exchanges the user's email and password for a session and persists it.
// The credentials are used for this request only: they are never stored, and
// the request body is never logged.
func (c *Client) Login(ctx context.Context, email, password string) error {
	t, err := c.tokenRequest(ctx, url.Values{
		"grant_type": {"password"},
		"client_id":  {clientID},
		"username":   {email},
		"password":   {password},
	}, "")
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens = t
	c.loaded = true
	c.dead = false
	c.saveSession(t, "login")
	return nil
}

// refreshLocked rotates the access token (and usually the refresh token).
// A rejected refresh token marks the client dead for the rest of the run.
func (c *Client) refreshLocked(ctx context.Context) error {
	slog.Debug("zwift sign-in: refreshing session")
	t, err := c.tokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {c.tokens.RefreshToken},
	}, c.tokens.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrAuth) {
			c.dead = true
			c.tokens = Tokens{}
		}
		return err
	}
	c.tokens = t
	c.saveSession(t, "refresh")
	return nil
}

// saveSession persists the session without failing the request. The tokens are
// already live in memory: a disk problem must not block this run's LEFT pod
// (the refresh would otherwise be lost), so it only costs a new
// sign-in after a restart. The warning says exactly that.
func (c *Client) saveSession(t Tokens, when string) {
	if err := c.store.Save(t); err != nil {
		slog.Warn("zwift sign-in: could not persist the session — it will be lost on restart",
			"when", when, "path", c.store.Path(), "error", err)
		return
	}
	slog.Debug("zwift sign-in: token stored", "path", c.store.Path(), "expires_at", t.ExpiresAt.Format(time.RFC3339))
}

// tokenResponse is the subset of Keycloak's answer we keep.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// tokenRequest performs one Keycloak grant. It logs the grant type, status,
// duration and error code only — never the form (which carries the password or
// the refresh token) and never the response body.
func (c *Client) tokenRequest(ctx context.Context, form url.Values, keepRefresh string) (Tokens, error) {
	// A sign-in failure is either a server-side error or an input mistake the
	// user cannot see (the password is masked). Log the SHAPE of the submitted
	// credentials only: never the values. password_len != rune count catches a
	// mangled paste; control chars catch a pasted newline; outer spaces catch
	// a stray space the user cannot see behind the bullets.
	if form.Get("grant_type") == "password" {
		email, password := form.Get("username"), form.Get("password")
		slog.Debug("zwift sign-in: credential shape",
			"email_len", len(email),
			"email_outer_space", email != strings.TrimSpace(email),
			"email_control_chars", controlCharCount(email),
			"password_len", len(password),
			"password_runes", utf8.RuneCountInString(password),
			"password_outer_space", password != strings.TrimSpace(password),
			"password_control_chars", controlCharCount(password),
		)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	start := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		slog.Debug("zwift sign-in: token request", "grant_type", form.Get("grant_type"), "error", err)
		return Tokens{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Tokens{}, err
	}
	code := tokenErrorCode(body)
	slog.Debug("zwift sign-in: token request",
		"grant_type", form.Get("grant_type"),
		"status", resp.StatusCode,
		"duration_ms", time.Since(start).Milliseconds(),
		"error", code)

	if resp.StatusCode != http.StatusOK {
		// Only these codes mean the credentials/session will never work.
		// Everything else (WAF 403, 5xx, proxy errors) is transient: the
		// stored refresh token must survive and the user must not be asked
		// for a password that was never wrong.
		if authErrorCodes[code] {
			return Tokens{}, fmt.Errorf("%w: %s", ErrAuth, code)
		}
		if code == "" {
			return Tokens{}, fmt.Errorf("token endpoint: HTTP %d", resp.StatusCode)
		}
		return Tokens{}, fmt.Errorf("token endpoint: HTTP %d (%s)", resp.StatusCode, code)
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return Tokens{}, fmt.Errorf("token endpoint: %w", err)
	}
	if tr.AccessToken == "" {
		return Tokens{}, errors.New("token endpoint: no access token in the reply")
	}
	refresh := tr.RefreshToken
	if refresh == "" {
		refresh = keepRefresh
	}
	if refresh == "" {
		return Tokens{}, errors.New("token endpoint: no refresh token in the reply")
	}
	expires := c.now()
	if tr.ExpiresIn > 0 {
		expires = expires.Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	return Tokens{AccessToken: tr.AccessToken, RefreshToken: refresh, ExpiresAt: expires}, nil
}

// controlCharCount counts C0/C1 control characters. A pasted password can
// carry a trailing newline, which is invisible in the masked field and makes
// the grant fail without the user being able to see why.
func controlCharCount(s string) int {
	n := 0
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			n++
		}
	}
	return n
}

// tokenErrorCode extracts Keycloak's machine-readable error code. The
// description is deliberately ignored: it is not needed and could echo input.
func tokenErrorCode(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return ""
	}
	return e.Error
}

// errUnauthorized triggers exactly one refresh-and-retry inside Solve.
var errUnauthorized = errors.New("dlock: unauthorized")

// Solve answers one pod challenge with the server-signed reply. The plan is:
// refresh the access token if needed, POST the challenge, and on a 401 refresh
// once and retry (the server may have rotated the session under us).
func (c *Client) Solve(ctx context.Context, challenge []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded || c.tokens.RefreshToken == "" {
		return nil, ErrNoToken
	}
	if c.dead {
		return nil, ErrAuth
	}
	if !c.accessValidLocked() {
		if err := c.refreshLocked(ctx); err != nil {
			return nil, err
		}
	}
	reply, err := c.authenticate(ctx, c.tokens.AccessToken, challenge)
	if errors.Is(err, errUnauthorized) {
		if rerr := c.refreshLocked(ctx); rerr != nil {
			return nil, rerr
		}
		return c.authenticate(ctx, c.tokens.AccessToken, challenge)
	}
	return reply, err
}

// authenticate is the d-lock REST call itself: the challenge protobuf is the
// body, the reply bytes are what the pod expects. A 401 is returned as
// errUnauthorized so Solve can refresh once; every other non-200 is final.
func (c *Client) authenticate(ctx context.Context, access string, challenge []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.dlockURL, bytes.NewReader(challenge))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/x-protobuf-lite")
	req.Header.Set("Accept", "application/x-protobuf-lite")

	slog.Debug("dlock request",
		"method", req.Method,
		"url", c.dlockURL,
		"body_len", len(challenge),
		"body", hexBody(challenge),
		"headers", redactHeaders(req.Header))

	start := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		slog.Debug("dlock request failed", "error", err)
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	slog.Debug("dlock response",
		"status", resp.StatusCode,
		"duration_ms", time.Since(start).Milliseconds(),
		"body_len", len(body),
		"body", hexBody(body),
		"headers", redactHeaders(resp.Header))

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, errUnauthorized
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("dlock: HTTP %d", resp.StatusCode)
	case len(body) == 0:
		return nil, errors.New("dlock: empty reply")
	}
	return body, nil
}

// maxLoggedBody caps the hex dump so one huge response cannot flood the log.
const maxLoggedBody = 4 << 10

// hexBody renders a body for the debug log, with a stable "AA BB CC" shape.
func hexBody(b []byte) string {
	if len(b) <= maxLoggedBody {
		return fmt.Sprintf("% X", b)
	}
	return fmt.Sprintf("% X … (%d bytes total)", b[:maxLoggedBody], len(b))
}

// redactHeaders makes a header map safe to log: credential-bearing headers are
// blanked, and the rest is sorted for stable lines.
func redactHeaders(h http.Header) string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString(k)
		b.WriteString(": ")
		if secretHeader(k) {
			b.WriteString("<redacted>")
			continue
		}
		b.WriteString(strings.Join(h[k], ", "))
	}
	return b.String()
}

func secretHeader(k string) bool {
	switch strings.ToLower(k) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie":
		return true
	}
	return false
}
