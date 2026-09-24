package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// usageExitUUID is a well-formed database ID, so a case reaches the
// refusal under test rather than stopping at parseUUIDArg.
const usageExitUUID = "7f3a5c1e-3333-4000-8000-000000000abc"

// TestManagedClientSideRefusalsExitTwo is managed's half of the
// PostgREST contract. It is a separate test from byoc's because the
// two implementations are deliberately duplicated over different
// generated types, so a fix to one does not fix the other.
//
// Each case runs with no credentials, so a check left after
// clientFromCmd answers ExitAuth (5) rather than the usage code; the
// positive control proves the runner reaches that 5.
func TestManagedClientSideRefusalsExitTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "postgrest deploy, --db-pool below range",
			args: []string{"database", "postgrest", "deploy", usageExitUUID,
				"--db-schemas", "public", "--db-anon-role", "web_anon",
				"--db-pool", "0"},
			want: "--db-pool must be between 1 and 30",
		},
		{
			name: "postgrest deploy, --max-rows above range",
			args: []string{"database", "postgrest", "deploy", usageExitUUID,
				"--db-schemas", "public", "--db-anon-role", "web_anon",
				"--max-rows", "10001"},
			want: "--max-rows must be between 1 and 10000",
		},
		{
			name: "postgrest deploy, --jwt-secret too short",
			args: []string{"database", "postgrest", "deploy", usageExitUUID,
				"--db-schemas", "public", "--db-anon-role", "web_anon",
				"--jwt-secret", strings.Repeat("a", 31)},
			want: "--jwt-secret must be at least 32 characters",
		},
		{
			name: "postgrest deploy, --db-anon-role explicitly empty",
			args: []string{"database", "postgrest", "deploy", usageExitUUID,
				"--db-schemas", "public", "--db-anon-role", ""},
			want: "--db-anon-role is required to deploy a PostgREST service",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, _, _ := testsupport.NewRuntime(t, "", "text")
			var out bytes.Buffer
			err := runManaged(t, rt, &out, tc.args...)
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

	t.Run("control: a complete postgrest deploy reaches credentials",
		func(t *testing.T) {
			rt, _, _ := testsupport.NewRuntime(t, "", "text")
			var out bytes.Buffer
			err := runManaged(t, rt, &out, "database", "postgrest",
				"deploy", usageExitUUID, "--db-schemas", "public",
				"--db-anon-role", "web_anon")
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
