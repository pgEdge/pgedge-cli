package cmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// mcpServiceOpts collects the deploy/update flag values.
//
// Unlike byoc's, it has no targetNodes: a managed database is
// single-master, and the API discards HostIDs and TargetNodes on the
// managed write path, so a placement flag would be a silent no-op.
//
// Nor has it ollamaURL. Ollama is self-hosted model serving, which has
// nowhere to run in a tenant namespace, so the API dropped it from the
// managed contract and rejects the field. byoc and Control Plane still
// accept it (internal/starfleet/byoc/cmd/database_mcp.go).
type mcpServiceOpts struct {
	allowWrites       bool
	embeddingProvider string
	embeddingModel    string
	embeddingAPIKey   string
	initTokens        string
	initUsers         string
}

// mcpEmbeddingProviders is the managed MCP embedding vocabulary, in
// display order, spelled with the generated constants. It lacks byoc's
// ollama, which the API answers with a 400 here, so checking locally
// gives exit 2 without a round trip. The generated enum's Valid()
// cannot enumerate its members, so it cannot tell when this list is
// refusing a provider the API has added;
// TestMCPEmbeddingProvidersMatchTheSpecEnum reads the vendored spec and
// fails when one appears.
var mcpEmbeddingProviders = []string{
	string(api.MCPServiceConfigEmbeddingProviderOpenai),
	string(api.MCPServiceConfigEmbeddingProviderVoyage),
}

// NewDatabaseMCPCmd builds the `pgedge starfleet managed database mcp` command
// group, which manages the MCP server deployed on a managed database.
func NewDatabaseMCPCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage the MCP server on a database",
		Long: `mcp manages the MCP server deployed alongside a managed
database: the endpoint that lets an LLM query the database.

deploy creates the service; update reconfigures it. Each refuses to
run in the other's place: deploy fails if one is already deployed,
update fails if none is.

Example:
  pgedge starfleet managed database mcp deploy <database_id>
  pgedge starfleet managed database mcp update <database_id> --allow-writes`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newDatabaseMCPDeployCmd(rt),
		newDatabaseMCPUpdateCmd(rt),
	)
	return cmd
}

// --- deploy ---

func newDatabaseMCPDeployCmd(rt *module.Runtime) *cobra.Command {
	opts := &mcpServiceOpts{}
	cmd := &cobra.Command{
		Use:   "deploy <database_id>",
		Short: "Deploy an MCP server on a database",
		Long: `deploy stands up an MCP server alongside a managed database.

Use it to give an LLM read (or, with --allow-writes, read-write)
access to the database over MCP. deploy creates the service: if one
is already deployed on this database, it fails and points you at
update instead of reconfiguring it.

If no bearer token is supplied with --init-tokens, the API generates
one.

Example:
  pgedge starfleet managed database mcp deploy <database_id>
  pgedge starfleet managed database mcp deploy <database_id> \
    --embedding-provider openai \
    --embedding-model text-embedding-3-small \
    --embedding-api-key sk-...`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return applyMCPService(rt, cmd, args[0], opts, intentDeploy)
		},
	}
	bindMCPFlags(cmd, opts)
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// --- update ---

func newDatabaseMCPUpdateCmd(rt *module.Runtime) *cobra.Command {
	opts := &mcpServiceOpts{}
	cmd := &cobra.Command{
		Use:   "update <database_id>",
		Short: "Update the MCP server on a database",
		Long: `update changes the MCP server configuration on a managed
database.

update requires a deployed MCP service on this database; if none is
deployed, it fails and points you at deploy instead of creating one.

Only the flags you pass are changed; every other setting is read
from the deployed service and preserved. That includes the MCP
secrets, which the API does return on a read, so there is no need to
re-supply --embedding-api-key, --init-tokens or --init-users.

--allow-writes is a boolean, so it can only be turned on by passing
it and off by passing --allow-writes=false. Omitting it leaves the
current access level alone.

Example:
  pgedge starfleet managed database mcp update <database_id> --allow-writes
  pgedge starfleet managed database mcp update <database_id> --allow-writes=false`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return applyMCPService(rt, cmd, args[0], opts, intentUpdate)
		},
	}
	bindMCPFlags(cmd, opts)
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// bindMCPFlags registers the MCP service flags, shared by deploy and
// update.
func bindMCPFlags(cmd *cobra.Command, opts *mcpServiceOpts) {
	f := cmd.Flags()
	f.BoolVar(&opts.allowWrites, "allow-writes", false,
		"Grant the MCP service read-write access "+
			"(WARNING: allows LLM to modify data)")
	f.StringVar(&opts.embeddingProvider, "embedding-provider", "",
		"Embedding provider: openai or voyage")
	f.StringVar(&opts.embeddingModel, "embedding-model", "",
		"Embedding model identifier "+
			"(required when --embedding-provider is set)")
	f.StringVar(&opts.embeddingAPIKey, "embedding-api-key", "",
		"API key for the embedding provider "+
			"(required when --embedding-provider is set on deploy; "+
			"the stored key is reused if omitted on update)")
	f.StringVar(&opts.initTokens, "init-tokens", "",
		"Bearer token forwarded to the MCP server as INIT_TOKENS")
	f.StringVar(&opts.initUsers, "init-users", "",
		"Comma-separated username:password pairs forwarded as INIT_USERS")
}

