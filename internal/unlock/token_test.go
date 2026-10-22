package unlock

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	want := Tokens{
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresAt:    time.Unix(1_700_000_000, 0).UTC(),
	}
	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}
}

func TestStoreLoadMissing(t *testing.T) {
	_, err := NewStore(t.TempDir()).Load()
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load error = %v, want fs.ErrNotExist", err)
	}
}

func TestStoreSaveCreatesDirWithPrivateMode(t *testing.T) {
	// %LocalAppData%\zwiftboard normally exists by the time a token is saved,
	// but a custom -config in a fresh folder must work too.
	dir := filepath.Join(t.TempDir(), "missing", "zwiftboard")
	s := NewStore(dir)
	if err := s.Save(Tokens{RefreshToken: "refresh"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got, want := s.Path(), filepath.Join(dir, TokenFile); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}
}

func TestStoreDeleteIsIdempotent(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Delete(); err != nil {
		t.Fatalf("Delete on missing file: %v", err)
	}
	if err := s.Save(Tokens{RefreshToken: "refresh"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Load(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load after Delete = %v, want fs.ErrNotExist", err)
	}
}
