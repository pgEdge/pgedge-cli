package signin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDevice is an Auth0 that issues one device code and answers each
// poll with the next of polls: an OAuth error code, or "" for tokens.
type fakeDevice struct {
	code  map[string]any
	polls []string

	mu       sync.Mutex
	codeForm url.Values
	tokForms []url.Values
	onPoll   func()
}

func (a *fakeDevice) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			a.mu.Lock()
			defer a.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/oauth/device/code":
				a.codeForm = r.PostForm
				_ = json.NewEncoder(w).Encode(a.code)
				return
			case "/oauth/token":
			default:
				http.NotFound(w, r)
				return
			}
			a.tokForms = append(a.tokForms, r.PostForm)
			if a.onPoll != nil {
				a.onPoll()
			}
			next := ""
			if len(a.polls) > 0 {
				next, a.polls = a.polls[0], a.polls[1:]
			}
			if next != "" {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": next, "error_description": "said " + next})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at", "refresh_token": "rt",
				"expires_in": 86400})
		}))
	t.Cleanup(srv.Close)
	return srv
}

func deviceCodeBody(interval, expiresIn int) map[string]any {
	return map[string]any{
		"device_code":               "dev-code",
		"user_code":                 "ABCD-EFGH",
		"verification_uri":          "https://auth.example/activate",
		"verification_uri_complete": "https://auth.example/activate?user_code=ABCD-EFGH",
		"interval":                  interval,
		"expires_in":                expiresIn,
	}
}

// fakeClock advances only when RunDevice sleeps, and records each wait.
type fakeClock struct {
	now   time.Time
	slept []time.Duration
}

func (c *fakeClock) sleep(_ context.Context, d time.Duration) error {
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
	return nil
}

func TestRunDevice(t *testing.T) {
	s := time.Second
	for _, tc := range []struct {
		name      string
		code      map[string]any
		polls     []string
		want      error  // nil means tokens
		denied    string // the DeniedError code wanted
		slept     []time.Duration
		pollCount int
	}{
		{name: "pending then success", code: deviceCodeBody(5, 900),
			polls: []string{"authorization_pending", "authorization_pending"},
			slept: []time.Duration{5 * s, 5 * s, 5 * s}, pollCount: 3},
		{name: "slow_down raises the interval for good",
			code:  deviceCodeBody(2, 900),
			polls: []string{"slow_down", "authorization_pending"},
			slept: []time.Duration{2 * s, 7 * s, 7 * s}, pollCount: 3},
		{name: "no interval means five seconds",
			code: deviceCodeBody(0, 900), slept: []time.Duration{5 * s},
			pollCount: 1},
		{name: "expired_token", code: deviceCodeBody(5, 900),
			polls: []string{"authorization_pending", "expired_token"},
			want:  ErrExpired, pollCount: 2},
		{name: "access_denied", code: deviceCodeBody(5, 900),
			polls: []string{"access_denied"}, denied: "access_denied",
			pollCount: 1},
		// Polled at 5s; the second wait reaches expires_in, so no
		// second poll.
		{name: "stops at expires_in", code: deviceCodeBody(5, 10),
			polls: slices.Repeat([]string{"authorization_pending"}, 10),
			want:  ErrExpired, pollCount: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeDevice{code: tc.code, polls: tc.polls}
			srv := a.serve(t)
			f, out := newFlow(srv, nil)
			clock := &fakeClock{now: time.Unix(1e9, 0)}
			f.Sleep = clock.sleep
			f.Now = func() time.Time { return clock.now }

			tok, err := f.RunDevice(context.Background())
			switch {
			case tc.denied != "":
				var d *DeniedError
				if !errors.As(err, &d) || d.Code != tc.denied {
					t.Fatalf("err = %v, want %s", err, tc.denied)
				}
			case tc.want != nil:
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
			case err != nil:
				t.Fatalf("RunDevice: %v", err)
			case tok.AccessToken != "at" || tok.RefreshToken != "rt":
				t.Errorf("tokens = %+v", tok)
			}
			if len(a.tokForms) != tc.pollCount {
				t.Errorf("polled %d times, want %d", len(a.tokForms),
					tc.pollCount)
			}
			if tc.slept != nil && !slices.Equal(clock.slept, tc.slept) {
				t.Errorf("slept %v, want %v", clock.slept, tc.slept)
			}
			for k, want := range map[string]string{
				"client_id": "cid",
				"audience":  "https://api.example",
				"scope":     "openid profile email offline_access",
			} {
				if a.codeForm.Get(k) != want {
					t.Errorf("device code %s = %q, want %q", k,
						a.codeForm.Get(k), want)
				}
			}
			for _, form := range a.tokForms {
				if form.Get("grant_type") != DeviceGrant ||
					form.Get("device_code") != "dev-code" ||
					form.Get("client_id") != "cid" {
					t.Errorf("poll form = %v", form)
				}
			}
			for _, want := range []string{
				"https://auth.example/activate?user_code=ABCD-EFGH",
				"ABCD-EFGH",
				"only if you started this sign-in yourself",
			} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output lacks %q:\n%s", want, out.String())
				}
			}
		})
	}
}

