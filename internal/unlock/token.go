// Package unlock signs in to Zwift and answers the LEFT Click pod's ~24h
// unlock challenge.
//
// The pod's button engine stays alive only while Zwift's server-signed unlock
// is valid. The game app forwards the pod's challenge to its d-lock service and
// relays the reply; ff 04 00 alone does NOT renew it. This package performs the
// same exchange with a session token obtained once from the user's credentials.
package unlock

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// TokenFile is the session file kept next to config.yml.
const TokenFile = "zwift-token.json"

// Tokens is the persisted Zwift sign-in. The refresh token is the long-lived
// credential; the access token is cached with its expiry. The user's password
// is never part of this struct or the file.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Store is the token file's location. The directory is normally the one that
// holds config.yml (%LocalAppData%\zwiftboard on Windows).
type Store struct{ dir string }

// NewStore returns the store for a config directory.
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Path is the token file's full path.
func (s *Store) Path() string { return filepath.Join(s.dir, TokenFile) }

// Load reads the persisted session. A missing file surfaces as fs.ErrNotExist
// so callers can tell "never signed in" from a broken file.
func (s *Store) Load() (Tokens, error) {
	b, err := os.ReadFile(s.Path())
	if err != nil {
		return Tokens{}, err
	}
	var t Tokens
	if err := json.Unmarshal(b, &t); err != nil {
		return Tokens{}, err
	}
	return t, nil
}

// Save writes the session atomically with mode 0600: a crash mid-write must
// never leave a truncated token file, and the file must stay user-only between
// saves.
func (s *Store) Save(t Tokens) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.CreateTemp(s.dir, "."+TokenFile+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path())
}

// Delete removes the stored session; already absent is fine.
func (s *Store) Delete() error {
	err := os.Remove(s.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
