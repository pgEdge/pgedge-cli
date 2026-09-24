package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// ragServiceOpts collects the deploy/update flag values. It has no
// targetNodes, for the reason given on mcpServiceOpts.
type ragServiceOpts struct {
	embeddingProvider  string
	embeddingModel     string
	embeddingAPIKey    string
	completionProvider string
	completionModel    string
	completionAPIKey   string
	tokenBudget        int
	topN               int
	pipelineConfigPath string
}

// NewDatabaseRAGCmd builds the `pgedge starfleet managed database rag` command
// group, which manages the RAG server deployed on a managed database.
func NewDatabaseRAGCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rag",
		Short: "Manage the RAG server on a database",
		Long: `rag manages the RAG server deployed alongside a managed
database: the retrieval-augmented-generation pipelines built on it.

deploy creates the service; update reconfigures it. Each refuses to
run in the other's place: deploy fails if one is already deployed,
update fails if none is.

Example:
  pgedge starfleet managed database rag deploy <database_id> \
    --embedding-llm-provider openai \
    --embedding-llm-model text-embedding-3-small \
    --embedding-llm-api-key "$OPENAI_API_KEY" \
    --completion-llm-provider openai \
    --completion-llm-model gpt-4o \
    --completion-llm-api-key "$OPENAI_API_KEY" \
    --pipeline-config pipelines.json
  pgedge starfleet managed database rag update <database_id> --top-n 10`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newDatabaseRAGDeployCmd(rt),
		newDatabaseRAGUpdateCmd(rt),
	)
	return cmd
}

// --- deploy ---

func newDatabaseRAGDeployCmd(rt *module.Runtime) *cobra.Command {
	opts := &ragServiceOpts{}
	cmd := &cobra.Command{
		Use:   "deploy <database_id>",
		Short: "Deploy a RAG server on a database",
		Long: `deploy stands up a RAG server alongside a managed database.

Use it to build retrieval-augmented-generation pipelines over the
database's data. The embedding and completion LLM settings and a
--pipeline-config file are required. deploy creates the service: if
one is already deployed on this database, it fails and points you at
update instead of reconfiguring it.

Example:
  pgedge starfleet managed database rag deploy <database_id> \
    --embedding-llm-provider openai \
    --embedding-llm-model text-embedding-3-small \
    --embedding-llm-api-key sk-... \
    --completion-llm-provider openai --completion-llm-model gpt-4o \
    --completion-llm-api-key sk-... --pipeline-config pipelines.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return applyRAGService(rt, cmd, args[0], opts, intentDeploy)
		},
	}
	bindRAGFlags(cmd, opts)

	_ = cmd.MarkFlagRequired("embedding-llm-provider")
	_ = cmd.MarkFlagRequired("embedding-llm-model")
	_ = cmd.MarkFlagRequired("embedding-llm-api-key")
	_ = cmd.MarkFlagRequired("completion-llm-provider")
	_ = cmd.MarkFlagRequired("completion-llm-model")
	_ = cmd.MarkFlagRequired("completion-llm-api-key")
	_ = cmd.MarkFlagRequired("pipeline-config")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// --- update ---

func newDatabaseRAGUpdateCmd(rt *module.Runtime) *cobra.Command {
	opts := &ragServiceOpts{}
	cmd := &cobra.Command{
		Use:   "update <database_id>",
		Short: "Update the RAG server on a database",
		Long: `update changes the RAG server configuration on a managed
database.

update requires a deployed RAG service on this database; if none is
deployed, it fails and points you at deploy instead of creating one.

Only the flags you pass are changed; every other setting, including
the deployed pipelines, is read from the deployed service and
preserved.

The LLM API keys may be omitted. They are write-only and never
returned, so the CLI cannot read them back, but the API refills an
omitted key from stored state on an update. Pass
--embedding-llm-api-key or --completion-llm-api-key only to rotate
one.

Example:
  pgedge starfleet managed database rag update <database_id> --top-n 10
  pgedge starfleet managed database rag update <database_id> \
    --pipeline-config pipelines.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return applyRAGService(rt, cmd, args[0], opts, intentUpdate)
		},
	}
	bindRAGFlags(cmd, opts)
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// bindRAGFlags registers the RAG service flags, shared by deploy and
// update.
func bindRAGFlags(cmd *cobra.Command, opts *ragServiceOpts) {
	f := cmd.Flags()
	f.StringVar(&opts.embeddingProvider, "embedding-llm-provider", "",
		"Embedding LLM provider (openai or anthropic)")
	f.StringVar(&opts.embeddingModel, "embedding-llm-model", "",
		"Embedding LLM model identifier")
	f.StringVar(&opts.embeddingAPIKey, "embedding-llm-api-key", "",
		"API key for the embedding LLM provider")
	f.StringVar(&opts.completionProvider, "completion-llm-provider", "",
		"Completion LLM provider (e.g. openai, anthropic)")
	f.StringVar(&opts.completionModel, "completion-llm-model", "",
		"Completion LLM model identifier")
	f.StringVar(&opts.completionAPIKey, "completion-llm-api-key", "",
		"API key for the completion LLM provider")
	f.IntVar(&opts.tokenBudget, "token-budget", 0,
		"Default max completion tokens across all pipelines")
	f.IntVar(&opts.topN, "top-n", 0,
		"Default number of results to retrieve per pipeline")
	f.StringVar(&opts.pipelineConfigPath, "pipeline-config", "",
		"Path to a JSON file containing pipeline definitions")
}

