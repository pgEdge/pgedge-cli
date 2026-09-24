// Package keychain stores the Starfleet client secret in the operating
// system's credential store: macOS Keychain, the Secret Service on
// Linux, Credential Manager on Windows.
package keychain

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

// service is the keychain service name every entry is filed under.
const service = "pgedge-cli"

// callTimeout bounds one keychain call. A Linux host with no Secret
// Service can block on D-Bus rather than fail, and a stalled login is
// worse than a plaintext fallback.
const callTimeout = 5 * time.Second

var (
	// ErrNotFound reports that the store holds no entry for the account.
	ErrNotFound = errors.New("keychain: no entry")
	// ErrUnavailable reports that the store could not be used at all.
	ErrUnavailable = errors.New("keychain: unavailable")
)

// Store is a credential store keyed by account.
type Store interface {
	Get(account string) (string, error)
	Set(account, secret string) error
	Delete(account string) error
}

// Account names the entry for one profile of one config file. The path
// is part of it so --config other.yaml with a same-named profile cannot
// read another file's secret.
func Account(profile, configPath string) string {
	if abs, err := filepath.Abs(configPath); err == nil {
		configPath = abs
	}
	return profile + "@" + configPath
}

// OS returns the operating system's store.
func OS() Store {
	return &osStore{
		get:     keyring.Get,
		set:     keyring.Set,
		del:     keyring.Delete,
		timeout: callTimeout,
	}
}

type osStore struct {
	get     func(service, account string) (string, error)
	set     func(service, account, secret string) error
	del     func(service, account string) error
	timeout time.Duration
}

func (s *osStore) Get(account string) (string, error) {
	var v string
	err := s.call(func() error {
		var err error
		v, err = s.get(service, account)
		return err
	})
	return v, err
}

func (s *osStore) Set(account, secret string) error {
	return s.call(func() error { return s.set(service, account, secret) })
}

func (s *osStore) Delete(account string) error {
	return s.call(func() error { return s.del(service, account) })
}

// call runs fn under the timeout and maps go-keyring's errors onto this
// package's two. A timed-out call is abandoned, not cancelled: go-keyring
// takes no context, and the process exits shortly after either way.
func (s *osStore) call(fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return classify(err)
	case <-time.After(s.timeout):
		return fmt.Errorf("%w: no answer within %s", ErrUnavailable,
			s.timeout)
	}
}

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, keyring.ErrNotFound):
		return ErrNotFound
	default:
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
}

// Fake is an in-memory Store for tests. A non-nil Err makes every call
// fail with it, wrapped as ErrUnavailable.
type Fake struct {
	Err error

	mu      sync.Mutex
	entries map[string]string
	// Sets and Deletes count calls, failed ones included.
	Sets, Deletes int
}

// Get returns the stored secret.
func (f *Fake) Get(account string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, f.Err)
	}
	v, ok := f.entries[account]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set stores secret under account.
func (f *Fake) Set(account, secret string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sets++
	if f.Err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, f.Err)
	}
	if f.entries == nil {
		f.entries = map[string]string{}
	}
	f.entries[account] = secret
	return nil
}

// Delete removes the entry.
func (f *Fake) Delete(account string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Deletes++
	if f.Err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, f.Err)
	}
	if _, ok := f.entries[account]; !ok {
		return ErrNotFound
	}
	delete(f.entries, account)
	return nil
}
