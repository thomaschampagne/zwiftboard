package unlock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
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
var ErrAuth = errors.New("Zwift sign-in rejected")

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

// Ensure renews the access token when it is missing or about to expire, so a
// start-up check catches a dead refresh token before the pods connect.
func (c *Client) Ensure(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded {
		return ErrNoToken
	}
	if c.dead {
		return ErrAuth
	}
	if c.accessValidLocked() {
		return nil
	}
	return c.refreshLocked(ctx)
}

func (c *Client) accessValidLocked() bool {
	return c.tokens.AccessToken != "" && c.now().Add(refreshLeeway).Before(c.tokens.ExpiresAt)
}

// Login exchanges the user's email and password for a session and persists it.
// The credentials are used for this request only: they are never stored, and
// the request body is never logged.
func (c *Client) Login(ctx context.Context, email, password string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, err := c.tokenRequest(ctx, url.Values{
		"grant_type": {"password"},
		"client_id":  {clientID},
		"username":   {email},
		"password":   {password},
	}, "")
	if err != nil {
		return err
	}
	c.tokens = t
	c.loaded = true
	c.dead = false
	if err := c.store.Save(t); err != nil {
		return err
	}
	slog.Debug("zwift sign-in: token stored", "path", c.store.Path(), "expires_at", t.ExpiresAt.Format(time.RFC3339))
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
	if err := c.store.Save(t); err != nil {
		return err
	}
	slog.Debug("zwift sign-in: token stored", "path", c.store.Path(), "expires_at", t.ExpiresAt.Format(time.RFC3339))
	return nil
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
	defer resp.Body.Close()
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
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return Tokens{}, fmt.Errorf("%w: %s", ErrAuth, codeOrStatus(code, resp.StatusCode))
		}
		return Tokens{}, fmt.Errorf("token endpoint: HTTP %d", resp.StatusCode)
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

func codeOrStatus(code string, status int) string {
	if code != "" {
		return code
	}
	return fmt.Sprintf("HTTP %d", status)
}
