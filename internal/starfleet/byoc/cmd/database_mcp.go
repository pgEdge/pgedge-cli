package cmd

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// mcpServiceOpts collects the deploy/update flag values so
// applyMCPService can assemble the request without reaching for
// package-level state.
type mcpServiceOpts struct {
	allowWrites       bool
	embeddingProvider string
	embeddingModel    string
	embeddingAPIKey   string
	ollamaURL         string
	targetNodes       []string
	initTokens        string
	initUsers         string
}

// NewDatabaseMCPCmd builds the `pgedge starfleet byoc database mcp` command
// group, which manages the MCP server deployed on a database.
func NewDatabaseMCPCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage the MCP server on a database",
		Long: `mcp manages the MCP server deployed alongside a database:
the endpoint that lets an LLM query the database.

deploy creates the service; update reconfigures it. Each refuses to
run in the other's place: deploy fails if one is already deployed,
update fails if none is.

Example:
  pgedge starfleet byoc database mcp deploy <database_id>
  pgedge starfleet byoc database mcp update <database_id> --allow-writes`,
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
		Long: `deploy stands up an MCP server alongside a database.

Use it to give an LLM read (or, with --allow-writes, read-write)
access to the database over MCP. Pass --wait to block until
deployment finishes. deploy creates the service: if one is already
deployed on this database, it fails and points you at update instead
of reconfiguring it.

Example:
  pgedge starfleet byoc database mcp deploy <database_id>
  pgedge starfleet byoc database mcp deploy <database_id> \
    --embedding-provider openai --embedding-model text-embedding-3-small \
    --embedding-api-key sk-... --wait`,
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
		Long: `update changes the MCP server configuration on a database.

update requires a deployed MCP service on this database; if none is
deployed, it fails and points you at deploy instead of creating one.

Only the flags you pass are changed; every other setting, including
the service's node placement, is read from the deployed service and
preserved. Pass --wait to block until the change finishes.

--allow-writes is a boolean, so it can only be turned on by passing it
and off by passing --allow-writes=false. Omitting it leaves the
current access level alone.

Example:
  pgedge starfleet byoc database mcp update <database_id> --allow-writes
  pgedge starfleet byoc database mcp update <database_id> --allow-writes=false`,
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
		"Embedding provider: ollama, openai, or voyage")
	f.StringVar(&opts.embeddingModel, "embedding-model", "",
		"Embedding model identifier "+
			"(required when --embedding-provider is set)")
	f.StringVar(&opts.embeddingAPIKey, "embedding-api-key", "",
		"API key for the embedding provider "+
			"(required for openai and voyage)")
	f.StringVar(&opts.ollamaURL, "ollama-url", "",
		"Endpoint URL for an Ollama server "+
			"(required when --embedding-provider is ollama)")
	f.StringSliceVar(&opts.targetNodes, "target-nodes", nil,
		"Node names to deploy on (e.g. n1,n2). "+
			"Auto-selects if cluster has one node")
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
	// Before the client, deliberately: clientFromCmd resolves
	// credentials, so a malformed ID checked after it reports exit 5
	// "no credentials found" for a mistake the caller can see -- and
	// managed's identical verbs already answer 2.
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

	if err := guardServiceIntent(rt, db, api.ServiceServiceTypeMcp, intent,
		cmd.Parent().CommandPath()); err != nil {
		return err
	}

	// Start from the deployed configuration, same as the other two
	// services.
	cfg := existingMCPConfig(db)
	if err := applyMCPFlags(cmd, opts, &cfg); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(db.ClusterId)
	if err != nil {
		return newExitError(fmt.Sprintf(
			"invalid cluster ID %q on database: %v", db.ClusterId, err), ExitGeneral)
	}

	hostIDs, err := resolveServicePlacement(
		client, db, clusterID, api.ServiceServiceTypeMcp,
		opts.targetNodes)
	if err != nil {
		return err
	}

	newSvc := api.ServiceConfig{
		ServiceType: api.ServiceConfigServiceTypeMcp,
		McpConfig:   &cfg,
		HostIds:     &hostIDs,
	}

	body := api.UpdateDatabaseJSONRequestBody{
		Services: nullable.NewNullableWithValue(buildServiceList(db, newSvc)),
	}

	var priorTaskID string
	if tracking() {
		priorTaskID, err = newestSubjectTaskID(
			context.Background(), client, dbID)
		if err != nil {
			return err
		}
	}

	resp, err := client.UpdateDatabaseWithResponse(
		context.Background(), id, body)
	if err != nil {
		return fmt.Errorf("apply MCP service: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return err
	}

	if err := printUpdatedDatabase(rt, resp.JSON200); err != nil {
		return err
	}
	fmt.Fprintf(rt.Stderr, "MCP service applied to database %s.\n",
		output.Sanitize(dbID))
	return trackMutation(rt, client, dbID, priorTaskID)
}

// existingMCPConfig returns a copy of the MCP configuration already
// deployed on the database, or a zero config when no MCP service is
// present.
//
// MCP's secrets DO survive this round trip, unlike RAG's api_key and
// PostgREST's jwt_secret. The spec records embedding_api_key,
// init_tokens and init_users as "stored encrypted server-side; returned
// in GET /databases/{id}", and that is accurate — verified live on
// --profile dev.
//
// It is only true of GET /databases/{id}, though, and that is the trap.
// The API converts the two read paths differently, so the same service
// reports its secrets on a get and omits them on a list.
//
// This merge is safe because fetchDatabaseWith calls GetDatabase. Any
// future code that populates a config from a LIST response would
// silently blank all three fields — the list result looks like a
// service that simply has no secrets set.
func existingMCPConfig(db *api.Database) api.MCPServiceConfig {
	svc := findService(db, api.ServiceServiceTypeMcp)
	if svc == nil || svc.McpConfig == nil {
		return api.MCPServiceConfig{}
	}
	return *svc.McpConfig
}

// applyMCPFlags overlays the flags the caller actually set onto cfg.
//
// --allow-writes is the one that most needed this. It was assigned
// unconditionally from a bool flag defaulting to false, so ANY other
// change — `mcp update <db> --embedding-model x` — silently sent
// allow_writes:false and revoked the service's write access. A bool
// flag cannot express "leave it alone" through its value, so
// Flags().Changed is the only thing that can tell the two apart. That
// makes this a quiet privilege change, not merely a dropped setting,
// and it is why every field here is gated the same way.
//
// After the overlay it refuses openai or voyage with no key reachable
// from either this call or the stored config: that write used to
// succeed and fail only at the deployed server's first call needing
// the key, as managed's once did. ollama takes --ollama-url
// instead of a key, so it is exempt.
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
	if f.Changed("ollama-url") {
		cfg.OllamaUrl = &opts.ollamaURL
	}
	if f.Changed("init-tokens") {
		cfg.InitTokens = &opts.initTokens
	}
	if f.Changed("init-users") {
		cfg.InitUsers = &opts.initUsers
	}

	p := api.MCPServiceConfigEmbeddingProvider(opts.embeddingProvider)
	needsKey := p == api.MCPServiceConfigEmbeddingProviderOpenai ||
		p == api.MCPServiceConfigEmbeddingProviderVoyage
	if f.Changed("embedding-provider") && needsKey &&
		(cfg.EmbeddingApiKey == nil || *cfg.EmbeddingApiKey == "") {
		return newExitError(fmt.Sprintf(
			"--embedding-provider %q requires an embedding API key: "+
				"pass --embedding-api-key, or (on update) leave it "+
				"unset to reuse one already stored",
			opts.embeddingProvider), ExitUsage)
	}
	return nil
}
