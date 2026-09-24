package cmd

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// ragConfigCase is one way of getting --pipeline-config wrong.
//
// db is the database GET body the stub serves: deploy's guard needs a
// database with no RAG service, update's needs one that has it.
//
// The path the flag receives is decided by kind, so a case cannot
// silently describe two setups at once.
type ragConfigCase struct {
	name string
	verb string
	db   string
	kind ragConfigPathKind
	body string
}

type ragConfigPathKind int

const (
	// ragPathFile writes body to the path.
	ragPathFile ragConfigPathKind = iota
	// ragPathAbsent leaves the path with no file on it (ENOENT).
	ragPathAbsent
	// ragPathDir makes the path a directory (EISDIR) — a second read
	// failure, and one cp names explicitly.
	ragPathDir
)

// The byoc twin of managed's database_rag_exit_run_test.go. byoc was
// not the module #258 measured, but it shares parsePipelineConfig,
// validatePipelines and the completeness guard line for line, so it
// diverged from cp identically — and byoc's own --network, --node and
// --firewall-rule parsers already answer 2 (#134), which left one
// module reporting two codes for one class of mistake.
func TestRAGPipelineConfigFailuresExitUsage(t *testing.T) {
	const reservedName = `[{"name":"_default","tables":[` +
		`{"table":"public.docs","text_column":"content",` +
		`"vector_column":"embedding"}]}]`

	cases := []ragConfigCase{
		{name: "deploy: file absent", verb: "deploy",
			db: dbNoServiceBody, kind: ragPathAbsent},
		{name: "deploy: path is a directory", verb: "deploy",
			db: dbNoServiceBody, kind: ragPathDir},
		{name: "deploy: unparseable JSON", verb: "deploy",
			db: dbNoServiceBody, body: "not json at all"},
		{name: "deploy: object without a pipelines key", verb: "deploy",
			db: dbNoServiceBody, body: `{"pipeline":[]}`},
		{name: "deploy: parses but holds no pipelines", verb: "deploy",
			db: dbNoServiceBody, body: "[]"},
		{name: "deploy: reserved pipeline name", verb: "deploy",
			db: dbNoServiceBody, body: reservedName},
		{name: "update: file absent", verb: "update",
			db: dbWithRAGBody, kind: ragPathAbsent},
		{name: "update: path is a directory", verb: "update",
			db: dbWithRAGBody, kind: ragPathDir},
		{name: "update: unparseable JSON", verb: "update",
			db: dbWithRAGBody, body: "not json at all"},
		{name: "update: parses but holds no pipelines", verb: "update",
			db: dbWithRAGBody, body: "[]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusOK, tc.db))

			requireExitUsage(t, runAuthed(t, rt, out, url,
				ragArgsWithConfig(tc.verb, testDatabaseID,
					ragConfigPath(t, tc))...))
		})
	}
}

// TestRAGDeployIncompleteFlagsExitUsage covers the completeness guard
// on the same flag set. cobra catches an OMITTED required flag at exit
// 2 already, so on deploy an explicitly empty value is what reaches
// this guard, and reporting the identical mistake as 1 is the
// divergence #258 names, one flag along from the file itself.
func TestRAGDeployIncompleteFlagsExitUsage(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, dbNoServiceBody))

	args := ragArgsWithConfig("deploy", testDatabaseID, ragConfigPath(t,
		ragConfigCase{body: validPipelineJSON}))
	for i, a := range args {
		if a == "--embedding-llm-model" {
			args[i+1] = ""
		}
	}
	requireExitUsage(t, runAuthed(t, rt, out, url, args...))
}

// TestRAGUpdateServedNoPipelinesExitsUsage pins the one way the guard
// fires on a command line with nothing wrong with it: `update` inherits
// the deployed pipelines, so a served rag_config with none leaves the
// guard unable to tell "the API gave me nothing" from "you passed
// nothing", and it names a flag the caller never had to pass.
//
// Reaching it needs a response that violates the spec — pipelines is
// required with minItems 1 — which the CLI decodes leniently rather
// than enforcing. The code is pinned here so that changing it is
// deliberate: if a real response can carry an empty or absent pipelines
// array, the honest answer on this path is 1, and that is not settled
// (#258).
func TestRAGUpdateServedNoPipelinesExitsUsage(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, dbRAGEmptyPipelinesBody))

	err := runAuthed(t, rt, out, url, "database", "rag", "update",
		testDatabaseID, "--top-n", "7")
	requireExitUsage(t, err)
	if !strings.Contains(err.Error(), "--pipeline-config") {
		t.Errorf("error does not name the flag it demands: %v", err)
	}
}

// dbRAGEmptyPipelinesBody is dbWithRAGBody with its pipelines array
// emptied — the spec-violating shape the test above needs, and the only
// shape that reaches the guard from `update`.
const dbRAGEmptyPipelinesBody = `{"id":"` + testDatabaseID +
	`","name":"mydb","status":"available","pg_version":"16",` +
	`"cluster_id":"` + testClusterID +
	`","created_at":"2024-03-15T10:30:00Z",` +
	`"services":[{"service_id":"svc-3","service_type":"rag",` +
	`"state":"running","host_id":"host-1","rag_config":{` +
	`"embedding_llm":{"provider":"openai","model":"gpt"},` +
	`"completion_llm":{"provider":"openai","model":"gpt-4o"},` +
	`"top_n":5,"pipelines":[]}}]}`

// requireExitUsage is requireExitOne for the class this file covers:
// input the caller supplied and got wrong, which is exit 2.
func requireExitUsage(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitUsage {
		t.Fatalf("want exit %d, got %v", ExitUsage, err)
	}
}

// ragConfigPath builds the path a case wants --pipeline-config to
// receive, inside the test's own TempDir.
func ragConfigPath(t *testing.T, tc ragConfigCase) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pipelines.json")
	switch tc.kind {
	case ragPathAbsent:
		return path
	case ragPathDir:
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		return path
	case ragPathFile:
		if err := os.WriteFile(
			path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	t.Fatalf("unhandled --pipeline-config path kind %d", tc.kind)
	return ""
}

// ragArgsWithConfig is the rag flag list with the verb, database and
// --pipeline-config path chosen by the caller. update accepts the same
// flags: only the deployed service it requires differs. ragDeployArgs
// delegates here, so a new required flag on `rag deploy` cannot be
// added to one list and missed by the other.
func ragArgsWithConfig(verb, dbID, cfgPath string) []string {
	return []string{"database", "rag", verb, dbID,
		"--embedding-llm-provider", "openai",
		"--embedding-llm-model", "text-embedding-3-small",
		"--embedding-llm-api-key", "sk-e",
		"--completion-llm-provider", "openai",
		"--completion-llm-model", "gpt-4o",
		"--completion-llm-api-key", "sk-c",
		"--pipeline-config", cfgPath}
}
