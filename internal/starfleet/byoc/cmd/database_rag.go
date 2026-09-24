package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// ragServiceOpts collects the deploy/update flag values so
// applyRAGService can assemble the request without reaching for
// package-level state.
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
	targetNodes        []string
}

// NewDatabaseRAGCmd builds the `pgedge starfleet byoc database rag` command
// group, which manages the RAG server deployed on a database.
func NewDatabaseRAGCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rag",
		Short: "Manage the RAG server on a database",
		Long: `rag manages the RAG server deployed alongside a database:
the retrieval-augmented-generation pipelines built on it.

deploy creates the service; update reconfigures it. Each refuses to
run in the other's place: deploy fails if one is already deployed,
update fails if none is.

Example:
  pgedge starfleet byoc database rag deploy <database_id> \
    --embedding-llm-provider openai \
    --embedding-llm-model text-embedding-3-small \
    --embedding-llm-api-key "$OPENAI_API_KEY" \
    --completion-llm-provider openai \
    --completion-llm-model gpt-4o \
    --completion-llm-api-key "$OPENAI_API_KEY" \
    --pipeline-config pipelines.json
  pgedge starfleet byoc database rag update <database_id> --top-n 10`,
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
		Long: `deploy stands up a RAG server alongside a database.

Use it to build retrieval-augmented-generation pipelines over the
database's data. The embedding and completion LLM settings and a
--pipeline-config file are required. Pass --wait to block until
deployment finishes. deploy creates the service: if one is already
deployed on this database, it fails and points you at update instead
of reconfiguring it.

Example:
  pgedge starfleet byoc database rag deploy <database_id> \
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
	addWaitFlags(cmd)

	_ = cmd.MarkFlagRequired("embedding-llm-provider")
	_ = cmd.MarkFlagRequired("embedding-llm-model")
	_ = cmd.MarkFlagRequired("embedding-llm-api-key")
	_ = cmd.MarkFlagRequired("completion-llm-provider")
	_ = cmd.MarkFlagRequired("completion-llm-model")
	_ = cmd.MarkFlagRequired("completion-llm-api-key")
	_ = cmd.MarkFlagRequired("pipeline-config")
	cli.MarkMutating(cmd)

	return cmd
}

// --- update ---

func newDatabaseRAGUpdateCmd(rt *module.Runtime) *cobra.Command {
	opts := &ragServiceOpts{}
	cmd := &cobra.Command{
		Use:   "update <database_id>",
		Short: "Update the RAG server on a database",
		Long: `update changes the RAG server configuration on a database.

update requires a deployed RAG service on this database; if none is
deployed, it fails and points you at deploy instead of creating one.

Only the flags you pass are changed; every other setting, including
the deployed pipelines and the service's node placement, is read from
the deployed service and preserved. Pass --wait to block until the
change finishes.

The LLM API keys are the exception. They are write-only and never
returned by the API, so they cannot be preserved across an update:
pass --embedding-llm-api-key and --completion-llm-api-key again
whenever you change the provider or model they belong to.

