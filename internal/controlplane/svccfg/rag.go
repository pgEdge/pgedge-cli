package svccfg

// RAGPipelineLLMConfig mirrors control-plane
// server/internal/database/rag_service_config.go:25-30
// (RAGPipelineLLMConfig) at v0.10.0: the shape of both a pipeline's
// embedding_llm and its rag_llm, whose provider enums differ (see the
// "provider" entry in ragKeys).
type RAGPipelineLLMConfig struct {
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	APIKey   *string `json:"api_key,omitempty"`
	BaseURL  *string `json:"base_url,omitempty"`
}

// RAGPipelineTable mirrors control-plane
// server/internal/database/rag_service_config.go:33-38
// (RAGPipelineTable) at v0.10.0.
type RAGPipelineTable struct {
	Table        string  `json:"table"`
	TextColumn   string  `json:"text_column"`
	VectorColumn string  `json:"vector_column"`
	IDColumn     *string `json:"id_column,omitempty"`
}

// RAGPipelineSearch mirrors control-plane
// server/internal/database/rag_service_config.go:41-44
// (RAGPipelineSearch) at v0.10.0.
type RAGPipelineSearch struct {
	HybridEnabled *bool    `json:"hybrid_enabled,omitempty"`
	VectorWeight  *float64 `json:"vector_weight,omitempty"`
}

// RAGPipeline mirrors control-plane
// server/internal/database/rag_service_config.go:47-57
// (RAGPipeline) at v0.10.0.
type RAGPipeline struct {
	Name         string               `json:"name"`
	Description  *string              `json:"description,omitempty"`
	Tables       []RAGPipelineTable   `json:"tables"`
	EmbeddingLLM RAGPipelineLLMConfig `json:"embedding_llm"`
	RAGLLM       RAGPipelineLLMConfig `json:"rag_llm"`
	TokenBudget  *int                 `json:"token_budget,omitempty"`
	TopN         *int                 `json:"top_n,omitempty"`
	SystemPrompt *string              `json:"system_prompt,omitempty"`
	Search       *RAGPipelineSearch   `json:"search,omitempty"`
}

// RAGDefaults mirrors control-plane
// server/internal/database/rag_service_config.go:60-63
// (RAGDefaults) at v0.10.0.
type RAGDefaults struct {
	TokenBudget *int `json:"token_budget,omitempty"`
	TopN        *int `json:"top_n,omitempty"`
}

// RAGConfig mirrors control-plane
// server/internal/database/rag_service_config.go:67-70
// (RAGServiceConfig) at v0.10.0 (openapi/SOURCE CP_TAG). CP decodes
// it with json.Decoder.DisallowUnknownFields at every nesting level
// (rag_service_config.go:106-110), so every struct in this tree is
// closed: `pipelines` and `defaults` are the only top-level keys
// (ragKnownTopLevelKeys, :75-78). pipelines is required and non-empty
// (:113-115), so config: {} is a guaranteed 400 for rag.
type RAGConfig struct {
	Pipelines []RAGPipeline `json:"pipelines"`
	Defaults  *RAGDefaults  `json:"defaults,omitempty"`
}

// ragKeys is every field name in the RAGConfig tree, flattened (see
// Keys). Required means CP rejects the pipeline, table or llm config
// the field sits under when it is absent; only pipelines is required at
// the top level.
var ragKeys = []KeyMeta{
	{Name: "pipelines", Required: true,
		Doc: ">= 1 pipeline; config: {} is rejected"},
	{Name: "name", Required: true,
		Doc: "pipelines[].name, matches ^[a-z0-9_][a-z0-9_-]*$, " +
			"unique across pipelines"},
	{Name: "description", Doc: "pipelines[].description"},
	{Name: "tables", Required: true,
		Doc: "pipelines[].tables, >= 1"},
	{Name: "table", Required: true, Doc: "pipelines[].tables[].table"},
	{Name: "text_column", Required: true,
		Doc: "pipelines[].tables[].text_column"},
	{Name: "vector_column", Required: true,
		Doc: "pipelines[].tables[].vector_column"},
	{Name: "id_column", Doc: "pipelines[].tables[].id_column"},
	{Name: "embedding_llm", Required: true,
		Doc: "pipelines[].embedding_llm"},
	{Name: "rag_llm", Required: true, Doc: "pipelines[].rag_llm"},
	{Name: "provider", Required: true,
		Enum: []string{"anthropic", "openai", "voyage", "ollama"},
		Doc: "embedding_llm/rag_llm provider; rag_llm's enum has no " +
			"voyage"},
	{Name: "model", Required: true,
		Doc: "embedding_llm/rag_llm model"},
	{Name: "api_key", Secret: true,
		Doc: "embedding_llm/rag_llm api_key; required unless " +
			"provider is ollama"},
	{Name: "base_url", Doc: "embedding_llm/rag_llm base_url"},
	{Name: "token_budget",
		Doc: "pipelines[].token_budget or defaults.token_budget, > 0"},
	{Name: "top_n",
		Doc: "pipelines[].top_n or defaults.top_n, > 0"},
	{Name: "system_prompt", Doc: "pipelines[].system_prompt"},
	{Name: "search", Doc: "pipelines[].search"},
	{Name: "hybrid_enabled", Doc: "pipelines[].search.hybrid_enabled"},
	{Name: "vector_weight",
		Doc: "pipelines[].search.vector_weight, 0.0-1.0"},
	{Name: "defaults",
		Doc: "top-level defaults applied to every pipeline"},
}
