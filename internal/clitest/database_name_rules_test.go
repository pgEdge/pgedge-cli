package clitest

import (
	"io"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
)

// The byoc and managed database-name rules are deliberately different,
// and this is the only gate that can see both.
//
// They cannot be compared inside either module: each validator is
// unexported and lives in its own package, so a test beside one of them
// can assert its rule but never that the OTHER rule still differs. An
// earlier version of this gate lived in internal/starfleet/byoc/cmd and
// called only the byoc validator — it would have stayed green if the
// managed rule had been widened to match, which is precisely the drift
// it claimed to prevent. This package exists to hold checks that no
// single module can make (see TestExitCodeVocabulariesAgree), so the
// comparison belongs here, and it is made through the shipped command
// tree rather than through symbols neither package exports.
//
// The rules differ because saas's do:
//
//   - byoc names go through pgutil.ValidateDatabaseName. A byoc
//     database name is a Postgres identifier and nothing else: up to 63
//     bytes, leading letter or underscore, letters/digits/underscores.
//   - managed names go through ValidateK8sCompatibleDatabaseName, which
//     layers RFC-1123 label rules on top because the name is also a
//     Kubernetes object name: capped at 50 to leave room for
//     CNPG-derived children, and no underscores, since k8s names use
//     hyphens.
//
// Underscores and case are where that is visible from the CLI.
func TestByocAndManagedDatabaseNameRulesDiverge(t *testing.T) {
	tests := []struct {
		name  string
		input string
		why   string
	}{
		{
			name:  "underscore",
			input: "my_db",
			why: "an underscore is a legal Postgres identifier " +
				"character but not a legal k8s label character",
		},
		{
			name:  "leading underscore",
			input: "_internal",
			why: "byoc allows a leading underscore; a k8s name must " +
				"start alphanumeric",
		},
		{
			name:  "unicode letter",
			input: "café",
			why: "byoc mirrors saas's unicode.IsLetter; the managed " +
				"check narrows to ASCII",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			byoc := nameExitCode(t, "starfleet", "byoc", "database", "create",
				"--name", tt.input,
				"--cluster-id", "3fa85f64-5717-4562-b3fc-2c963f66afa6")
			managed := nameExitCode(t, "starfleet", "managed", "database",
				"create", "--name", tt.input,
				"--region", "us-east-1", "--size", "large")

			if byoc == cli.ExitUsage {
				t.Errorf("byoc rejected %q as malformed, but its API "+
					"accepts it (%s)", tt.input, tt.why)
			}
			if managed != cli.ExitUsage {
				t.Errorf("managed accepted %q (exit %d), but its API "+
					"refuses it (%s) — if the managed rule was widened "+
					"on purpose, this gate and both SKILL.md files need "+
					"updating together", tt.input, managed, tt.why)
			}
		})
	}
}

// TestByocAndManagedDatabaseNameRulesAgreeWhereTheyShould is the
// positive control. Without it the test above passes for a byoc
// validator that accepts everything, including names both APIs refuse.
func TestByocAndManagedDatabaseNameRulesAgreeWhereTheyShould(t *testing.T) {
	rejected := []string{"my-db", "1mydb", "my db", "   "}
	for _, name := range rejected {
		byoc := nameExitCode(t, "starfleet", "byoc", "database", "create",
			"--name", name,
			"--cluster-id", "3fa85f64-5717-4562-b3fc-2c963f66afa6")
		managed := nameExitCode(t, "starfleet", "managed", "database",
			"create", "--name", name,
			"--region", "us-east-1", "--size", "large")
		if byoc != cli.ExitUsage {
			t.Errorf("byoc accepted %q (exit %d); both APIs refuse it",
				name, byoc)
		}
		if managed != cli.ExitUsage {
			t.Errorf("managed accepted %q (exit %d); both APIs refuse it",
				name, managed)
		}
	}

	// And one both accept. Asserting the code EXACTLY — conn.ExitAuth,
	// reached because the isolated HOME has no credentials — rather
	// than merely "not ExitUsage" is what makes this a control: exit 1
	// would satisfy the looser check, so a name that died at flag
	// parsing rather than passing validation would read as accepted.
	const good = "mydb"
	if c := nameExitCode(t, "starfleet", "byoc", "database", "create",
		"--name", good,
		"--cluster-id", "3fa85f64-5717-4562-b3fc-2c963f66afa6"); c != conn.ExitAuth {
		t.Errorf("byoc %q gave exit %d, want %d — it should pass "+
			"validation and then fail for want of credentials",
			good, c, conn.ExitAuth)
	}
	if c := nameExitCode(t, "starfleet", "managed", "database", "create",
		"--name", good, "--region", "us-east-1",
		"--size", "large"); c != conn.ExitAuth {
		t.Errorf("managed %q gave exit %d, want %d — it should pass "+
			"validation and then fail for want of credentials",
			good, c, conn.ExitAuth)
	}
}

// nameExitCode runs one verb against the shipped tree under an
// isolated HOME with no credentials, and returns its exit code.
//
// No credentials is the point rather than a shortcut: a name the CLI
// accepts gets as far as credential resolution and fails there (exit
// 5), while a name it rejects never reaches it (exit 2). The two
// outcomes are distinguishable without a server, and no request is made
// either way.
func nameExitCode(t *testing.T, args ...string) int {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	rt := &module.Runtime{}
	root := cli.NewRootCmd(rt)
	for _, m := range Modules() {
		c, err := m.Command(rt)
		if err != nil {
			t.Fatal(err)
		}
		root.AddCommand(c)
	}
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	// cli.ExitCode, not a local reimplementation of it: this is the
	// same mapping main.go applies to an error returned from a run
	// hook, so anything else here would test a mapping the binary does
	// not use. (main.go additionally promotes ExitError to ExitUsage
	// when no run hook ran at all — a class no case in this file
	// reaches.) A first draft handled the Code() interface but not
	// *cli.UsageError, which ExitCode also maps to 2.
	return cli.ExitCode(root.Execute())
}
