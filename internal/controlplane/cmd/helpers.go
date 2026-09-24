package cmd

import (
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
)

// emitAccepted writes payload to stdout under -o json/-o yaml, and
// does nothing under text/table. It is the one place a task-spawning
// mutation puts its accepted API response on the machine channel —
// stdout is for structured data a script can capture; rt.Stderr is
// for the human-facing acceptance line and any progress/terminal
// verdict that follows, which stay exactly where they already are.
//
// A nil payload is never written: every call site already guards with
// `resp.JSON200 == nil` (a 200 with no decodable body), and this check
// is belt-and-braces should a future call site forget that guard. It
// will not catch a *typed* nil boxed into `any` (e.g. a nil *api.Task)
// — the guard at the call site is the real defence.
func emitAccepted(rt *module.Runtime, payload any) error {
	if !rt.Output.Structured() || payload == nil {
		return nil
	}
	return rt.Output.Print(payload, nil)
}

// joinStrings joins a slice with ", ".
func joinStrings(ss []string) string { return strings.Join(ss, ", ") }

// formatDate renders an ISO timestamp as its date portion (YYYY-MM-DD).
func formatDate(t time.Time) string { return t.Format("2006-01-02") }

// derefOr returns *p, or fallback when p is nil or points at "".
func derefOr(p *string, fallback string) string {
	if p == nil || *p == "" {
		return fallback
	}
	return *p
}
