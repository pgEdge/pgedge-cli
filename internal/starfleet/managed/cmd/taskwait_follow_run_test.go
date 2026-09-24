package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestTaskWaitFollow pins that attaching to a task shows what launching
// it would have shown. `task wait` is the only way to observe work this
// CLI did not start, and it used to print bare status polls while the
// step messages sat unreachable in the payload.
//
// The two cases are asserted as a PAIR on purpose: the messages
// appearing under --follow proves the flag does something, and the
// status line surviving without it proves the default output did not
// change. Either assertion alone would pass on a version that simply
// always streamed.
//
// followFlowHandler is reused from the mutating verbs' --follow test,
// so both paths are driven against identical task payloads — which is
// the claim being made, that attaching is no longer the poorer view.
func TestTaskWaitFollow(t *testing.T) {
	// Messages the handler accumulates across polls. Each must appear
	// exactly once: the cursor is what makes that true, and a
	// per-poll re-print duplicates the first.
	messages := []string{
		"Configuring System: running (5%)",
		"Configuring System: succeeded (15%)",
		"Provisioning Database: running (65%)",
	}

	tests := []struct {
		name         string
		follow       bool
		wantMessages bool
		wantPolls    bool
	}{
		{
			name:         "--follow streams the step messages",
			follow:       true,
			wantMessages: true,
			wantPolls:    false,
		},
		{
			name:         "without --follow the bare status polls remain",
			follow:       false,
			wantMessages: false,
			wantPolls:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				followFlowHandler("succeeded", ""))

			args := []string{"task", "wait", "e9562e00-c8f8-438b-860a-b5eb427f88d8",
				"--wait-interval", "1", "--wait-timeout", "30"}
			if tt.follow {
				args = append(args, "--follow")
			}

			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("task wait: %v", err)
			}

			s := errb.String()
			// The terminal line is unconditional, and asserting it
			// first means a run that never polled at all cannot pass
			// the rest by vacuous absence.
			if !strings.Contains(s, "Task e9562e00-c8f8-438b-860a-b5eb427f88d8: succeeded") {
				t.Fatalf("no terminal status line:\n%s", s)
			}

			for _, m := range messages {
				n := strings.Count(s, m)
				switch {
				case tt.wantMessages && n != 1:
					t.Errorf("message %q appeared %d times, want 1:\n%s",
						m, n, s)
				case !tt.wantMessages && n != 0:
					t.Errorf("message %q leaked without --follow:\n%s",
						m, s)
				}
			}

			gotPolls := strings.Contains(s, "Task e9562e00-c8f8-438b-860a-b5eb427f88d8: running...")
			if gotPolls != tt.wantPolls {
				t.Errorf("bare status polls present = %v, want %v:\n%s",
					gotPolls, tt.wantPolls, s)
			}
		})
	}
}
