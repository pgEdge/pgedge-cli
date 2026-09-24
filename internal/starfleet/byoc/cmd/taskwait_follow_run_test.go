package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestTaskWaitFollow mirrors the managed test of the same name, because
// byoc's task wait is the same command over the same task resource and
// had the same gap (#179): attaching to work this CLI did not start
// reported bare status polls while the step messages sat unreachable in
// the payload.
//
// The two cases are asserted as a PAIR: messages appearing under
// --follow proves the flag acts, and the status line surviving without
// it proves the default output is unchanged. Either alone would pass on
// a version that always streamed.
func TestTaskWaitFollow(t *testing.T) {
	messages := []string{
		"Stop Monitoring: running (5%)",
		"Stop Monitoring: succeeded (15%)",
		"Removing Nodes: running (65%)",
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
				followFlowHandler("succeeded"))

			args := []string{"task", "wait", "e9562e00-c8f8-438b-860a-b5eb427f88d8",
				"--wait-interval", "1", "--wait-timeout", "30"}
			if tt.follow {
				args = append(args, "--follow")
			}

			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("task wait: %v", err)
			}

			s := errb.String()
			// Asserted first, so a run that never polled cannot pass
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
