package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
)

// fullPageRows is the size of the stub page below.
//
// It was FIVE, and review measured what that cost: on a bare read the
// hint's threshold is the endpoint's default page, so five rows could
// not have tripped a hint for any default above five — i.e. for any
// value cp would plausibly adopt. The bare subtest passed under the
// mutation that was supposed to redden it, and the PR body claimed
// both subtests failed. 30 is above the 25 that byoc and managed both
// apply to their own task lists.
const fullPageRows = 30

// tasksPage returns a page of n completed tasks.
func tasksPage(n int) string {
	rows := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, fmt.Sprintf(
			`{"task_id":"%08d-1111-1111-1111-111111111111",`+
				`"status":"completed","type":"create",`+
				`"scope":"database","entity_id":"db1",`+
				`"created_at":"2025-06-18T00:00:00Z"}`, i))
	}
	return `{"tasks":[` + strings.Join(rows, ",") + `]}`
}

// TestCPPrintsNoTruncationHintAndTheReferencesSaySo binds a claim TWO
// documents make about this module to what this module does.
//
// The claim had gone stale before anything held it: the index said
// `managed database list` was the ONE verb printing no hint on an
// unbounded read, while `controlplane task list` printed none on ANY read — and
// controlplane's own reference sends the reader to that very paragraph for the
// paging contract. A cross-module sentence with nothing bound to it is
// how that happens, so the sentence is bound here rather than left to
// the next reader.
//
// This does NOT assert cp should keep behaving this way. If cp gains
// the hint, this reddens and names the two sentences that then have to
// change, which is the whole job.
func TestCPPrintsNoTruncationHintAndTheReferencesSaySo(t *testing.T) {
	// Behavioural rather than a grep over this package: a grep for
	// PrintTruncationHint survives an alias but not a wrapper, and the
	// question is what the command PRINTS.
	for _, args := range [][]string{
		// Def-DEPENDENT, and therefore the weaker of the two: the
		// threshold on a bare read is the endpoint's default page, so
		// this can only see a hint for a default in 0 < Def <= 30:
		// above 30 the stub is too short to cross the threshold, and
		// Def <= 0 is silent by design. It covers the omitted-flag
		// path and nothing more.
		{"task", "list"},
		// Def- and Cap-INDEPENDENT, which is why it is here. With
		// --limit equal to the row count the threshold is
		// min(30, Cap), and 30 rows meets it for every Cap — so any
		// hint implementation at all fires on this read.
		{"task", "list", "--limit", strconv.Itoa(fullPageRows)},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			rt, out, errb := newTestRuntime(t, "", "text")
			url := newServer(t, jsonHandler(200, tasksPage(fullPageRows)))
			if err := runControlplane(t, rt, out, url, args...); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			// Both buffers: runControlplane points cobra's own error writer at
			// stdout while rt.Stderr is separate, so checking one only
			// would be checking the wrong half half the time.
			if got := out.String() + errb.String(); strings.Contains(
				got, "Showing first",
			) {
				t.Errorf("%v printed a truncation hint; "+
					"internal/controlplane/llms.txt and the index both state "+
					"that cp prints none, and both sentences are now "+
					"wrong: %q", args, got)
			}

			// POSITIVE CONTROL. Without it a broken observation
			// channel — the wrong buffer, a Runtime that renders
			// nowhere — makes the negative above pass for the wrong
			// reason. The control writes a hint through the same
			// mechanism byoc and managed use, onto the same Runtime,
			// and the negative is only worth anything if this lands.
			cli.PrintTruncationHint(rt, fullPageRows, fullPageRows,
				cli.PageDefaults{
					Def: fullPageRows, Cap: fullPageRows,
				}, "tasks")
			if !strings.Contains(
				out.String()+errb.String(), "Showing first",
			) {
				t.Fatalf("the control hint did not land, so this test " +
					"cannot observe a hint at all and its negative " +
					"above proves nothing")
			}
		})
	}

	// And the two sentences. Whitespace is collapsed because both are
	// hard-wrapped, and a wrap falling inside the phrase would
	// otherwise read as an absence.
	for _, doc := range []struct{ path, want string }{
		{
			filepath.Join("..", "llms.txt"),
			"A full `task list` page may be truncated",
		},
		{
			filepath.Join("..", "..", "..", "llms.txt"),
			"A full `controlplane task list` page may be truncated",
		},
	} {
		raw, err := os.ReadFile(doc.path)
		if err != nil {
			t.Fatalf("read %s: %v", doc.path, err)
		}
		flat := strings.Join(strings.Fields(string(raw)), " ")
		if !strings.Contains(flat, doc.want) {
			t.Errorf("%s does not say %q; cp prints no hint, so the "+
				"reference has to say a full page may be truncated",
				doc.path, doc.want)
		}
	}
}
