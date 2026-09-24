package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeCoderError is a stand-in for internal/starfleet/byoc/cmd.ExitError: any
// error that carries its own process exit code via a Code() method.
type fakeCoderError struct {
	msg  string
	code int
	// err is what this wraps. It is what makes the ordering row below
	// mean anything: without an Unwrap, an error whose MESSAGE says
	// "deadline" is not an error that CARRIES one, and the row passes
	// whichever order the branches are in.
	err error
}

func (e *fakeCoderError) Error() string { return e.msg }
func (e *fakeCoderError) Code() int     { return e.code }
func (e *fakeCoderError) Unwrap() error { return e.err }

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "nil error is success",
			err:  nil,
			want: 0,
		},
		{
			name: "usage error is exit code 2",
			err:  &UsageError{Msg: "bad flag"},
			want: 2,
		},
		{
			name: "wrapped usage error is still exit code 2",
			err:  fmt.Errorf("wrap: %w", &UsageError{Msg: "bad flag"}),
			want: 2,
		},
		{
			name: "generic error is exit code 1",
			err:  errors.New("boom"),
			want: 1,
		},
		{
			name: "coder error returns its own code",
			err:  &fakeCoderError{msg: "auth failed", code: 2},
			want: 2,
		},
		{
			name: "wrapped coder error returns its own code",
			err: fmt.Errorf("wrap: %w",
				&fakeCoderError{msg: "not found", code: 4}),
			want: 4,
		},
		{
			name: "coder error with timeout code",
			err:  &fakeCoderError{msg: "timed out", code: 3},
			want: 3,
		},
		{
			name: "usage error takes priority over coder check",
			err:  &UsageError{Msg: "bad flag"},
			want: 2,
		},
		// #352: a deadline is exit 3 whatever produced it, so the
		// CLI's own bound agrees with a 408/504 the server reports.
		{
			name: "a bare context deadline is exit code 3",
			err:  context.DeadlineExceeded,
			want: 3,
		},
		{
			name: "a wrapped context deadline is exit code 3",
			err:  fmt.Errorf("list databases: %w", context.DeadlineExceeded),
			want: 3,
		},
		{
			name: "a net.Error reporting Timeout is exit code 3",
			err:  fmt.Errorf("get: %w", &timeoutNetError{}),
			want: 3,
		},
		// A code already on the error wins. This is the ordering
		// itself: the deadline is genuinely IN the chain, so the row
		// fails if the isDeadline branch is moved above coder.
		{
			name: "a coder error wrapping a deadline keeps its code",
			err: &fakeCoderError{
				msg:  "authentication failed: context deadline exceeded",
				code: 5,
				err:  context.DeadlineExceeded,
			},
			want: 5,
		},
		// The same, with a code that is NOT a timeout code, so the row
		// cannot pass by coincidence if 3 and the coder's code agree.
		{
			name: "a coder error wrapping a deadline is not upgraded",
			err: fmt.Errorf("wrap: %w", &fakeCoderError{
				msg:  "conflict",
				code: 1,
				err:  context.DeadlineExceeded,
			}),
			want: 1,
		},
		// A refused connection is not a deadline.
		{
			name: "a non-timeout net.Error is still exit code 1",
			err:  fmt.Errorf("dial: %w", &plainNetError{}),
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d",
					tt.err, got, tt.want)
			}
		})
	}
}

// timeoutNetError satisfies net.Error and reports a timeout, standing
// in for the *url.Error a response-header stall produces.
type timeoutNetError struct{}

func (e *timeoutNetError) Error() string { return "i/o timeout" }
func (e *timeoutNetError) Timeout() bool { return true }

//nolint:staticcheck // net.Error.Temporary is deprecated but required
func (e *timeoutNetError) Temporary() bool { return false }

// plainNetError is a net.Error that is NOT a timeout — a refused
// connection. Without it the table could not tell "any net.Error" from
// "a net.Error that timed out".
type plainNetError struct{}

func (e *plainNetError) Error() string { return "connection refused" }
func (e *plainNetError) Timeout() bool { return false }

//nolint:staticcheck // net.Error.Temporary is deprecated but required
func (e *plainNetError) Temporary() bool { return false }

// TestExitCodeOnARealStalledServer is the end-to-end half. The table
// above asserts the classification over hand-built errors; this drives
// a real http.Client against a server that accepts and never answers,
// so the error is whatever Go actually produces rather than what I
// think it produces.
//
// Both stall shapes are covered, and measured they classify the same
// way: a header stall is a *url.Error and a body stall an
// *http.timeoutError, and BOTH satisfy net.Error with Timeout() true.
// They are here because they are different code paths in net/http, not
// because they need different predicates -- a classifier narrowed to
// a message string fails both together, which is how their value was
// confirmed.
func TestExitCodeOnARealStalledServer(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		read    bool
	}{
		{
			name: "headers never arrive",
			handler: func(_ http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
			},
		},
		{
			name: "headers arrive, body stalls",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "128")
				w.WriteHeader(http.StatusOK)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				<-r.Context().Done()
			},
			read: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()

			c := &http.Client{Timeout: 300 * time.Millisecond}
			resp, err := c.Get(srv.URL) //nolint:bodyclose,noctx // closed below
			if tc.read && err != nil {
				// Otherwise this case silently exercises the header
				// shape and passes, while claiming to pin the body one.
				t.Fatalf("headers never arrived, so the body shape "+
					"was never reached: %v", err)
			}
			if err == nil {
				if !tc.read {
					_ = resp.Body.Close()
					t.Fatal("a stalled server produced a response")
				}
				_, err = io.ReadAll(resp.Body)
				_ = resp.Body.Close()
			}
			if err == nil {
				t.Fatal("reading a stalled body succeeded")
			}
			// Wrapped, because every call site wraps before the error
			// reaches ExitCode.
			if got := ExitCode(fmt.Errorf("list databases: %w", err)); got != 3 {
				t.Errorf("ExitCode = %d, want 3, for %v", got, err)
			}
		})
	}
}

var _ net.Error = (*timeoutNetError)(nil)
