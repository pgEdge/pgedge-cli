package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// usageExitUUID is a well-formed tenant/client ID, so a case reaches
// the refusal under test rather than stopping at ParseUUIDArg.
const usageExitUUID = "7f3a5c1e-2222-4000-8000-000000000abc"

// TestAccountClientSideRefusalsExitTwo pins the exit code and the
// ordering for the account tree's "nothing to update" refusals. Both
// run with no credentials, so a check that drifted after
// clientFromCmd would answer ExitAuth (5); the positive control
// proves the runner reaches that 5.
func TestAccountClientSideRefusalsExitTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "tenant update with no --name",
			args: []string{"tenant", "update", usageExitUUID},
			want: "nothing to update — pass --name",
		},
		{
			name: "client update with no flags",
			args: []string{"client", "update", usageExitUUID},
			want: "nothing to update — pass --name and/or --description",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, _, _ := testsupport.NewRuntime(t, "", "text")
			var out bytes.Buffer
			err := runAccount(t, rt, &out, tc.args...)
			if err == nil {
				t.Fatal("want a refusal, got nil")
			}
			var ee *conn.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("err = %v, want *conn.ExitError", err)
			}
			if ee.Code() != conn.ExitUsage {
				t.Errorf("code = %d, want ExitUsage (%d); err = %v",
					ee.Code(), conn.ExitUsage, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to contain %q",
					err.Error(), tc.want)
			}
		})
	}

	t.Run("control: a complete tenant update reaches credentials",
		func(t *testing.T) {
			rt, _, _ := testsupport.NewRuntime(t, "", "text")
			var out bytes.Buffer
			err := runAccount(t, rt, &out, "tenant", "update",
				usageExitUUID, "--name", "acme")
			if err == nil {
				t.Fatal("want a credential failure, got nil")
			}
			var ee *conn.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("err = %v, want *conn.ExitError", err)
			}
			if ee.Code() != conn.ExitAuth {
				t.Fatalf("code = %d, want ExitAuth (%d); err = %v",
					ee.Code(), conn.ExitAuth, err)
			}
		})
}
