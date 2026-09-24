package svccfg

// PostgRESTConfig mirrors control-plane
// server/internal/database/postgrest_service_config.go:14-23
// (PostgRESTServiceConfig) at v0.10.0 (openapi/SOURCE CP_TAG).
// DBAnonRole is the only required key (:58-59) — config: {} is
// therefore also a guaranteed 400 for postgrest, like rag.
type PostgRESTConfig struct {
	DBSchemas                string  `json:"db_schemas"`
	DBAnonRole               string  `json:"db_anon_role"`
	DBPool                   int     `json:"db_pool"`
	MaxRows                  int     `json:"max_rows"`
	JWTSecret                *string `json:"jwt_secret,omitempty"`
	JWTAud                   *string `json:"jwt_aud,omitempty"`
	JWTRoleClaimKey          *string `json:"jwt_role_claim_key,omitempty"`
	ServerCORSAllowedOrigins *string `json:"server_cors_allowed_origins,omitempty"`
}

// postgrestKeys mirrors postgrestKnownKeys
// (server/internal/database/postgrest_service_config.go:25-34) at
// v0.10.0, 8 flat keys — no nesting, unlike rag.
var postgrestKeys = []KeyMeta{
	{Name: "db_anon_role", Required: true,
		Doc: "required, non-empty; the only required postgrest key"},
	{Name: "db_schemas", Doc: "default \"public\""},
	{Name: "db_pool", Doc: "1-30, default 10"},
	{Name: "max_rows", Doc: "1-10000, default 1000"},
	{Name: "jwt_secret", Secret: true, Doc: ">= 32 characters"},
	{Name: "jwt_aud", Doc: "free string"},
	{Name: "jwt_role_claim_key", Doc: "free string"},
	{Name: "server_cors_allowed_origins", Doc: "free string"},
}
