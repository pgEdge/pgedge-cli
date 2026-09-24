package keychain

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestAccountJoinsProfileAndAbsolutePath(t *testing.T) {
	abs, err := filepath.Abs("rel/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ profile, path, want string }{
		{"dev", "/tmp/a/config.yaml", "dev@/tmp/a/config.yaml"},
		{"dev", "rel/config.yaml", "dev@" + abs},
	}
	for _, tt := range tests {
		if got := Account(tt.profile, tt.path); got != tt.want {
			t.Errorf("Account(%q, %q) = %q, want %q",
				tt.profile, tt.path, got, tt.want)
		}
	}
	if Account("dev", "/a/config.yaml") == Account("dev", "/b/config.yaml") {
		t.Error("same profile in two config files shares one entry")
	}
}

func TestOSStoreMapsErrors(t *testing.T) {
	boom := errors.New("dbus: no session bus")
	tests := []struct {
		name  string
		inner error
		want  error
	}{
		{"ok", nil, nil},
		{"missing", keyring.ErrNotFound, ErrNotFound},
		{"broken", boom, ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &osStore{
				get: func(string, string) (string, error) {
					return "v", tt.inner
				},
				set:     func(string, string, string) error { return tt.inner },
				del:     func(string, string) error { return tt.inner },
				timeout: time.Second,
			}
			v, err := s.Get("a")
			check(t, "Get", err, tt.want)
			if tt.want == nil && v != "v" {
				t.Errorf("Get = %q, want v", v)
			}
			check(t, "Set", s.Set("a", "v"), tt.want)
			check(t, "Delete", s.Delete("a"), tt.want)
		})
	}
}

func check(t *testing.T, op string, got, want error) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("%s: %v, want nil", op, got)
		}
		return
	}
	if !errors.Is(got, want) {
		t.Errorf("%s: %v, want %v", op, got, want)
	}
}

func TestOSStoreTimesOut(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	s := &osStore{
		get: func(string, string) (string, error) {
			<-block
			return "", nil
		},
		timeout: 10 * time.Millisecond,
	}
	_, err := s.Get("a")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Get on a stalled store = %v, want ErrUnavailable", err)
	}
}

func TestOSReturnsAStore(t *testing.T) {
	s, ok := OS().(*osStore)
	if !ok || s.get == nil || s.set == nil || s.del == nil ||
		s.timeout != callTimeout {
		t.Fatalf("OS() = %#v", OS())
	}
}

func TestFake(t *testing.T) {
	f := &Fake{}
	if _, err := f.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on empty = %v", err)
	}
	if err := f.Delete("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete on empty = %v", err)
	}
	if err := f.Set("a", "s"); err != nil {
		t.Fatal(err)
	}
	if v, err := f.Get("a"); err != nil || v != "s" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if err := f.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v", err)
	}

	broken := &Fake{Err: errors.New("locked")}
	for op, err := range map[string]error{
		"Set":    broken.Set("a", "s"),
		"Delete": broken.Delete("a"),
	} {
		if !errors.Is(err, ErrUnavailable) ||
			!strings.Contains(err.Error(), "locked") {
			t.Errorf("%s on broken fake = %v", op, err)
		}
	}
	if _, err := broken.Get("a"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Get on broken fake = %v", err)
	}
	if broken.Sets != 1 {
		t.Errorf("Sets = %d, want 1", broken.Sets)
	}
}
