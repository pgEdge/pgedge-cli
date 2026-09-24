package conn

import (
	"errors"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestByocBusyPhraseSeparatesDatabaseFromCluster pins the discrimination
// the whole phrase choice rests on (#204).
//
// byocBusyStatusPhrase's own comment carries the census; this is the
// executable half of it.
//
// The bodies are the literals from saas's database_service.go at the
// revision openapi/SOURCE pins, typo included. They are duplicated here
// because the CLI cannot import them, and two independent statements of
// the same fact are what make a divergence visible.
func TestByocBusyPhraseSeparatesDatabaseFromCluster(t *testing.T) {
	testsupport.ClearEnv(t)

	const prefix = `{"code":400,"message":"`

	tests := []struct {
		name     string
		message  string
		wantBusy bool
	}{
		{
			// The one producer keyed on the database. Live-verified on
			// a BYOC dev tenant against two `failed` databases and one `deleting`
			// database on a `failed` cluster.
			name: "rotate-password, database status",
			message: "database update cannot be completed." +
				" database is not in available status",
			wantBusy: true,
		},
		{
			name: "rotate-password, cluster status",
			message: "database update cannot be completed." +
				" cluster is not in available status",
		},
		{
			name: "database restore, cluster status",
			message: "database restore cannot be completed." +
				" cluster is not in available status",
		},
		{
			// Three producers carry `in not in`, sic. A phrase keyed on
			// the intended spelling would miss them; one keyed on the
			// typo would miss the other two. Naming `database` sidesteps
			// both, which is the point.
			name: "database creation, cluster status, typo",
			message: "database creation cannot be completed." +
				" cluster in not in available status",
		},
		{
			name: "database update, cluster status, typo",
			message: "database update cannot be completed." +
				" cluster in not in available status",
		},
		{
			name: "database deletion, cluster status, typo",
			message: "database deletion cannot be completed." +
				" cluster in not in available status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := prefix + tt.message + `"}`
			err := CheckResponse(400, body)
			if err == nil {
				t.Fatal("a 400 must always produce an error")
			}
			gotBusy := strings.Contains(err.Error(), "resource is busy")
			if gotBusy != tt.wantBusy {
				t.Errorf("classified as busy = %v, want %v\n"+
					"message: %s\nrendered: %v",
					gotBusy, tt.wantBusy, tt.message, err)
			}
			// Whichever branch it takes, the server's own words survive:
			// on the cluster branch they are the only thing telling the
			// caller the fault is the cluster's.
			if !strings.Contains(err.Error(), "available status") {
				t.Errorf("the server's message did not survive: %v", err)
			}
			// Both branches are ExitGeneral, so without this the test
			// cannot tell an exit-code regression from a message one.
			var ee *ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("not an *ExitError: %v", err)
			}
			if ee.Code() != ExitGeneral {
				t.Errorf("exit code = %d, want ExitGeneral (%d)",
					ee.Code(), ExitGeneral)
			}
		})
	}
}

// TestByocBusyPhraseIsNotSubstringOfTheClusterOnes is the direct
// statement of the trap: the shorter phrase the issue considered first
// matches every one of these. Three spellings are listed rather than
// all five producers, because the other two differ only by verb.
func TestByocBusyPhraseIsNotSubstringOfTheClusterOnes(t *testing.T) {
	clusterMessages := []string{
		"database update cannot be completed." +
			" cluster is not in available status",
		"database restore cannot be completed." +
			" cluster is not in available status",
		"database creation cannot be completed." +
			" cluster in not in available status",
	}
	for _, m := range clusterMessages {
		if strings.Contains(m, byocBusyStatusPhrase) {
			t.Errorf("byocBusyStatusPhrase %q matches a CLUSTER-status "+
				"message: %q", byocBusyStatusPhrase, m)
		}
		// The control: the phrase the issue rejected does match, which
		// is why this test exists rather than a simpler one.
		if !strings.Contains(m, "not in available status") {
			t.Errorf("control failed — %q no longer carries the shorter "+
				"phrase, so this test proves nothing", m)
		}
	}
}