// --- shared implementation ---

func applyRAGService(
	rt *module.Runtime, cmd *cobra.Command, dbID string,
	opts *ragServiceOpts, intent serviceIntent,
) error {
	// Before the client: after it, a malformed ID would report exit 5
	// "no credentials found".
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

	if err := guardServiceIntent(rt, db, api.Rag, intent,
		cmd.Parent().CommandPath()); err != nil {
		return err
	}

	// Start from the deployed configuration so an update touches only
	// the flags the caller passed. Provider, Model and Pipelines are
	// non-pointer required fields, so a config rebuilt from flags alone
	// would send them empty and the API would reject it.
	cfg := existingRAGConfig(db)
	if err := applyRAGFlags(rt, cmd, opts, &cfg); err != nil {
		return err
	}

	newSvc := api.ServiceConfig{
		ServiceType: api.Rag,
		RagConfig:   &cfg,
	}

	return applyServices(rt, client, db, buildServiceList(db, newSvc),
		"RAG service applied", dbID)
}

// existingRAGConfig returns a copy of the RAG configuration already
// deployed on the database, or a zero config when there is none.
//
// Its API keys are always absent: saas's ragConfigToModel never
// populates ApiKey. That is harmless only because buildServiceList
// carries the service_id. The managed update path classifies a service
// as new or reconfigured from the incoming (service_id, type) pair, and
// CarryForwardManagedSecrets refills a reconfigured service's omitted
// keys; a write classified as new must still supply both. Verified on
// devapi 2026-08-06: `rag update --top-n 5` without keys succeeds on a
// deployed service.
//
// byoc decides from its stored services instead
// (requireAPIKeysForNewRAGServices checks hasExistingRAG).
func existingRAGConfig(db *api.ManagedDatabase) api.RAGServiceConfig {
	svc := findService(db, api.Rag)
	if svc == nil || svc.RagConfig == nil {
		return api.RAGServiceConfig{}
	}
	return *svc.RagConfig
}

// applyRAGFlags overlays the flags the caller actually set onto cfg.
//
// Flags().Changed is the test throughout, never the zero value:
// `--top-n 0` and `--token-budget 0` are explicit instructions a `> 0`
// check would swallow.
func applyRAGFlags(
	rt *module.Runtime, cmd *cobra.Command, opts *ragServiceOpts,
	cfg *api.RAGServiceConfig,
) error {
	f := cmd.Flags()

	if f.Changed("embedding-llm-provider") {
		cfg.EmbeddingLlm.Provider =
			api.RAGLLMConfigProvider(opts.embeddingProvider)
	}
	if f.Changed("embedding-llm-model") {
		cfg.EmbeddingLlm.Model = opts.embeddingModel
	}
	if f.Changed("embedding-llm-api-key") {
		cfg.EmbeddingLlm.ApiKey = &opts.embeddingAPIKey
	}
	if f.Changed("completion-llm-provider") {
		cfg.CompletionLlm.Provider =
			api.RAGLLMConfigProvider(opts.completionProvider)
	}
	if f.Changed("completion-llm-model") {
		cfg.CompletionLlm.Model = opts.completionModel
	}
	if f.Changed("completion-llm-api-key") {
		cfg.CompletionLlm.ApiKey = &opts.completionAPIKey
	}
	if f.Changed("token-budget") {
		cfg.TokenBudget = &opts.tokenBudget
	}
	if f.Changed("top-n") {
		cfg.TopN = &opts.topN
	}

	if f.Changed("pipeline-config") {
		// Every way this file can be unusable (absent, unreadable,
		// unparseable, structurally invalid) is ExitUsage, as in
		// controlplane's spec loader and byoc's structured-flag parsers:
		// nothing was sent and the fix is in what the caller typed. Read
		// and parse errors share it, so one mistake never gets two codes.
		data, readErr := os.ReadFile(opts.pipelineConfigPath)
		if readErr != nil {
			return newExitError(fmt.Sprintf(
				"read pipeline config %q: %v",
				opts.pipelineConfigPath, readErr), ExitUsage)
		}
		pipelines, err := parsePipelineConfig(data)
		if err != nil {
			return newExitError(fmt.Sprintf(
				"parse pipeline config %q: %v",
				opts.pipelineConfigPath, err), ExitUsage)
		}
		if err := validatePipelines(
			opts.pipelineConfigPath, pipelines); err != nil {
			return err
		}
		rt.DryRun.Pass("%d RAG pipeline(s) in %s valid",
			len(pipelines), opts.pipelineConfigPath)
		cfg.Pipelines = pipelines
	}

	// All three are required by the API. On update they come from the
	// deployed service, which guardServiceIntent confirmed exists, so
	// while the API honours its spec only deploy reaches this. Say so
	// here rather than send a request that can only 400.
	var missing []string
	if cfg.EmbeddingLlm.Provider == "" || cfg.EmbeddingLlm.Model == "" {
		missing = append(missing,
			"--embedding-llm-provider/--embedding-llm-model")
	}
	if cfg.CompletionLlm.Provider == "" || cfg.CompletionLlm.Model == "" {
		missing = append(missing,
			"--completion-llm-provider/--completion-llm-model")
	}
	if len(cfg.Pipelines) == 0 {
		missing = append(missing, "--pipeline-config")
	}
	if len(missing) > 0 {
		// ExitUsage, matching cobra's 2 for an omitted flag: on deploy
		// only an explicitly empty value gets here.
		// update gets here only if the API returns a rag_config that
		// breaks its own spec (pipelines required, minItems 1); then a
		// faultless command line gets 2, the cost of one shared guard.
		return newExitError(fmt.Sprintf(
			"%s %s required to deploy a RAG service",
			joinFlags(missing), pluralIs(len(missing))), ExitUsage)
	}
	return nil
}

