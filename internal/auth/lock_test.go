package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/keychain"
)

// TestLockSessionIsPerProfile checks that a held lock shuts out the
// same profile only, and that a dead holder's lock is recovered.
func TestLockSessionIsPerProfile(t *testing.T) {
	savedWait, savedAge := lockWait, staleLockAge
	t.Cleanup(func() { lockWait, staleLockAge = savedWait, savedAge })
	lockWait, staleLockAge = 100*time.Millisecond, time.Hour

	dir := t.TempDir()
	dev := &Auth{Profile: "dev", Module: "starfleet", CacheDir: dir}
	unlock, err := dev.LockSession()
	if err != nil {
		t.Fatalf("LockSession: %v", err)
	}
	if _, err := (&Auth{Profile: "dev", Module: "starfleet",
		CacheDir: dir}).LockSession(); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("second lock on dev: err = %v, want ErrLockTimeout", err)
	}
	other, err := (&Auth{Profile: "prod", Module: "starfleet",
		CacheDir: dir}).LockSession()
	if err != nil {
		t.Fatalf("lock on another profile: %v", err)
	}
	other()
	unlock()
	if _, err := dev.LockSession(); err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}

	// The holder dies without unlocking: its lock goes stale.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "dev-starfleet.json.lock"),
		old, old); err != nil {
		t.Fatal(err)
	}
	staleLockAge = time.Minute
	if _, err := dev.LockSession(); err != nil {
		t.Fatalf("stale lock was not recovered: %v", err)
	}
}

// TestSessionRefreshTokenHasItsOwnKeychainEntry checks that the refresh
// token neither overwrites nor is cleared with the profile's client
// secret, and that it is refused rather than kept elsewhere when there
// is no keychain.
func TestSessionRefreshTokenHasItsOwnKeychainEntry(t *testing.T) {
	kc := &keychain.Fake{}
	a := &Auth{Profile: "dev", ConfigPath: "/cfg/config.yaml", Keychain: kc}
	if err := kc.Set(a.KeychainAccount(), "client-secret"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveSessionRefreshToken("rt"); err != nil {
		t.Fatalf("SaveSessionRefreshToken: %v", err)
	}
	if got, err := a.SessionRefreshToken(); err != nil || got != "rt" {
		t.Errorf("SessionRefreshToken = %q, %v", got, err)
	}
	if err := a.ClearSessionRefreshToken(); err != nil {
		t.Fatalf("ClearSessionRefreshToken: %v", err)
	}
	if err := a.ClearSessionRefreshToken(); err != nil {
		t.Errorf("clearing an absent token: %v", err)
	}
	if got, err := a.KeychainSecret(); err != nil || got != "client-secret" {
		t.Errorf("client secret = %q, %v after the session's churn", got,
			err)
	}

	none := &Auth{Profile: "dev"}
	if err := none.SaveSessionRefreshToken("rt"); !errors.Is(err,
		keychain.ErrUnavailable) {
		t.Errorf("save without a keychain: err = %v", err)
	}
	if _, err := none.SessionRefreshToken(); !errors.Is(err,
		keychain.ErrUnavailable) {
		t.Errorf("read without a keychain: err = %v", err)
	}
	if err := none.ClearSessionRefreshToken(); err != nil {
		t.Errorf("clear without a keychain: %v", err)
	}
}
