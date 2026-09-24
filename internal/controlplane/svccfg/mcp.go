package svccfg

// MCPServiceUser mirrors control-plane
// server/internal/database/mcp_service_config.go:13-16
// (MCPServiceUser) at v0.10.0 (openapi/SOURCE CP_TAG): one entry of
// the mcp config's bootstrap-only init_users list.
type MCPServiceUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// MCPConfig mirrors control-plane
// server/internal/database/mcp_service_config.go:20-63
// (MCPServiceConfig) at v0.10.0 (openapi/SOURCE CP_TAG), the typed form
// CP parses ServiceSpec.Config into after validation
// (ParseMCPServiceConfig, :102-383). Field names, order and json tags
// are copied verbatim. It is never (de)serialized against a real
// service; it exists to put every mcp key in the controlplane truth
// universe of TestSkillDocsFieldClaimsMatchStructs and to be diffed
// against CP by eye. The per-key CP rules (conditional requirements,
// enums, secrets) live in mcpKeys, since a struct tag cannot express
// them.
type MCPConfig struct {
	// Optional - LLM proxy for web client (default: false)
	LLMEnabled *bool `json:"llm_enabled,omitempty"`

	// Required when llm_enabled is true; rejected otherwise
	LLMProvider     string  `json:"llm_provider"`
	LLMModel        string  `json:"llm_model"`
	AnthropicAPIKey *string `json:"anthropic_api_key,omitempty"`
	OpenAIAPIKey    *string `json:"openai_api_key,omitempty"`
	OllamaURL       *string `json:"ollama_url,omitempty"`

	// Optional - security
	AllowWrites *bool            `json:"allow_writes,omitempty"`
	InitToken   *string          `json:"init_token,omitempty"`
	InitUsers   []MCPServiceUser `json:"init_users,omitempty"`

	// Optional - embeddings
	EmbeddingProvider *string `json:"embedding_provider,omitempty"`
	EmbeddingModel    *string `json:"embedding_model,omitempty"`
	EmbeddingAPIKey   *string `json:"embedding_api_key,omitempty"`

	// Optional - LLM tuning (overridable defaults)
	LLMTemperature *float64 `json:"llm_temperature,omitempty"`
	LLMMaxTokens   *int     `json:"llm_max_tokens,omitempty"`

	// Optional - connection pool (overridable defaults)
	PoolMaxConns *int `json:"pool_max_conns,omitempty"`

	// Optional - tool toggles (all enabled by default)
	DisableQueryDatabase       *bool `json:"disable_query_database,omitempty"`
	DisableGetSchemaInfo       *bool `json:"disable_get_schema_info,omitempty"`
	DisableSimilaritySearch    *bool `json:"disable_similarity_search,omitempty"`
	DisableExecuteExplain      *bool `json:"disable_execute_explain,omitempty"`
	DisableGenerateEmbedding   *bool `json:"disable_generate_embedding,omitempty"`
	DisableSearchKnowledgebase *bool `json:"disable_search_knowledgebase,omitempty"`
	DisableCountRows           *bool `json:"disable_count_rows,omitempty"`

	// Optional - knowledgebase search, only when kb_enabled is true
	KBEnabled           *bool   `json:"kb_enabled,omitempty"`
	KBEmbeddingProvider *string `json:"kb_embedding_provider,omitempty"`
	KBEmbeddingModel    *string `json:"kb_embedding_model,omitempty"`
	KBEmbeddingAPIKey   *string `json:"kb_embedding_api_key,omitempty"`
	KBDatabaseHostPath  *string `json:"kb_database_host_path,omitempty"`
}