// parsePipelineConfig parses a RAG pipeline definition file. It accepts
// either a bare JSON array of pipelines or an object with a "pipelines"
// key — the latter mirrors the shape the API returns under rag_config,
// so a config read back from the API can be pasted into a file and used
// directly.
func parsePipelineConfig(data []byte) ([]api.RAGPipelineConfig, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		// Object form: require an explicit "pipelines" key so a typo
		// such as {"pipeline": [...]} is rejected rather than silently
		// treated as an empty pipeline list. An explicit
		// {"pipelines": []} is allowed.
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &probe); err != nil {
			return nil, err
		}
		raw, ok := probe["pipelines"]
		if !ok {
			return nil, fmt.Errorf(
				`object form must contain a "pipelines" key`)
		}
		var pipelines []api.RAGPipelineConfig
		if err := json.Unmarshal(raw, &pipelines); err != nil {
			return nil, err
		}
		return pipelines, nil
	}

	var pipelines []api.RAGPipelineConfig
	if err := json.Unmarshal(trimmed, &pipelines); err != nil {
		return nil, err
	}
	return pipelines, nil
}

// ragReservedPipelineName is the pipeline name the API reserves.
const ragReservedPipelineName = "_default"

// validatePipelines checks a pipeline config against the API's
// structural rules before the request goes out. All are rejected
// server-side with a 400, and the round trip is avoidable.
//
// Table existence is deliberately not checked — the API does not check
// it either, and the CLI has no database connection to check it with.
func validatePipelines(path string, pipelines []api.RAGPipelineConfig) error {
	fail := func(format string, args ...any) error {
		// ExitUsage, not ExitGeneral: the same class of mistake as the
		// parse that precedes it, so the same code.
		return newExitError(fmt.Sprintf("pipeline config %q: ", path)+
			fmt.Sprintf(format, args...), ExitUsage)
	}

	if len(pipelines) == 0 {
		return fail("at least one pipeline is required")
	}
	seen := make(map[string]bool, len(pipelines))
	for i, p := range pipelines {
		switch {
		case p.Name == "":
			return fail("pipelines[%d]: name is required", i)
		case p.Name == ragReservedPipelineName:
			return fail("pipelines[%d]: name %q is reserved", i, p.Name)
		case seen[p.Name]:
			return fail("pipelines[%d]: duplicate name %q", i, p.Name)
		}
		seen[p.Name] = true

		if len(p.Tables) == 0 {
			return fail(
				"pipelines[%d] (%s): at least one table is required",
				i, p.Name)
		}
		for j, tbl := range p.Tables {
			switch {
			case tbl.Table == "":
				return fail(
					"pipelines[%d] (%s): tables[%d]: table is required",
					i, p.Name, j)
			case tbl.TextColumn == "":
				return fail(
					"pipelines[%d] (%s): tables[%d]: text_column is required",
					i, p.Name, j)
			case tbl.VectorColumn == "":
				return fail(
					"pipelines[%d] (%s): tables[%d]: vector_column is required",
					i, p.Name, j)
			}
		}
	}
	return nil
}
