package unlock

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// tokenServer serves the Keycloak endpoint and records the last form it saw.
func tokenServer(t *testing.T, status int, body string) (*httptest.Server, *url.Values) {
	t.Helper()
	var got url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		got = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(ts.Close)
	return ts, &got
}

func newTestClient(store *Store, tokenURL, dlockURL string, now func() time.Time) *Client {
	return newClient(store, tokenURL, dlockURL, &http.Client{Timeout: 5 * time.Second}, now)
}

func TestLoadMissingToken(t *testing.T) {
	c := newTestClient(NewStore(t.TempDir()), "", "", time.Now)
	if err := c.Load(); !errors.Is(err, ErrNoToken) {
		t.Fatalf("Load = %v, want ErrNoToken", err)
	}
	if c.Ready() {
		t.Fatal("Ready = true without a token file")
	}
}

func TestLoginPostsPasswordGrantAndStoresTokens(t *testing.T) {
	ts, form := tokenServer(t, http.StatusOK,
		`{"access_token":"access-1","refresh_token":"refresh-1","expires_in":300}`)
	store := NewStore(t.TempDir())
	now := time.Unix(1_700_000_000, 0).UTC()
	c := newTestClient(store, ts.URL, ts.URL, func() time.Time { return now })

	if err := c.Login(context.Background(), "rider@example.com", "hunter2"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	want := map[string]string{
		"grant_type": "password",
		"client_id":  "Zwift Game Client",
		"username":   "rider@example.com",
		"password":   "hunter2",
	}
	for k, v := range want {
		if got := form.Get(k); got != v {
			t.Fatalf("form %s = %q, want %q", k, got, v)
		}
	}
	if !c.Ready() {
		t.Fatal("Ready = false after a successful login")
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if saved.AccessToken != "access-1" || saved.RefreshToken != "refresh-1" {
		t.Fatalf("saved = %+v", saved)
	}
	if wantExpiry := now.Add(300 * time.Second); !saved.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("ExpiresAt = %v, want %v", saved.ExpiresAt, wantExpiry)
	}
}

func TestLoginRejected(t *testing.T) {
	ts, _ := tokenServer(t, http.StatusBadRequest,
		`{"error":"invalid_grant","error_description":"Invalid user credentials"}`)
	store := NewStore(t.TempDir())
	c := newTestClient(store, ts.URL, ts.URL, time.Now)

	err := c.Login(context.Background(), "rider@example.com", "wrong")
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("Login = %v, want ErrAuth", err)
	}
	if _, err := store.Load(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("token file written on a rejected login: %v", err)
	}
	if c.Ready() {
		t.Fatal("Ready = true after a rejected login")
	}
}

func TestLoginNeverLogsCredentials(t *testing.T) {
	ts, _ := tokenServer(t, http.StatusBadRequest, `{"error":"invalid_grant"}`)
	var out bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	c := newTestClient(NewStore(t.TempDir()), ts.URL, ts.URL, time.Now)
	_ = c.Login(context.Background(), "rider@example.com", "hunter2")

	logged := out.String()
	for _, secret := range []string{"rider@example.com", "hunter2", "username=", "password="} {
		if strings.Contains(logged, secret) {
			t.Fatalf("log leaked %q:\n%s", secret, logged)
		}
	}
	if !strings.Contains(logged, "invalid_grant") {
		t.Fatalf("log should keep the error code for debugging:\n%s", logged)
	}
}

