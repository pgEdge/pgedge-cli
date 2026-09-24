package cmd

// accountCmdCase is one resource command invocation with valid
// arguments.
type accountCmdCase struct {
	name string
	args []string
}

// accountCmdCases enumerates every invite/membership/client/tenant
// command with a valid, hermetic argument set. Shared by the transport-error
// and no-credentials table tests, which run the whole surface against
// a stub that fails at, respectively, the connection and the
// credential stage. Ported from internal/byoc/cmd's byocCmdCases when
// invite and membership moved here — auth is intentionally excluded,
// since its commands do not go through clientFromCmd the way a
// resource command does.
var accountCmdCases = []accountCmdCase{
	{"invite list", []string{"invite", "list"}},
	{"invite get", []string{"invite", "get", testInviteID}},
	{"invite create", []string{"invite", "create", "--email", "a@b.com"}},
	{"invite delete",
		[]string{"invite", "delete", testInviteID, "--force"}},
	{"invite accept", []string{"invite", "accept", testInviteID,
		"--token", "tok"}},
	{"membership list", []string{"membership", "list"}},
	{"membership delete",
		[]string{"membership", "delete", testMembershipID, "--force"}},
	{"client list", []string{"client", "list"}},
	{"client get", []string{"client", "get", testAPIClientID}},
	{"client create",
		[]string{"client", "create", "--name", "ci",
			"--description", "CI"}},
	// --name is required for update to get past its changed-flags
	// check and reach clientFromCmd, which is the branch both callers
	// of this table exercise.
	{"client update",
		[]string{"client", "update", testAPIClientID, "--name", "ci"}},
	{"client delete",
		[]string{"client", "delete", testAPIClientID, "--force"}},
	{"tenant list", []string{"tenant", "list"}},
	{"tenant get", []string{"tenant", "get", testTenantID}},
	// --name is required for update to get past its changed-flags
	// check and reach clientFromCmd, which is the branch both callers
	// of this table exercise.
	{"tenant update",
		[]string{"tenant", "update", testTenantID, "--name", "acme"}},
}
