package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// The threshold arithmetic, exhaustively, because the module tests
// cannot vary Def and Cap: each module's table is fixed, so the
// Def == 0 branch in particular is unreachable from byoc (every byoc
// Def is positive) and reachable from managed only for one verb.
func TestPrintTruncationHintThreshold(t *testing.T) {
	cases := []struct {
		name        string
		resultCount int
		limit       int
		pd          PageDefaults
		wantHint    bool
	}{
		// --limit unset: the default page is the threshold.
		{"full default page", 10, 0, PageDefaults{Def: 10, Cap: 100}, true},
		{"short default page", 9, 0, PageDefaults{Def: 10, Cap: 100}, false},
		{"over the default", 11, 0, PageDefaults{Def: 10, Cap: 100}, true},

		// --limit set: the caller's own value is the threshold.
		{"full explicit page", 3, 3, PageDefaults{Def: 10, Cap: 100}, true},
		{"short explicit page", 2, 3, PageDefaults{Def: 10, Cap: 100}, false},

		// A cap below the asked-for limit lowers the threshold, which
		// is the silent-clamp case: --limit 200 comes back as 100 and
		// nothing else would say so.
		{"clamped to the cap", 100, 200, PageDefaults{Def: 10, Cap: 100}, true},
		{"under a lowered cap", 99, 200, PageDefaults{Def: 10, Cap: 100}, false},

		// No known cap: only the caller's limit bounds the page.
		{"no cap, full page", 100, 100, PageDefaults{Def: 100, Cap: 0}, true},
		{"no cap, short page", 50, 100, PageDefaults{Def: 100, Cap: 0}, false},

		// Def 0 means the endpoint applies no default page, so an
		// unbounded read is the WHOLE result and claiming "there may
		// be more" would be false. This is managed `database list`.
		// Without the guard, every non-empty page would hint.
		{"no default page, one row", 1, 0, PageDefaults{}, false},
		{"no default page, many rows", 1000, 0, PageDefaults{}, false},
		// ...but an explicit --limit still gives a threshold there.
		{"no default page, explicit limit met", 5, 5, PageDefaults{}, true},
		{"no default page, explicit limit unmet", 4, 5, PageDefaults{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var errb bytes.Buffer
			rt := &module.Runtime{Stderr: &errb}
			PrintTruncationHint(rt, tc.resultCount, tc.limit, tc.pd,
				"results")
			got := errb.String()
			if tc.wantHint {
				if !strings.Contains(got, "Showing first") {
					t.Errorf("no hint; stderr = %q", got)
				}
				if !strings.Contains(got, "--limit/--offset") {
					t.Errorf("hint names no remedy; stderr = %q", got)
				}
			} else if got != "" {
				t.Errorf("unexpected hint %q", got)
			}
		})
	}
}

// The count in the message is the ROWS RETURNED, not the threshold.
// A hint reading "Showing first 100" for a clamped --limit 200 tells
// the caller what they got; one reading 200 would tell them what they
// asked for, which they already know.
//
// THE SECOND CASE IS THE ONE THAT TESTS THIS. Review proved the first
// one alone is inert: with resultCount=100, limit=200 and Cap=100 the
// threshold is min(200,100)=100, which EQUALS resultCount, so printing
// `threshold` instead passes. Every other case in this file also builds
// a body of exactly Def or exactly Cap rows, so the two coincide
// everywhere and the mutation was green across all three packages.
//
// The live shape it would have missed: byoc `backup-repository get`
// carries backupInfoDefaults{Def: 100, Cap: 0}, so 150 nested backups
// with no --limit print 150 rows on stdout under a hint claiming 100.
func TestPrintTruncationHintNamesTheRowsReturned(t *testing.T) {
	t.Run("clamped page, count equals threshold", func(t *testing.T) {
		var errb bytes.Buffer
		rt := &module.Runtime{Stderr: &errb}
		PrintTruncationHint(rt, 100, 200,
			PageDefaults{Def: 25, Cap: 100}, "results")
		if got := errb.String(); !strings.Contains(
			got, "first 100 results") {
			t.Errorf("stderr = %q, want the returned count (100)", got)
		}
	})

	t.Run("count EXCEEDS the threshold", func(t *testing.T) {
		// 11 rows against a default page of 10 and no cap: the two
		// values differ, so the message can only be right by naming
		// the rows it got.
		var errb bytes.Buffer
		rt := &module.Runtime{Stderr: &errb}
		PrintTruncationHint(rt, 11, 0, PageDefaults{Def: 10}, "results")
		got := errb.String()
		if !strings.Contains(got, "first 11 results") {
			t.Errorf("stderr = %q, want the 11 rows returned rather "+
				"than the threshold of 10", got)
		}
		if strings.Contains(got, "first 10 results") {
			t.Errorf("stderr = %q names the THRESHOLD, not the rows "+
				"returned", got)
		}
	})
}

// noun reaches the message. byoc's nested backup array passes
// "backups", and a hint saying "results" there would name the wrong
// collection on a verb whose top-level result is one repository.
func TestPrintTruncationHintUsesTheNoun(t *testing.T) {
	var errb bytes.Buffer
	rt := &module.Runtime{Stderr: &errb}
	PrintTruncationHint(rt, 100, 0, PageDefaults{Def: 100}, "backups")
	if got := errb.String(); !strings.Contains(got, "100 backups") {
		t.Errorf("stderr = %q, want the noun", got)
	}
}

// The json guard is structural, so it must be tested as behaviour and
// not inferred from the doc comment. Review's point: the three managed
// "json mode unaffected" subtests cover today's three verbs only, so a
// fourth could hint in json mode with nothing red.
func TestPrintTruncationHintIsSilentInMachineOutput(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			var errb bytes.Buffer
			rt := &module.Runtime{
				Stderr: &errb,
				Output: &output.Renderer{Format: format},
			}
			PrintTruncationHint(rt, 100, 0, PageDefaults{Def: 100},
				"results")
			if got := errb.String(); got != "" {
				t.Errorf("%s mode printed %q; stdout must stay "+
					"parseable and stderr must stay empty", format, got)
			}
		})
	}
	// The control: the same call in text mode DOES hint, so the test
	// above cannot pass by the hint being broken outright.
	var errb bytes.Buffer
	rt := &module.Runtime{
		Stderr: &errb,
		Output: &output.Renderer{Format: "text"},
	}
	PrintTruncationHint(rt, 100, 0, PageDefaults{Def: 100}, "results")
	if !strings.Contains(errb.String(), "Showing first") {
		t.Errorf("text mode printed %q; the guard has swallowed "+
			"everything and the cases above prove nothing",
			errb.String())
	}
}

// The Def-0 branch is the one worth pinning: "(API default 0)" would
// read as a page of nothing, where Def 0 means the endpoint applies no
// default page at all. No shipping verb passes such a table today —
// managed database list states its bounds instead — so nothing else
// exercises it.
func TestLimitFlagHelp(t *testing.T) {
	for _, tc := range []struct {
		name string
		pd   PageDefaults
		want string
	}{
		{
			name: "a default page names its size",
			pd:   PageDefaults{Def: 25, Cap: 100},
			want: "Maximum number of results to return (API default 25)",
		},
		{
			name: "no default page names no size",
			pd:   PageDefaults{},
			want: "Maximum number of results to return",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := LimitFlagHelp(tc.pd); got != tc.want {
				t.Errorf("LimitFlagHelp(%+v) = %q, want %q",
					tc.pd, got, tc.want)
			}
		})
	}
}
