package signin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/output"
)

// DeviceGrant is the RFC 8628 grant type /oauth/token polls with.
const DeviceGrant = "urn:ietf:params:oauth:grant-type:device_code"

// ErrExpired reports a device code that ran out before the user
// confirmed it.
var ErrExpired = errors.New("the sign-in code expired before it was " +
	"confirmed; run login again for a new one")

// deviceCode is the /oauth/device/code answer.
type deviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
}

// RunDevice signs in with the OAuth device authorization grant: it
// prints a URL and a code for a browser on any machine, then polls
// until the user confirms or refuses, the code expires, or ctx ends.
func (f *Flow) RunDevice(ctx context.Context) (*Tokens, error) {
	dc, err := f.requestDeviceCode(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ErrCancelled
		}
		return nil, err
	}
	f.showDeviceCode(dc)

	now := f.Now
	if now == nil {
		now = time.Now
	}
	sleep := f.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	life := time.Duration(dc.ExpiresIn) * time.Second
	if life <= 0 {
		life = DefaultWait
	}
	deadline := now().Add(life)
	// RFC 8628 section 3.2: 5 seconds when the server names no interval.
	interval := 5 * time.Second
	if dc.Interval > 0 {
		interval = time.Duration(dc.Interval) * time.Second
	}
	form := url.Values{
		"grant_type":  {DeviceGrant},
		"device_code": {dc.DeviceCode},
		"client_id":   {f.ClientID},
	}
	for {
		if err := sleep(ctx, interval); err != nil {
			return nil, ErrCancelled
		}
		if !now().Before(deadline) {
			return nil, ErrExpired
		}
		pctx, cancel := context.WithTimeout(ctx, deadline.Sub(now()))
		tok, err := postToken(pctx, f.HTTPClient, f.BaseURL, form)
		expired := pctx.Err() != nil
		cancel()
		if ctx.Err() != nil {
			return nil, ErrCancelled
		}
		if expired {
			return nil, ErrExpired
		}
		var d *DeniedError
		if !errors.As(err, &d) {
			return tok, err
		}
		switch d.Code {
		case "authorization_pending":
		case "slow_down":
			// Section 3.5: the increase is permanent for this code.
			interval += 5 * time.Second
		case "expired_token":
			return nil, ErrExpired
		default:
			return nil, d
		}
	}
}

func (f *Flow) requestDeviceCode(ctx context.Context) (*deviceCode, error) {
	resp, raw, err := postForm(ctx, f.HTTPClient, f.BaseURL,
		"/oauth/device/code", url.Values{
			"client_id": {f.ClientID},
			"audience":  {f.Audience},
			"scope":     {scope},
		})
	if err != nil {
		return nil, fmt.Errorf("device code request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if d := deniedFrom(raw); d != nil {
			return nil, d
		}
		return nil, fmt.Errorf("device code request returned %s",
			resp.Status)
	}
	var dc deviceCode
	if err := json.Unmarshal(raw, &dc); err != nil {
		return nil, fmt.Errorf("device code request: decode response: %w",
			err)
	}
	if dc.DeviceCode == "" || dc.UserCode == "" ||
		dc.VerificationURI == "" && dc.VerificationURIComplete == "" {
		return nil, errors.New("device code request returned an " +
			"incomplete response")
	}
	return &dc, nil
}

func (f *Flow) showDeviceCode(dc *deviceCode) {
	if dc.VerificationURIComplete != "" {
		fmt.Fprintf(f.Out, "To sign in to pgEdge Starfleet, open this URL "+
			"in a browser on any device:\n  %s\nand check that it shows "+
			"the code %s.\n", output.Sanitize(dc.VerificationURIComplete),
			output.Sanitize(dc.UserCode))
	} else {
		fmt.Fprintf(f.Out, "To sign in to pgEdge Starfleet, open this URL "+
			"in a browser on any device:\n  %s\nand enter the code %s.\n",
			output.Sanitize(dc.VerificationURI),
			output.Sanitize(dc.UserCode))
	}
	fmt.Fprintln(f.Out, "This code signs in this terminal. Confirm it "+
		"only if you started this sign-in yourself.")
	fmt.Fprintln(f.Out, "Waiting for the sign-in to finish "+
		"(Ctrl-C to cancel)...")
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RemoteSession reports why a browser sign-in on goos cannot reach the
// user, or "" when it can. getenv reads the environment.
func RemoteSession(goos string, getenv func(string) string) string {
	if getenv("SSH_CONNECTION") == "" && getenv("SSH_TTY") == "" {
		return ""
	}
	switch goos {
	case "darwin", "windows":
		// The opener succeeds there, on the remote machine's screen.
		return "this is an SSH session, so a browser would open on " +
			"the remote machine"
	}
	if getenv("DISPLAY") != "" || getenv("WAYLAND_DISPLAY") != "" {
		return ""
	}
	return "this is an SSH session with no display"
}
