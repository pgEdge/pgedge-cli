package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// byocTaskWithSteps is a task carrying a step trace. taskBody's
// messages array is empty, which is what let the detail block ship
// unnoticed on the byoc side — a fixture with no messages cannot tell
// a printed step from an unprinted one.
const byocTaskWithSteps = `[{"id":"7db8f401-0d8d-4daf-889b-f721e395df61","name":"deploy-cluster",
	"status":"failed","subject_id":"cluster-1","subject_kind":"cluster",
	"error":"\n  node unreachable\n\n",
	"messages":[
	{"level":"info","progress":10,"status":"succeeded",
	 "step":"provision","text":"Provisioning Hosts",
	 "time":"2024-03-15T10:30:00Z"},
	{"level":"error","progress":40,"status":"failed",
	 "step":"bootstrap","text":"Bootstrapping Cluster",
	 "time":"2024-03-15T10:34:00Z"}],
	"created_at":"2024-03-15T10:30:00Z",
	"updated_at":"2024-03-15T10:34:00Z"}]`

// TestByocTaskGetShowsDetail is the byoc half of the task-detail check. The two modules
// keep separate copies of printTaskDetail because each binds its own
// generated api.Task, so each needs its own proof: a fix applied to one
// tree is invisible to the other's test.
func TestByocTaskGetShowsDetail(t *testing.T) {
	t.Run("latest step, progress and reason", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, byocTaskWithSteps))
		if err := runAuthed(t, rt, out, url,
			"task", "get", "7db8f401-0d8d-4daf-889b-f721e395df61"); err != nil {
			t.Fatalf("task get: %v", err)
		}
		s := out.String()

		for _, want := range []string{
			"Bootstrapping Cluster",
			"40%",
			"node unreachable",
			"[error]",
			"2024-03-15T10:34:00Z",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("detail block is missing %q:\n%s", want, s)
			}
		}

		// Only the newest message; printing the whole trace would
		// satisfy every assertion above.
		if strings.Contains(s, "Provisioning Hosts") {
			t.Errorf("printed an earlier step:\n%s", s)
		}

		// The fixture's reason is padded, so this also pins the
		// TrimSpace. Without padding the assertions above cannot tell a
		// trimmed reason from an untrimmed one — and a fix applied to
		// managed's copy is invisible here, which is the whole reason
		// the two trees test separately.
		if strings.Contains(s, "\n\n\n") {
			t.Errorf("the padding was not trimmed:\n%q", s)
		}
		if !strings.HasSuffix(s, "node unreachable\n") {
			t.Errorf("trailing padding survived:\n%q", s)
		}
	})

	t.Run("no messages, no step line", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[`+taskBody("succeeded")+`]`))
		if err := runAuthed(t, rt, out, url,
			"task", "get", "7db8f401-0d8d-4daf-889b-f721e395df61"); err != nil {
			t.Fatalf("task get: %v", err)
		}
		if strings.Contains(out.String(), "Step") {
			t.Errorf("empty messages produced a Step line:\n%s",
				out.String())
		}
	})
}