// mcpKeys mirrors mcpKnownKeys
// (server/internal/database/mcp_service_config.go:66-94) at v0.10.0,
// 27 keys, plus the conditional/secret/bootstrap rules CP enforces in
// ParseMCPServiceConfig (same file, :102-383) that a flat key list
// cannot express:
//
//   - :200-205 — with llm_enabled false or absent, llm_provider,
//     llm_model, anthropic_api_key, openai_api_key, llm_temperature and
//     llm_max_tokens are all REJECTED, not merely optional.
//   - :206-218 — ollama_url is shared with embeddings: rejected under
//     the same rule unless embedding_provider == "ollama".
//   - :157-178 — provider -> credential: anthropic -> anthropic_api_key,
//     openai -> openai_api_key, ollama -> ollama_url, each required
//     once llm_enabled is true and that provider is chosen.
//   - :304-311 — with kb_enabled false or absent, the four other kb_*
//     keys are rejected.
//   - :269-273 — kb_enabled: true and disable_search_knowledgebase:
//     true is a hard conflict.
//   - :329-347 — embedding_provider requires embedding_model, plus
//     embedding_api_key (voyage/openai) or ollama_url (ollama).
//   - :109-116 — init_token and init_users are bootstrap-only: rejected
//     on update, accepted on create.
//
// config: {} is valid for mcp — every key here is optional absent
// llm_enabled/kb_enabled being turned on.
var mcpKeys = []KeyMeta{
	{Name: "llm_enabled",
		Doc: "enables the LLM proxy tools; default false"},
	{Name: "llm_provider", Required: true, ConditionalOn: "llm_enabled",
		Enum: []string{"anthropic", "openai", "ollama"},
		Doc:  "required when llm_enabled is true; rejected otherwise"},
	{Name: "llm_model", Required: true, ConditionalOn: "llm_enabled",
		Doc: "required when llm_enabled is true; rejected otherwise"},
	{Name: "anthropic_api_key", ConditionalOn: "llm_enabled", Secret: true,
		Doc: "required when llm_provider is anthropic"},
	{Name: "openai_api_key", ConditionalOn: "llm_enabled", Secret: true,
		Doc: "required when llm_provider is openai"},
	{Name: "ollama_url", ConditionalOn: "llm_enabled",
		Doc: "required when llm_provider is ollama, or when " +
			"embedding_provider is ollama"},
	{Name: "allow_writes",
		Doc: "allow write queries via the mcp tools; default false"},
	{Name: "init_token", Secret: true, BootstrapOnly: true,
		Doc: "bootstrap token; create-time only, rejected on update"},
	{Name: "init_users", BootstrapOnly: true,
		Doc: "bootstrap users ({username,password}); create-time " +
			"only, rejected on update"},
	{Name: "embedding_provider",
		Enum: []string{"voyage", "openai", "ollama"},
		Doc:  "embedding provider for the similarity_search tool"},
	{Name: "embedding_model", ConditionalOn: "embedding_provider",
		Doc: "required when embedding_provider is set"},
	{Name: "embedding_api_key", ConditionalOn: "embedding_provider",
		Secret: true,
		Doc:    "required when embedding_provider is voyage or openai"},
	{Name: "llm_temperature", ConditionalOn: "llm_enabled",
		Doc: "0.0-2.0"},
	{Name: "llm_max_tokens", ConditionalOn: "llm_enabled",
		Doc: "> 0"},
	{Name: "pool_max_conns", Doc: "> 0"},
	{Name: "disable_query_database", Doc: "tool toggle; default false"},
	{Name: "disable_get_schema_info", Doc: "tool toggle; default false"},
	{Name: "disable_similarity_search", Doc: "tool toggle; default false"},
	{Name: "disable_execute_explain", Doc: "tool toggle; default false"},
	{Name: "disable_generate_embedding", Doc: "tool toggle; default false"},
	{Name: "disable_search_knowledgebase", Doc: "tool toggle; default false"},
	{Name: "disable_count_rows", Doc: "tool toggle; default false"},
	{Name: "kb_enabled",
		Doc: "enables knowledgebase search; default false"},
	{Name: "kb_embedding_provider", Required: true,
		ConditionalOn: "kb_enabled",
		Enum:          []string{"voyage", "openai"},
		Doc:           "required when kb_enabled is true; ollama unsupported"},
	{Name: "kb_embedding_model", Required: true, ConditionalOn: "kb_enabled",
		Doc: "required when kb_enabled is true"},
	{Name: "kb_embedding_api_key", ConditionalOn: "kb_enabled", Secret: true,
		Doc: "required when kb_enabled is true"},
	{Name: "kb_database_host_path", ConditionalOn: "kb_enabled",
		Doc: "absolute, clean path; only when kb_enabled is true"},
}