Example:
  pgedge starfleet byoc database rag update <database_id> --top-n 10
  pgedge starfleet byoc database rag update <database_id> \
    --pipeline-config pipelines.json --wait`,
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
		"Completion LLM provider (e.g. openai)")
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
	f.StringSliceVar(&opts.targetNodes, "target-nodes", nil,
		"Node names to deploy on (e.g. n1,n2). "+
			"Auto-selects if cluster has one node")
}

// --- shared implementation ---

func applyRAGService(
	rt *module.Runtime, cmd *cobra.Command, dbID string,
	opts *ragServiceOpts, intent serviceIntent,
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

	if err := guardServiceIntent(rt, db, api.ServiceServiceTypeRag, intent,
		cmd.Parent().CommandPath()); err != nil {
		return err
	}

	// Start from the deployed configuration so an update touches only
	// the flags the caller passed. RAG needs this more than the other
	// services do: Provider, Model and Pipelines are all non-pointer
	// required fields, so a config rebuilt from flags does not merely
	// lose the values it omits — it sends empty ones, and the API
	// rejects the request outright with "rag_config must have at least
	// one pipeline".
	cfg := existingRAGConfig(db)
	if err := applyRAGFlags(rt, cmd, opts, &cfg); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(db.ClusterId)
	if err != nil {
		return newExitError(fmt.Sprintf(
			"invalid cluster ID %q on database: %v", db.ClusterId, err), ExitGeneral)
	}

	hostIDs, err := resolveServicePlacement(
		client, db, clusterID, api.ServiceServiceTypeRag,
		opts.targetNodes)
	if err != nil {
		return err
	}

	newSvc := api.ServiceConfig{
		ServiceType: api.ServiceConfigServiceTypeRag,
		RagConfig:   &cfg,
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
		return fmt.Errorf("apply RAG service: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return err
	}

	if err := printUpdatedDatabase(rt, resp.JSON200); err != nil {
		return err
	}
	fmt.Fprintf(rt.Stderr, "RAG service applied to database %s.\n",
		output.Sanitize(dbID))
	return trackMutation(rt, client, dbID, priorTaskID)
}

// existingRAGConfig returns a copy of the RAG configuration already
// deployed on the database, or a zero config when no RAG service is
// present.
//
// The API keys inside it are always absent: RAGLLMConfig.ApiKey is
// documented write-only ("stored in AWS Secrets Manager") and never
// comes back on a GET, so there is nothing to copy. That is the one
// field this merge cannot preserve, and it is why --*-llm-api-key must
// be passed again whenever it changes.
//
// PostgREST's jwt_secret is in the same position.
//
// The evidence for those two was read from the server's source, which
// is stronger than a probe: the RAG LLM config it returns carries only
// provider and model, and its PostgREST config omits jwt_secret, so
// no code path can populate either field.
//
// MCP's secrets are NOT in this position — they come back on
// GET /databases/{id}, confirmed against the live API. The
// asymmetry is deliberate, not an oversight. See existingMCPConfig;
// do not "unify" the two.
func existingRAGConfig(db *api.Database) api.RAGServiceConfig {
	svc := findService(db, api.ServiceServiceTypeRag)
	if svc == nil || svc.RagConfig == nil {
		return api.RAGServiceConfig{}
	}
	return *svc.RagConfig
}

// applyRAGFlags overlays the flags the caller actually set onto cfg.
//
// Flags().Changed is the test throughout, never a comparison against
// the zero value. `--top-n 0` and `--token-budget 0` are explicit
// instructions that a `> 0` check silently swallows, which is how the
// previous version could not tell "set it to zero" from "leave it
// alone".
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
		// Every way this file can be unusable — absent, unreadable,
		// unparseable, or structurally invalid — is ExitUsage (2),
		// matching controlplane's spec loader and byoc's own structured-flag
		// parsers. The caller named the path, nothing was sent, and
		// the fix is entirely in what they typed. Read and parse are
		// deliberately not split: one flag answering two codes for one
		// class of mistake is what this avoids.
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

	// All three are required by the API. On an update they come from the
	// deployed service, which guardServiceIntent has already confirmed
	// exists; this branch is reachable only from `deploy`, when the
	// flags omit one, as long as the API honours its spec.
	var missing []string
	if cfg.EmbeddingLlm.Provider == "" || cfg.EmbeddingLlm.Model == "" {
		missing = append(missing, "--embedding-llm-provider/--embedding-llm-model")
	}
	if cfg.CompletionLlm.Provider == "" || cfg.CompletionLlm.Model == "" {
		missing = append(missing,
			"--completion-llm-provider/--completion-llm-model")
	}
	if len(cfg.Pipelines) == 0 {
		missing = append(missing, "--pipeline-config")
	}
	if len(missing) > 0 {
		// ExitUsage: cobra answers 2 when one of these flags is simply
		// omitted, so on deploy only an explicitly empty value reaches
		// this guard, and reporting that as 1 gave one mistake two
		// codes. update reaches it only if the API returns a
		// rag_config violating its own spec (pipelines is required,
		// minItems 1) — with "pipelines":[] a faultless command line
		// gets 2, which is the cost of not forking this guard.
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
		// Object form: require an explicit "pipelines" key so a typo such
		// as {"pipeline": [...]} is rejected rather than silently treated
		// as an empty pipeline list. An explicit {"pipelines": []} is
		// allowed.
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

// RAG pipeline names the API reserves.
const ragReservedPipelineName = "_default"

// validatePipelines checks a pipeline config against the API's
// structural rules before the request goes out. All three are rejected
// server-side with a 400, and the round trip is avoidable: a live run
// lost a RAG deploy to `rag_config must have at least one pipeline`
// after supplying an empty array.
//
// Table existence is deliberately not checked — the API does not check
// it either, and the CLI has no database connection to check it with.
func validatePipelines(path string, pipelines []api.RAGPipelineConfig) error {
	fail := func(format string, args ...any) error {
		// ExitUsage, not ExitGeneral: this is the file the caller named
		// failing its structural rules — the same class of mistake as
		// the parse that precedes it, so the same code.
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
			return fail("pipelines[%d]: name %q is reserved",
				i, p.Name)
		case seen[p.Name]:
			return fail("pipelines[%d]: duplicate name %q", i, p.Name)
		}
		seen[p.Name] = true

		if len(p.Tables) == 0 {
			return fail("pipelines[%d] (%s): at least one table is required",
				i, p.Name)
		}
		for j, tbl := range p.Tables {
			switch {
			case tbl.Table == "":
				return fail("pipelines[%d] (%s): tables[%d]: table is required",
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