// --- shared implementation ---

func applyMCPService(
	rt *module.Runtime, cmd *cobra.Command, dbID string,
	opts *mcpServiceOpts, intent serviceIntent,
) error {
	// Before the client: after it, a misspelled provider would report
	// exit 5 "no credentials found", or cost a token exchange and a GET
	// to reject a value that was never going to be sent.
	if err := validateEmbeddingProvider(rt, cmd, opts); err != nil {
		return err
	}
	id, err := parseUUIDArg(dbID, "database ID")
	if err != nil {
		return err
	}

	client, err := clientFromCmd(rt, cmd)
	if err != nil {
		return err
	}

	db, err := fetchDatabaseWith(rt, client, id)
	if err != nil {
		return err
	}

	if err := guardServiceIntent(rt, db, api.Mcp, intent,
		cmd.Parent().CommandPath()); err != nil {
		return err
	}

	// Start from the deployed configuration so an update touches only
	// the flags the caller passed.
	cfg := existingMCPConfig(db)
	if err := applyMCPFlags(cmd, opts, &cfg); err != nil {
		return err
	}

	newSvc := api.ServiceConfig{
		ServiceType: api.Mcp,
		McpConfig:   &cfg,
	}

	return applyServices(rt, client, db, buildServiceList(db, newSvc),
		"MCP service applied", dbID)
}

// existingMCPConfig returns a copy of the MCP configuration already
// deployed on the database, or a zero config when there is none.
//
// The secrets survive the round trip: GetManagedDatabase, unlike list
// (see fetchDatabaseWith), hydrates them and renders them in full, so
// embedding_api_key, init_tokens and init_users are echoed back
// unchanged. The API would refill an omitted key anyway (see
// existingRAGConfig); echoing them keeps the write an exact statement
// of intent.
func existingMCPConfig(db *api.ManagedDatabase) api.MCPServiceConfig {
	svc := findService(db, api.Mcp)
	if svc == nil || svc.McpConfig == nil {
		return api.MCPServiceConfig{}
	}
	return *svc.McpConfig
}

// applyMCPFlags overlays the flags the caller actually set onto cfg.
//
// Flags().Changed is the test throughout, never the zero value: a bool
// cannot say "leave it alone", so assigning --allow-writes
// unconditionally would make any unrelated change revoke write access.
//
// After the overlay it checks that a provider has a key. Without a key
// the write succeeds and the deployed server fails at its first
// tools/call. The check reads the merged cfg, not opts, so a key
// stored on the service satisfies it on update.
func applyMCPFlags(
	cmd *cobra.Command, opts *mcpServiceOpts, cfg *api.MCPServiceConfig,
) error {
	f := cmd.Flags()

	if f.Changed("allow-writes") {
		cfg.AllowWrites = &opts.allowWrites
	}
	if f.Changed("embedding-provider") {
		p := api.MCPServiceConfigEmbeddingProvider(opts.embeddingProvider)
		cfg.EmbeddingProvider = &p
	}
	if f.Changed("embedding-model") {
		cfg.EmbeddingModel = &opts.embeddingModel
	}
	if f.Changed("embedding-api-key") {
		cfg.EmbeddingApiKey = &opts.embeddingAPIKey
	}
	if f.Changed("init-tokens") {
		cfg.InitTokens = &opts.initTokens
	}
	if f.Changed("init-users") {
		cfg.InitUsers = &opts.initUsers
	}

	if f.Changed("embedding-provider") &&
		(cfg.EmbeddingApiKey == nil || *cfg.EmbeddingApiKey == "") {
		return newExitError(fmt.Sprintf(
			"--embedding-provider %q requires an embedding API key: "+
				"pass --embedding-api-key, or (on update) leave it "+
				"unset to reuse one already stored",
			opts.embeddingProvider), ExitUsage)
	}
	return nil
}

// validateEmbeddingProvider rejects an --embedding-provider outside
// managed's vocabulary with ExitUsage, and records the pass in the
// dry-run ledger. An unset flag means "leave it alone" and is not
// checked.
func validateEmbeddingProvider(
	rt *module.Runtime, cmd *cobra.Command, opts *mcpServiceOpts,
) error {
	if !cmd.Flags().Changed("embedding-provider") {
		return nil
	}
	if !slices.Contains(mcpEmbeddingProviders, opts.embeddingProvider) {
		return newExitError(fmt.Sprintf(
			"unknown embedding provider %q (expected one of: %s)",
			opts.embeddingProvider,
			strings.Join(mcpEmbeddingProviders, ", ")), ExitUsage)
	}
	rt.DryRun.Pass(
		"embedding provider %q accepted", opts.embeddingProvider)
	return nil
}
