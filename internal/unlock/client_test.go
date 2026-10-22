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
