package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

// TestFormatTaskMessage asserts the WHOLE rendered line rather than the
// substrings the streaming tests check, because the reference promises
// these lines are byte-identical across two verbs. A substring
// assertion cannot see the timestamp or the two-space separator, so an
// edit dropping either would pass every other gate while making that
// promise false.
func TestFormatTaskMessage(t *testing.T) {
	const ts = "2026-08-05T20:49:52Z"

	tests := []struct {
		name string
		msg  api.Message
		want string
	}{
		{
			name: "every optional field carried",
			msg: api.Message{
				Level:    "error",
				Progress: intPtr(65),
				Status:   strPtr("running"),
				Step:     strPtr("remove-nodes"),
				Text:     "Removing Nodes",
				Time:     ts,
			},
			want: ts + "  [error] Removing Nodes: running (65%)",
		},
		{
			name: "no optional field carried",
			msg: api.Message{
				Text: "Removing Nodes",
				Time: ts,
			},
			want: ts + "  Removing Nodes",
		},
		{
			// info is the level on an ordinary step — 418 of 419
			// messages measured — so tagging it would put "[info] " on
			// almost every line. The one exception was "error", which
			// is why the tag branch is worth having.
			name: "info level is suppressed",
			msg: api.Message{
				Level: "info",
				Text:  "Removing Nodes",
				Time:  ts,
			},
			want: ts + "  Removing Nodes",
		},
		{
			// Not a shape the API was observed to send: across 419
			// messages status was always present and non-empty. Both
			// specs type it as a string with no minLength, so "" is
			// permitted, and the guard's empty-string half is pinned
			// nowhere else — the nil half is the case above.
			name: "empty status is suppressed",
			msg: api.Message{
				Status: strPtr(""),
				Text:   "Removing Nodes",
				Time:   ts,
			},
			want: ts + "  Removing Nodes",
		},
		{
			// Zero is a value, not a missing one. Also unobserved —
			// measured progress ran 5..100 — but progress is optional
			// in both specs, so nil and 0 must stay distinguishable.
			name: "zero progress renders",
			msg: api.Message{
				Progress: intPtr(0),
				Text:     "Removing Nodes",
				Time:     ts,
			},
			want: ts + "  Removing Nodes (0%)",
		},
		{
			name: "empty text keeps the separator and the suffixes",
			msg: api.Message{
				Progress: intPtr(5),
				Status:   strPtr("running"),
				Time:     ts,
			},
			want: ts + "  : running (5%)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTaskMessage(tt.msg); got != tt.want {
				t.Errorf("formatTaskMessage()\n got %q\nwant %q",
					got, tt.want)
			}
			// task get's detail block prints the same step without the
			// timestamp, so the two renderings must not drift apart.
			step := strings.TrimPrefix(tt.want, tt.msg.Time+"  ")
			if got := formatTaskStep(tt.msg); got != step {
				t.Errorf("formatTaskStep()\n got %q\nwant %q", got, step)
			}
		})
	}
}

// TestFollowStepLinesAreByteIdentical pins the reference's claim that
// `task wait --follow` streams the same step lines as `--follow` on the
// mutating verb. Each verb reaches printNewMessages by its own path, so
// nothing but this comparison stops one of them drifting.
func TestFollowStepLinesAreByteIdentical(t *testing.T) {
	// The literals the shared fixture must render to. Written out in
	// full so this test fails on a format change even if both verbs
	// change together, which a verb-to-verb comparison alone would miss.
	want := []string{
		"2026-08-05T20:49:50Z  Stop Monitoring: running (5%)",
		"2026-08-05T20:49:51Z  Stop Monitoring: succeeded (15%)",
		"2026-08-05T20:49:52Z  Removing Nodes: running (65%)",
	}

	runs := map[string][]string{
		"mutating verb": {"cluster", "delete", testClusterID, "--force"},
		"task wait":     {"task", "wait", "e9562e00-c8f8-438b-860a-b5eb427f88d8"},
	}

	for name, base := range runs {
		t.Run(name, func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				followFlowHandler("succeeded"))

			args := make([]string, 0, len(base)+5)
			args = append(args, base...)
			args = append(args, "--follow",
				"--wait-interval", "1", "--wait-timeout", "30")
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("%s --follow: %v", name, err)
			}

			lines := strings.Split(errb.String(), "\n")
			for _, w := range want {
				n := 0
				for _, got := range lines {
					if got == w {
						n++
					}
				}
				if n != 1 {
					t.Errorf(
						"line %q appeared %d times as a whole line, "+
							"want 1:\n%s", w, n, errb.String())
				}
			}
		})
	}
}
