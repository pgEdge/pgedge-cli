package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Session lock timings. A renewal holds the lock for one bounded token
// request and two keychain calls, so a lock older than staleLockAge was
// left by a process that died holding it. lockWait outlasts that age so
// a waiter recovers a dead holder's lock rather than failing.
var (
	staleLockAge = 45 * time.Second
	lockWait     = 50 * time.Second
	lockPoll     = 50 * time.Millisecond
)

// ErrLockTimeout reports that another process held the session lock
// for the whole wait.
var ErrLockTimeout = errors.New("auth: timed out waiting for the " +
	"session lock")

// LockSession takes this profile's session lock, a file beside the
// token cache, so that only one process at a time renews the session:
// two renewals sending the same rotating refresh token would have Auth0
// revoke both. An O_EXCL file rather than flock, so one implementation
// serves every OS.
func (a *Auth) LockSession() (unlock func(), err error) {
	p, err := a.cachePath()
	if err != nil {
		return nil, err
	}
	p += ".lock"
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, fmt.Errorf("auth: create cache dir: %w", err)
	}
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304: cachePath validates both name segments
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(p) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("auth: session lock: %w", err)
		}
		if fi, err := os.Stat(p); err == nil &&
			time.Since(fi.ModTime()) > staleLockAge {
			_ = os.Remove(p)
			continue
		}
		if time.Now().After(deadline) {
			return nil, ErrLockTimeout
		}
		time.Sleep(lockPoll)
	}
}
