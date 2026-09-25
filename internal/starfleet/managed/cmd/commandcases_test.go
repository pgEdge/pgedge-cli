package cmd

// managedCmdCase is one resource command invocation with valid
// arguments.
type managedCmdCase struct {
	name string
	args []string
}

// managedCmdCases enumerates every resource command with a valid,
// hermetic argument set. Shared by the transport-error, no-credentials
// and empty-body table tests, which run the whole surface against a
// stub that fails at, respectively, the connection, the credential and
// the response-parsing stage.
//
// Every leaf in the tree must appear here —
// TestEveryManagedLeafHasACase enforces it, so a new verb cannot ship
// without being driven by these tables.
var managedCmdCases = []managedCmdCase{
	{"database list", []string{"database", "list"}},
	{"database get", []string{"database", "get", testDatabaseID}},
	{"database connection-string",
		[]string{"database", "connection-string", testDatabaseID}},
	{"database inspect",
		[]string{"database", "inspect", testDatabaseID, "table-sizes"}},
	{"database create", []string{"database", "create", "--name", "d",
		"--region", "us-east-1", "--size", "small"}},
	{"database update", []string{"database", "update", testDatabaseID,
		"--display-name", "x"}},
	{"database delete",
		[]string{"database", "delete", testDatabaseID, "--force"}},
	{"database link", []string{"database", "link", testDatabaseID}},
	{"database env pull",
		[]string{"database", "env", "pull", testDatabaseID}},
	{"database branch list",
		[]string{"database", "branch", "list", testDatabaseID}},
	{"database branch get", []string{"database", "branch", "get",
		testDatabaseID, testBranchID}},
	{"database branch create",
		[]string{"database", "branch", "create", testDatabaseID}},
	{"database branch delete", []string{"database", "branch", "delete",
		testDatabaseID, testBranchID, "--force"}},
	{"database branch metrics", []string{"database", "branch", "metrics",
		testDatabaseID, testBranchID}},
	{"database branch logs", []string{"database", "branch", "logs",
		testDatabaseID, testBranchID}},
	{"database resize", []string{"database", "resize", testDatabaseID,
		"--size", "large", "--force"}},
	{"database rotate-password", []string{"database", "rotate-password",
		testDatabaseID, "--role", "app", "--force"}},
	{"database metrics",
		[]string{"database", "metrics", testDatabaseID}},
	{"database logs", []string{"database", "logs", testDatabaseID}},
	{"database allowlist get", []string{"database", "allowlist", "get",
		testDatabaseID}},
	{"database allowlist add", []string{"database", "allowlist", "add",
		testDatabaseID, "192.0.2.1"}},
	{"database allowlist remove", []string{"database", "allowlist",
		"remove", testDatabaseID, "192.0.2.1", "--force"}},
	{"database allowlist set", []string{"database", "allowlist", "set",
		testDatabaseID, "192.0.2.1"}},
	{"database allowlist open", []string{"database", "allowlist", "open",
		testDatabaseID}},
	{"database allowlist clear", []string{"database", "allowlist", "clear",
		testDatabaseID, "--force"}},
	{"database service list",
		[]string{"database", "service", "list", testDatabaseID}},
	{"database service get",
		[]string{"database", "service", "get", testDatabaseID, "mcp"}},
	{"database service remove", []string{"database", "service",
		"remove", testDatabaseID, "mcp", "--force"}},
	{"database mcp deploy",
		[]string{"database", "mcp", "deploy", testDatabaseID}},
	{"database mcp update", []string{"database", "mcp", "update",
		testDatabaseID, "--allow-writes"}},
	{"database rag deploy", []string{"database", "rag", "deploy",
		testDatabaseID,
		"--embedding-llm-provider", "openai",
		"--embedding-llm-model", "em",
		"--embedding-llm-api-key", "sk-e",
		"--completion-llm-provider", "anthropic",
		"--completion-llm-model", "cm",
		"--completion-llm-api-key", "sk-c",
		"--pipeline-config", "PIPELINE_CONFIG"}},
	{"database rag update", []string{"database", "rag", "update",
		testDatabaseID, "--top-n", "5"}},
	{"database postgrest deploy", []string{"database", "postgrest",
		"deploy", testDatabaseID, "--db-schemas", "public",
		"--db-anon-role", "anon"}},
	{"database postgrest update", []string{"database", "postgrest",
		"update", testDatabaseID, "--max-rows", "500"}},
	{"backup list", []string{"backup", "list"}},
	{"backup get", []string{"backup", "get", testBackupID}},
	{"backup create", []string{"backup", "create",
		"--database-id", testDatabaseID, "--kind", "hot"}},
	{"backup restore",
		[]string{"backup", "restore", testBackupID, "--force"}},
	{"client-ip", []string{"client-ip"}},
	{"pg-version list", []string{"pg-version", "list"}},
	{"region list", []string{"region", "list"}},
	{"size list", []string{"size", "list"}},
	// A real UUID, not a size name: `size get` parses its argument
	// before issuing a request, so a name would fail locally and
	// never reach the stub these tables drive.
	{"size get", []string{"size", "get", testSizeID}},
	{"task list", []string{"task", "list"}},
	{"task get", []string{"task", "get", testTaskID}},
	// --wait-timeout 0 so the poll loop checks its deadline and gives up
	// after a single pass. Without it a stub that never reports a
	// terminal status would hold the test for the default ten
	// minutes.
	{"task wait", []string{"task", "wait", testTaskID,
		"--wait-timeout", "0", "--wait-interval", "1"}},
}
