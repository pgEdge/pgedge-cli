package cli

import (
	"errors"
	"net"
)

// Exit codes per the CLI contract. internal/controlplane/cmd declares its
// own; the two do not share them but must agree.
const (
	ExitOK      = 0 // success
	ExitError   = 1 // general runtime failure
	ExitUsage   = 2 // bad flags, arguments, or command
	ExitTimeout = 3 // a deadline expired before the work finished
)

// UsageError marks an error caused by bad flags or arguments; it
// maps to exit code 2 per the UX standards.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// coder is satisfied by any error that carries its own process exit
// code, such as conn.ExitError. It is declared here so ExitCode need not
// import the module packages that define such errors.
type coder interface{ Code() int }

// ExitCode maps an error to the CLI exit code contract: nil = 0,
// UsageError = 2, an error implementing coder returns its own code, a
// deadline that expired = 3, anything else = 1.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		return ExitUsage
	}
	// Before the timeout branch, so a module that classified a failure
	// itself keeps its code. This is not what holds a hung token
	// exchange at 5: conn wraps that with %v, so the deadline is not in
	// the chain (reversing the branches still gives 5).
	var c coder
	if errors.As(err, &c) {
		return c.Code()
	}
	if isDeadline(err) {
		return ExitTimeout
	}
	return ExitError
}

// isDeadline reports an error produced by a deadline expiring rather
// than by a failure to reach the server.
//
// net.Error.Timeout() covers every shape measured on go1.25.5: a
// response-header stall, a stall mid-body under the client's own Timeout
// (*http.timeoutError), and one under a caller's context deadline
// (context.deadlineExceededError). context.DeadlineExceeded implements
// net.Error with Timeout() true, so a separate errors.Is test would be
// redundant. It is not a string match on "Client.Timeout exceeded",
// because Go has reworded that text between releases.
//
// A caller that reported its own deadline, such as the byoc and managed
// wait loops, has already returned its code from the coder branch. This
// mirrors internal/controlplane/cmd.isTimeout.
func isDeadline(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