func TestRunDeviceShowsTheBareURIWithoutACompleteOne(t *testing.T) {
	body := deviceCodeBody(5, 900)
	delete(body, "verification_uri_complete")
	a := &fakeDevice{code: body}
	f, out := newFlow(a.serve(t), nil)
	f.Sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := f.RunDevice(context.Background()); err != nil {
		t.Fatalf("RunDevice: %v", err)
	}
	if !strings.Contains(out.String(),
		"https://auth.example/activate\nand enter the code ABCD-EFGH") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestRunDeviceStopsWhenCancelled(t *testing.T) {
	for _, tc := range []struct {
		name         string
		duringPoll   bool
		wantPollings int
	}{
		{name: "while waiting", wantPollings: 1},
		{name: "during a poll", duringPoll: true, wantPollings: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := &fakeDevice{code: deviceCodeBody(5, 900),
				polls: slices.Repeat([]string{"authorization_pending"}, 10)}
			if tc.duringPoll {
				a.onPoll = cancel
			}
			f, _ := newFlow(a.serve(t), nil)
			// The first wait is skipped; the second is a real 5s one.
			waits := 0
			f.Sleep = func(ctx context.Context, d time.Duration) error {
				waits++
				if waits == 1 {
					return nil
				}
				go func() {
					time.Sleep(10 * time.Millisecond)
					cancel()
				}()
				return sleepCtx(ctx, d)
			}
			start := time.Now()
			if _, err := f.RunDevice(ctx); !errors.Is(err, ErrCancelled) {
				t.Fatalf("err = %v, want %v", err, ErrCancelled)
			}
			if time.Since(start) > 2*time.Second {
				t.Errorf("cancellation took %v", time.Since(start))
			}
			if len(a.tokForms) != tc.wantPollings {
				t.Errorf("polled %d times, want %d", len(a.tokForms),
					tc.wantPollings)
			}
		})
	}
}

func TestRunDeviceCodeRequestFails(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		denied     string
		want       string
	}{
		{name: "grant not allowed", status: http.StatusForbidden,
			body: `{"error":"unauthorized_client",` +
				`"error_description":"Grant type not allowed"}`,
			denied: "unauthorized_client"},
		{name: "no OAuth error", status: http.StatusBadGateway,
			body: "<html>", want: "returned 502"},
		{name: "incomplete", status: http.StatusOK,
			body: `{"device_code":"d"}`, want: "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}))
			t.Cleanup(srv.Close)
			f, out := newFlow(srv, nil)
			_, err := f.RunDevice(context.Background())
			var d *DeniedError
			switch {
			case tc.denied != "":
				if !errors.As(err, &d) || d.Code != tc.denied {
					t.Fatalf("err = %v, want %s", err, tc.denied)
				}
			case err == nil || !strings.Contains(err.Error(), tc.want):
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if out.Len() != 0 {
				t.Errorf("printed a code it never got:\n%s", out.String())
			}
		})
	}
}

func TestRunReportsAFailedOpenBeforeWaiting(t *testing.T) {
	f, out := newFlow(httptest.NewServer(http.NotFoundHandler()),
		func(string) error { return errors.New("xdg-open: not found") })
	f.Wait = time.Minute
	_, err := f.Run(context.Background())
	if !errors.Is(err, ErrNoBrowser) ||
		!strings.Contains(err.Error(), "xdg-open: not found") {
		t.Fatalf("err = %v, want ErrNoBrowser", err)
	}
	if bytes.Contains(out.Bytes(), []byte("Waiting")) {
		t.Errorf("waited for a browser that never opened:\n%s", out)
	}
}

func TestRemoteSession(t *testing.T) {
	ssh := map[string]string{"SSH_CONNECTION": "10.0.0.2 51000 10.0.0.1 22"}
	with := func(base map[string]string, k, v string) map[string]string {
		m := map[string]string{k: v}
		for bk, bv := range base {
			m[bk] = bv
		}
		return m
	}
	for _, tc := range []struct {
		name   string
		goos   string
		env    map[string]string
		remote bool
	}{
		{"local linux with DISPLAY", "linux",
			map[string]string{"DISPLAY": ":0"}, false},
		{"local linux, no display", "linux", nil, false},
		{"plain local darwin", "darwin", nil, false},
		{"ssh with X forwarding", "linux", with(ssh, "DISPLAY",
			"localhost:10.0"), false},
		{"ssh with wayland", "freebsd", with(ssh, "WAYLAND_DISPLAY",
			"wayland-0"), false},
		{"ssh, no display", "linux", ssh, true},
		{"SSH_TTY alone, no display", "openbsd",
			map[string]string{"SSH_TTY": "/dev/pts/1"}, true},
		{"darwin over ssh", "darwin", with(ssh, "DISPLAY", ":0"), true},
		{"windows over ssh", "windows", ssh, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why := RemoteSession(tc.goos,
				func(k string) string { return tc.env[k] })
			if (why != "") != tc.remote {
				t.Errorf("RemoteSession = %q, want remote %v", why,
					tc.remote)
			}
		})
	}
}
