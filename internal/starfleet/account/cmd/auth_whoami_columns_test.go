package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// printWhoami is a hand-written list of nine labels beside a
// hand-written list of nine values, which is two lists that can drift.
// The run tests cannot see a drift, because they assert
// strings.Contains over bare values: swap the values behind `Tenant:`
// and `Tenant ID:` and both are still present, just under each
// other's label. Three such swaps escaped review on a table PR before
// a position test existed, so this is that test for this block.
//
// Every value is a distinct single token, so a label holding the wrong
// one cannot coincide with the right one.
func TestWhoamiLabelsCarryTheirOwnValues(t *testing.T) {
	report := whoamiReport{
		ClientID:          "cid-token",
		ClientName:        "cname-token",
		ClientRecordID:    "crecord-token",
		ClientDescription: "cdesc-token",
		TenantName:        "tname-token",
		TenantID:          "tid-token",
		Plan:              "plan-token",
		TenantCount:       7,
		APIURL:            "url-token",
	}
	want := map[string]string{
		"Client ID":     "cid-token",
		"Client name":   "cname-token",
		"Description":   "cdesc-token",
		"Client record": "crecord-token",
		"Tenant":        "tname-token",
		"Tenant ID":     "tid-token",
		"Plan":          "plan-token",
		"Tenants":       "7",
		"API URL":       "url-token",
	}

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := printWhoami(rt, report, false, false); err != nil {
		t.Fatalf("printWhoami: %v", err)
	}

	got := map[string]string{}
	for _, line := range strings.Split(
		strings.TrimRight(out.String(), "\n"), "\n") {
		label, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("line %q carries no label", line)
		}
		got[label] = strings.TrimSpace(value)
	}

	for label, wantValue := range want {
		if got[label] != wantValue {
			t.Errorf("%q carries %q, want %q",
				label, got[label], wantValue)
		}
	}
	// Every line printed is a line the map accounts for, so a tenth
	// label cannot appear unnoticed.
	if len(got) != len(want) {
		t.Errorf("printed %d labels, want %d: %q",
			len(got), len(want), out.String())
	}

	// Padding is part of the output, not decoration: the labels differ
	// in length, so each format string pads its own line and a value
	// column that lines up is nine literals agreeing. TrimSpace above
	// deliberately ignores that, which leaves it to this check.
	for _, line := range strings.Split(
		strings.TrimRight(out.String(), "\n"), "\n") {
		if col := whoamiValueStart(line); col != whoamiValueColumn {
			t.Errorf("value starts at column %d, want %d: %q",
				col, whoamiValueColumn, line)
		}
	}
}

// whoamiValueStart is the index at which line's value begins: past the
// colon, then past the padding. Measured this way rather than by
// searching for the value text, because the index of a substring
// INSIDE the value is not where the value starts.
func whoamiValueStart(line string) int {
	after := strings.Index(line, ":") + 1
	rest := line[after:]
	return after + len(rest) - len(strings.TrimLeft(rest, " "))
}

// whoamiValueColumn is where every value in the block begins, measured
// from the longest label ("Client record:" plus one space).
const whoamiValueColumn = 15

// The trial suffix is a second format string rather than a second
// line, so it is the one value that does not stand alone on its label.
func TestWhoamiTrialSuffixSitsOnThePlanLine(t *testing.T) {
	for _, tc := range []struct {
		name  string
		trial bool
		want  string
	}{
		{"trial", true, "Plan:          plan-token (trial)"},
		{"not trial", false, "Plan:          plan-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			report := whoamiReport{
				Plan: "plan-token", PlanTrial: tc.trial,
				APIURL: "url-token",
			}
			if err := printWhoami(rt, report, false, false); err != nil {
				t.Fatalf("printWhoami: %v", err)
			}
			var planLine string
			for _, line := range strings.Split(out.String(), "\n") {
				if strings.HasPrefix(line, "Plan:") {
					planLine = line
				}
			}
			if planLine != tc.want {
				t.Errorf("plan line = %q, want %q", planLine, tc.want)
			}
		})
	}
}
