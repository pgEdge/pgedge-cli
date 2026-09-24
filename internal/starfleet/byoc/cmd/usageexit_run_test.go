package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// usageExitUUID is a well-formed database/cluster/repository ID, so a
// case reaches the refusal under test rather than stopping at
// parseUUIDArg.
const usageExitUUID = "7f3a5c1e-1111-4000-8000-000000000abc"

// TestByocClientSideRefusalsExitTwo pins the exit code AND the
// ordering for byoc's pre-request refusals. Each case runs with no
// credentials and no --api-url, so a check that sits after
// clientFromCmd answers ExitAuth (5) for "no credentials found"
// instead of the usage code the mistake deserves. The positive
// control is what proves the runner would surface that 5.
func TestByocClientSideRefusalsExitTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "backup-repository get, unknown --type",
			args: []string{"backup-repository", "get", usageExitUUID,
				"n1", "--type", "sideways"},
			want: `unknown backup type "sideways": must be full, diff, or incr`,
		},
		{
			name: "cluster update with no flags",
			args: []string{"cluster", "update", usageExitUUID},
			want: "cluster update: specify at least one of " +
				"--firewall-rule, --backup-store-id, --regions",
		},
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
			// MarkFlagRequired tests Changed and nothing else, so an
			// explicitly empty value satisfies it and reaches the
			// completeness check.
			name: "postgrest deploy, --db-schemas explicitly empty",
			args: []string{"database", "postgrest", "deploy", usageExitUUID,
				"--db-schemas", "", "--db-anon-role", "web_anon"},
			want: "--db-schemas is required to deploy a PostgREST service",
		},
		{
			name: "postgrest update, --db-pool below range",
			args: []string{"database", "postgrest", "update", usageExitUUID,
				"--db-pool", "0"},
			want: "--db-pool must be between 1 and 30",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, _, _ := testsupport.NewRuntime(t, "", "text")
			var out bytes.Buffer
			err := runByoc(t, rt, &out, tc.args...)
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

	// The positive control: the same runner, the same absent
	// credentials, nothing client-side left to refuse. It must reach
	// credential resolution and report ExitAuth, or every case above
	// would pass on a runner that never got that far.
	t.Run("control: a complete cluster update reaches credentials",
		func(t *testing.T) {
			rt, _, _ := testsupport.NewRuntime(t, "", "text")
			var out bytes.Buffer
			err := runByoc(t, rt, &out, "cluster", "update",
				usageExitUUID, "--regions", "us-east-1")
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

	// The same control for postgrest deploy, whose refusals used to
	// sit after clientFromCmd: a complete deploy must still get as far
	// as credentials, so the cases above are measuring the move rather
	// than an argument the tree rejected earlier.
	t.Run("control: a complete postgrest deploy reaches credentials",
		func(t *testing.T) {
			rt, _, _ := testsupport.NewRuntime(t, "", "text")
			var out bytes.Buffer
			err := runByoc(t, rt, &out, "database", "postgrest",
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
