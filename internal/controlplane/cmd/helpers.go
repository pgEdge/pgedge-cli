package cmd

import (
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
)

// emitAccepted is the one place a task-spawning mutation puts its
// accepted response on stdout, and only under -o json/-o yaml; the
// human-facing lines stay on rt.Stderr. The nil check misses a typed
// nil boxed into `any`, so the call sites' JSON200 guard is the real
// defence.
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