func TestEnsureRefreshesExpiredTokenAndRotatesIt(t *testing.T) {
	ts, form := tokenServer(t, http.StatusOK,
		`{"access_token":"access-2","refresh_token":"refresh-2","expires_in":300}`)
	store := NewStore(t.TempDir())
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := store.Save(Tokens{AccessToken: "access-1", RefreshToken: "refresh-1", ExpiresAt: now.Add(-time.Minute)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c := newTestClient(store, ts.URL, ts.URL, func() time.Time { return now })
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := c.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got := form.Get("grant_type"); got != "refresh_token" {
		t.Fatalf("grant_type = %q", got)
	}
	if got := form.Get("refresh_token"); got != "refresh-1" {
		t.Fatalf("refresh_token = %q", got)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if saved.AccessToken != "access-2" || saved.RefreshToken != "refresh-2" {
		t.Fatalf("saved = %+v", saved)
	}
}

func TestEnsureSkipsRefreshWhileAccessTokenIsValid(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("token endpoint called although the access token is still valid")
	}))
	defer ts.Close()
	store := NewStore(t.TempDir())
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := store.Save(Tokens{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c := newTestClient(store, ts.URL, ts.URL, func() time.Time { return now })
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := c.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
}

func TestRefreshRejectedClearsTheSession(t *testing.T) {
	ts, _ := tokenServer(t, http.StatusBadRequest, `{"error":"invalid_grant"}`)
	store := NewStore(t.TempDir())
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := store.Save(Tokens{AccessToken: "old", RefreshToken: "dead", ExpiresAt: now.Add(-time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c := newTestClient(store, ts.URL, ts.URL, func() time.Time { return now })
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := c.Ensure(context.Background()); !errors.Is(err, ErrAuth) {
		t.Fatalf("Ensure = %v, want ErrAuth", err)
	}
	if c.Ready() {
		t.Fatal("Ready = true after the refresh token was rejected")
	}
}

// dlockServer serves the challenge endpoint at the path the game uses.
func dlockServer(t *testing.T, challenge, reply []byte, calls *int, auth func(call int, header string) (status int, body []byte)) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const path = "/api/d-lock-service/device/authenticate"
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != path {
			t.Errorf("path = %s, want %s", r.URL.Path, path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-protobuf-lite" {
			t.Errorf("Content-Type = %q", ct)
		}
		if ac := r.Header.Get("Accept"); ac != "application/x-protobuf-lite" {
			t.Errorf("Accept = %q", ac)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if challenge != nil && !bytes.Equal(body, challenge) {
			t.Errorf("body = % X, want % X", body, challenge)
		}
		*calls++
		status, out := http.StatusOK, reply
		if auth != nil {
			status, out = auth(*calls, r.Header.Get("Authorization"))
		}
		w.WriteHeader(status)
		w.Write(out)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestSolvePostsChallenge(t *testing.T) {
	challenge := []byte{0x0A, 0x21, 0x03, 0x10, 0x80}
	reply := []byte{0xAA, 0xBB, 0xCC}
	calls := 0
	ts := dlockServer(t, challenge, reply, &calls, nil)
	store := NewStore(t.TempDir())
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := store.Save(Tokens{AccessToken: "access-1", RefreshToken: "refresh-1", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c := newTestClient(store, ts.URL, ts.URL+"/api/d-lock-service/device/authenticate", func() time.Time { return now })
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := c.Solve(context.Background(), challenge)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !bytes.Equal(got, reply) {
		t.Fatalf("reply = % X, want % X", got, reply)
	}
}

func TestSolveRefreshesOn401ThenRetries(t *testing.T) {
	challenge := []byte{0x0A, 0x21, 0x03}
	reply := []byte{0x11, 0x22}
	calls := 0
	ts := dlockServer(t, challenge, reply, &calls, func(call int, header string) (int, []byte) {
		if call == 1 {
			return http.StatusUnauthorized, nil
		}
		if header != "Bearer access-2" {
			t.Errorf("retry Authorization = %q, want the refreshed token", header)
		}
		return http.StatusOK, reply
	})
	tokenCalls := 0
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		io.WriteString(w, `{"access_token":"access-2","refresh_token":"refresh-2","expires_in":300}`)
	}))
	defer tokens.Close()

	store := NewStore(t.TempDir())
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := store.Save(Tokens{AccessToken: "access-1", RefreshToken: "refresh-1", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c := newTestClient(store, tokens.URL, ts.URL+"/api/d-lock-service/device/authenticate", func() time.Time { return now })
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := c.Solve(context.Background(), challenge)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !bytes.Equal(got, reply) {
		t.Fatalf("reply = % X, want % X", got, reply)
	}
	if tokenCalls != 1 {
		t.Fatalf("token refreshes = %d, want 1", tokenCalls)
	}
	if calls != 2 {
		t.Fatalf("dlock calls = %d, want 2", calls)
	}
}

func TestSolveRejectsEmptyReply(t *testing.T) {
	calls := 0
	ts := dlockServer(t, []byte{0x01}, nil, &calls, nil)
	store := NewStore(t.TempDir())
	now := time.Now()
	if err := store.Save(Tokens{AccessToken: "a", RefreshToken: "r", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c := newTestClient(store, ts.URL, ts.URL+"/api/d-lock-service/device/authenticate", func() time.Time { return now })
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := c.Solve(context.Background(), []byte{0x01}); err == nil {
		t.Fatal("Solve accepted an empty reply")
	}
}

func TestSolveHTTPError(t *testing.T) {
	calls := 0
	ts := dlockServer(t, []byte{0x01}, nil, &calls, func(int, string) (int, []byte) {
		return http.StatusForbidden, []byte("RBAC: access denied")
	})
	store := NewStore(t.TempDir())
	now := time.Now()
	if err := store.Save(Tokens{AccessToken: "a", RefreshToken: "r", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c := newTestClient(store, ts.URL, ts.URL+"/api/d-lock-service/device/authenticate", func() time.Time { return now })
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := c.Solve(context.Background(), []byte{0x01}); err == nil {
		t.Fatal("Solve accepted a 403")
	}
}

func TestSolveLogsExchangeWithoutToken(t *testing.T) {
	challenge := []byte{0x0A, 0x21, 0x7F}
	reply := []byte{0xAA, 0xBB}
	calls := 0
	ts := dlockServer(t, challenge, reply, &calls, nil)
	var out bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	store := NewStore(t.TempDir())
	now := time.Now()
	if err := store.Save(Tokens{AccessToken: "secret-access", RefreshToken: "secret-refresh", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c := newTestClient(store, ts.URL, ts.URL+"/api/d-lock-service/device/authenticate", func() time.Time { return now })
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := c.Solve(context.Background(), challenge); err != nil {
		t.Fatalf("Solve: %v", err)
	}
	logged := out.String()
	if !strings.Contains(logged, "0A 21 7F") || !strings.Contains(logged, "AA BB") {
		t.Fatalf("log lacks the exchange hex:\n%s", logged)
	}
	for _, secret := range []string{"secret-access", "secret-refresh", "Bearer secret"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("log leaked %q:\n%s", secret, logged)
		}
	}
}

func TestTokenEndpointOnlyCredentialErrorsAreAuth(t *testing.T) {
	// Only Keycloak's credential/session error codes mean "never retry". A
	// WAF 403, a 5xx or a malformed 400 must stay transient: wiping the stored
	// session and asking for the password again would be wrong.
	cases := []struct {
		status   int
		body     string
		wantAuth bool
	}{
		{http.StatusBadRequest, `{"error":"invalid_grant"}`, true},
		{http.StatusUnauthorized, `{"error":"invalid_client"}`, true},
		{http.StatusBadRequest, `{"error":"unauthorized_client"}`, true},
		{http.StatusForbidden, `{"error":"access_denied"}`, false},
		{http.StatusBadRequest, `{"error":"invalid_request"}`, false},
		{http.StatusBadGateway, `<html>bad gateway</html>`, false},
	}
	for _, tc := range cases {
		ts, _ := tokenServer(t, tc.status, tc.body)
		c := newTestClient(NewStore(t.TempDir()), ts.URL, ts.URL, time.Now)
		err := c.Login(context.Background(), "rider@example.com", "hunter2")
		if err == nil {
			t.Fatalf("%d %s: Login succeeded", tc.status, tc.body)
		}
		if got := errors.Is(err, ErrAuth); got != tc.wantAuth {
			t.Fatalf("%d %s: ErrAuth = %v, want %v (err: %v)", tc.status, tc.body, got, tc.wantAuth, err)
		}
	}
}

func TestTransientRefreshFailureKeepsTheSession(t *testing.T) {
	// The stored access token is expired and the refresh hits a 403. The
	// session must survive: the next challenge can try again, and the user is
	// not asked to retype a password that was never wrong.
	ts, _ := tokenServer(t, http.StatusForbidden, `{"error":"access_denied"}`)
	store := NewStore(t.TempDir())
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := store.Save(Tokens{AccessToken: "old", RefreshToken: "refresh-1", ExpiresAt: now.Add(-time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c := newTestClient(store, ts.URL, ts.URL, func() time.Time { return now })
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	err := c.Ensure(context.Background())
	if err == nil {
		t.Fatal("Ensure should report the failed refresh")
	}
	if errors.Is(err, ErrAuth) {
		t.Fatalf("403 mapped to ErrAuth: %v", err)
	}
	if !c.Ready() {
		t.Fatal("a transient refresh failure discarded the stored session")
	}
}

func TestSolveRefreshesOnlyOnceOnRepeated401(t *testing.T) {
	// A 401 after the refresh is final: exactly one refresh, no loop, and the
	// caller gets an error instead of an empty reply.
	challenge := []byte{0x0A, 0x21, 0x03}
	calls := 0
	ts := dlockServer(t, challenge, nil, &calls, func(int, string) (int, []byte) {
		return http.StatusUnauthorized, nil
	})
	tokenCalls := 0
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		io.WriteString(w, `{"access_token":"access-2","refresh_token":"refresh-2","expires_in":300}`)
	}))
	defer tokens.Close()

	store := NewStore(t.TempDir())
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := store.Save(Tokens{AccessToken: "access-1", RefreshToken: "refresh-1", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c := newTestClient(store, tokens.URL, ts.URL+"/api/d-lock-service/device/authenticate", func() time.Time { return now })
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := c.Solve(context.Background(), challenge); err == nil {
		t.Fatal("Solve accepted a reply after a second 401")
	}
	if tokenCalls != 1 {
		t.Fatalf("token refreshes = %d, want exactly 1", tokenCalls)
	}
	if calls != 2 {
		t.Fatalf("dlock calls = %d, want 2 (one retry, no loop)", calls)
	}
}

func TestSolveRefreshesExpiredTokenBeforeTheCall(t *testing.T) {
	// An expired access token is refreshed BEFORE the dlock call, so the first
	// request already carries the new token (no wasted 401 round trip).
	challenge := []byte{0x0A, 0x21, 0x03}
	reply := []byte{0x11, 0x22}
	calls := 0
	ts := dlockServer(t, challenge, reply, &calls, func(call int, header string) (int, []byte) {
		if header != "Bearer access-2" {
			t.Errorf("call %d Authorization = %q, want the pre-refreshed token", call, header)
		}
		return http.StatusOK, reply
	})
	tokenCalls := 0
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		io.WriteString(w, `{"access_token":"access-2","refresh_token":"refresh-2","expires_in":300}`)
	}))
	defer tokens.Close()

	store := NewStore(t.TempDir())
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := store.Save(Tokens{AccessToken: "access-1", RefreshToken: "refresh-1", ExpiresAt: now.Add(-time.Minute)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c := newTestClient(store, tokens.URL, ts.URL+"/api/d-lock-service/device/authenticate", func() time.Time { return now })
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := c.Solve(context.Background(), challenge)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !bytes.Equal(got, reply) {
		t.Fatalf("reply = % X, want % X", got, reply)
	}
	if tokenCalls != 1 {
		t.Fatalf("token refreshes = %d, want 1", tokenCalls)
	}
	if calls != 1 {
		t.Fatalf("dlock calls = %d, want 1", calls)
	}
}
