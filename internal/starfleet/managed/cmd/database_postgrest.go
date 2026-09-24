package cmd

import (
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// PostgREST config bounds the API enforces. Checking them here turns a
// 400 round trip into an immediate, specific error.
const (
	postgrestMinDBPool  = 1
	postgrestMaxDBPool  = 30
	postgrestMinMaxRows = 1
	postgrestMaxMaxRows = 10000
	postgrestMinJWTLen  = 32
)

// postgrestServiceOpts collects the deploy/update flag values so
// applyPostgRESTService can assemble the request without reaching for
// package-level state. There is no targetNodes field — see
// mcpServiceOpts.
type postgrestServiceOpts struct {
	dbSchemas       string
	dbAnonRole      string
	dbPool          int
	maxRows         int
	corsOrigins     string
	jwtSecret       string
	jwtAudience     string
	jwtRoleClaimKey string
}

// NewDatabasePostgRESTCmd builds the `pgedge starfleet managed database
// postgrest` command group, which manages the PostgREST API deployed
// on a managed database.
func NewDatabasePostgRESTCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "postgrest",
		Short: "Manage PostgREST on a database (not yet supported)",
		Long: `postgrest manages the PostgREST API deployed alongside a
managed database: the REST interface generated from its schemas.

PostgREST is not yet supported on managed databases: the platform
rejects the service type outright, so deploy and update currently
fail. mcp and rag are the service types that deploy today.

deploy creates the service; update reconfigures it. Each refuses to
run in the other's place: deploy fails if one is already deployed,
update fails if none is.

Example:
  pgedge starfleet managed database postgrest deploy <database_id> \
    --db-schemas public --db-anon-role app_read_only
  pgedge starfleet managed database postgrest update <database_id> --max-rows 500`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newDatabasePostgRESTDeployCmd(rt),
		newDatabasePostgRESTUpdateCmd(rt),
	)
	return cmd
}

// --- deploy ---

func newDatabasePostgRESTDeployCmd(rt *module.Runtime) *cobra.Command {
	opts := &postgrestServiceOpts{}
	cmd := &cobra.Command{
		Use:   "deploy <database_id>",
		Short: "Deploy PostgREST on a database (not yet supported)",
		Long: `deploy stands up a PostgREST API alongside a managed
database.

PostgREST is not yet supported on managed databases: the platform
rejects the service type, so this command currently fails with a
400. mcp and rag are the service types that deploy today.

Use it to expose the database's schemas over REST. --db-schemas and
--db-anon-role are required: they decide what is exposed and which
role unauthenticated requests run as. deploy creates the service: if
one is already deployed on this database, it fails and points you at
update instead of reconfiguring it.

Example:
  pgedge starfleet managed database postgrest deploy <database_id> \
    --db-schemas public --db-anon-role app_read_only
  pgedge starfleet managed database postgrest deploy <database_id> \
    --db-schemas public,api --db-anon-role app_read_only \
    --jwt-secret <32-char-minimum-secret>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return applyPostgRESTService(
				rt, cmd, args[0], opts, intentDeploy)
		},
	}
	bindPostgRESTFlags(cmd, opts)
	_ = cmd.MarkFlagRequired("db-schemas")
	_ = cmd.MarkFlagRequired("db-anon-role")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// --- update ---

func newDatabasePostgRESTUpdateCmd(rt *module.Runtime) *cobra.Command {
	opts := &postgrestServiceOpts{}
	cmd := &cobra.Command{
		Use:   "update <database_id>",
		Short: "Update PostgREST on a database (not yet supported)",
		Long: `update changes the PostgREST configuration on a managed
database.

update requires a deployed PostgREST service on this database; if
none is deployed, it fails and points you at deploy instead of
creating one.

Only the flags you pass are changed; every other setting is read
from the deployed service and preserved.

--jwt-secret is write-only and never returned, so the CLI cannot
read it back, but the API keeps the stored secret when an update
omits it. Pass --jwt-secret only to rotate it.

PostgREST is not yet supported on managed databases: the platform
rejects the service type outright, so deploy always fails with a
400 and no PostgREST service can ever be deployed to update. update
therefore always ends in the guard that fires whenever this database
has no PostgREST service, at exit 1, provided it gets that far. A
malformed ID or an out-of-range flag value is refused at exit 2
first, and credential resolution and the database read both come
before the guard, so unconfigured credentials answer 5 instead.
mcp and rag are the service types that deploy today.

Example:
  pgedge starfleet managed database postgrest update <database_id> --max-rows 500
  pgedge starfleet managed database postgrest update <database_id> \
    --db-schemas public,api`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return applyPostgRESTService(
				rt, cmd, args[0], opts, intentUpdate)
		},
	}
	bindPostgRESTFlags(cmd, opts)
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// bindPostgRESTFlags registers the PostgREST service flags, shared by
// deploy and update.
func bindPostgRESTFlags(
	cmd *cobra.Command, opts *postgrestServiceOpts,
) {
	f := cmd.Flags()
	f.StringVar(&opts.dbSchemas, "db-schemas", "",
		"Comma-separated schemas to expose as REST (e.g. public,api)")
	f.StringVar(&opts.dbAnonRole, "db-anon-role", "",
		"Postgres role used for unauthenticated requests")
	f.IntVar(&opts.dbPool, "db-pool", 0,
		"Database connections to keep open (1-30, default 10)")
	f.IntVar(&opts.maxRows, "max-rows", 0,
		"Maximum rows returned per request (1-10000, default 1000)")
	f.StringVar(&opts.corsOrigins, "cors-origins", "",
		"Comma-separated list of allowed CORS origins")
	f.StringVar(&opts.jwtSecret, "jwt-secret", "",
		"JWT signing secret (min 32 chars); write-only, never returned")
	f.StringVar(&opts.jwtAudience, "jwt-audience", "",
		"JWT audience claim to require")
	f.StringVar(&opts.jwtRoleClaimKey, "jwt-role-claim-key", "",
		"JSONPath to the role claim inside the JWT")
}

// --- shared implementation ---

func applyPostgRESTService(
	rt *module.Runtime, cmd *cobra.Command, dbID string,
	opts *postgrestServiceOpts, intent serviceIntent,
) error {
	// Before the client, deliberately: clientFromCmd resolves
	// credentials, so a malformed ID checked after it reports exit 5
	// "no credentials found" for a mistake the caller can see.
	id, err := parseUUIDArg(dbID, "database ID")
	if err != nil {
		return err
	}
	if err := validatePostgRESTFlags(cmd, opts, intent); err != nil {
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

	if err := guardServiceIntent(rt, db, api.Postgrest, intent,
		cmd.Parent().CommandPath()); err != nil {
		return err
	}

	// Start from the deployed configuration so an update touches only
	// the flags the caller passed. DbSchemas and DbAnonRole are
	// non-pointer required fields, so a config rebuilt from flags would
	// send empty ones and be rejected.
	cfg := existingPostgRESTConfig(db)
	if err := applyPostgRESTFlags(cmd, opts, &cfg); err != nil {
		return err
	}

	newSvc := api.ServiceConfig{
		ServiceType:     api.Postgrest,
		PostgrestConfig: &cfg,
	}

	return applyServices(rt, client, db, buildServiceList(db, newSvc),
		"PostgREST service applied", dbID)
}

// existingPostgRESTConfig returns a copy of the PostgREST
// configuration already deployed on the database, or a zero config
// when no PostgREST service is present.
//
// JwtSecret is always absent: saas's postgrestConfigToModel omits it,
// and saas has tests asserting it must never be returned. As with
// RAG's keys, saas's CarryForwardManagedSecrets restores an omitted
// secret on a reconfiguration — see existingRAGConfig for the
// mechanism and why carrying the service_id is what makes it reach.
func existingPostgRESTConfig(
	db *api.ManagedDatabase,
) api.PostgRESTServiceConfig {
	svc := findService(db, api.Postgrest)
	if svc == nil || svc.PostgrestConfig == nil {
		return api.PostgRESTServiceConfig{}
	}
	return *svc.PostgrestConfig
}

// validatePostgRESTFlags refuses every PostgREST flag value a caller
// can get wrong on their own, with ExitUsage. Call it before
// clientFromCmd, which resolves credentials; otherwise
// `postgrest deploy --db-pool 0` with none configured answers exit 5
// "no credentials found" for a number the caller can see is out of
// range. validatePgVersion runs early for the same reason.
//
// Duplicated from byoc's twin deliberately, like the rest of this
// file: the two run over different generated config types, so a fix to
// one does not fix the other and each carries its own test.
func validatePostgRESTFlags(
	cmd *cobra.Command, opts *postgrestServiceOpts, intent serviceIntent,
) error {
	f := cmd.Flags()

	if f.Changed("db-pool") &&
		(opts.dbPool < postgrestMinDBPool ||
			opts.dbPool > postgrestMaxDBPool) {
		return newExitError(fmt.Sprintf(
			"--db-pool must be between %d and %d, got %d",
			postgrestMinDBPool, postgrestMaxDBPool, opts.dbPool),
			ExitUsage)
	}
	if f.Changed("max-rows") &&
		(opts.maxRows < postgrestMinMaxRows ||
			opts.maxRows > postgrestMaxMaxRows) {
		return newExitError(fmt.Sprintf(
			"--max-rows must be between %d and %d, got %d",
			postgrestMinMaxRows, postgrestMaxMaxRows, opts.maxRows),
			ExitUsage)
	}
	if f.Changed("jwt-secret") &&
		len(opts.jwtSecret) < postgrestMinJWTLen {
		return newExitError(fmt.Sprintf(
			"--jwt-secret must be at least %d characters, got %d",
			postgrestMinJWTLen, len(opts.jwtSecret)), ExitUsage)
	}

	// deploy only: it starts from an empty configuration, so a missing
	// required field is the caller's. An update inherits them from the
	// deployed service, not yet read, so applyPostgRESTFlags checks
	// that case. MarkFlagRequired tests only Changed, so
	// `--db-schemas ""` gets past cobra to here.
	if intent == intentDeploy {
		var missing []string
		if opts.dbSchemas == "" {
			missing = append(missing, "--db-schemas")
		}
		if opts.dbAnonRole == "" {
			missing = append(missing, "--db-anon-role")
		}
		if len(missing) > 0 {
			return newExitError(fmt.Sprintf(
				"%s %s required to deploy a PostgREST service",
				joinFlags(missing), pluralIs(len(missing))), ExitUsage)
		}
	}
	return nil
}

// applyPostgRESTFlags overlays the flags the caller actually set onto
// cfg. Their values are validated by validatePostgRESTFlags, before
// the client is built.
func applyPostgRESTFlags(
	cmd *cobra.Command, opts *postgrestServiceOpts,
	cfg *api.PostgRESTServiceConfig,
) error {
	f := cmd.Flags()

	if f.Changed("db-schemas") {
		cfg.DbSchemas = opts.dbSchemas
	}
	if f.Changed("db-anon-role") {
		cfg.DbAnonRole = opts.dbAnonRole
	}
	if f.Changed("db-pool") {
		cfg.DbPool = &opts.dbPool
	}
	if f.Changed("max-rows") {
		cfg.MaxRows = &opts.maxRows
	}
	if f.Changed("cors-origins") {
		cfg.CorsOrigins = &opts.corsOrigins
	}
	if f.Changed("jwt-secret") {
		cfg.JwtSecret = &opts.jwtSecret
	}
	if f.Changed("jwt-audience") {
		cfg.JwtAudience = &opts.jwtAudience
	}
	if f.Changed("jwt-role-claim-key") {
		cfg.JwtRoleClaimKey = &opts.jwtRoleClaimKey
	}

	// Both fields are required by the API. deploy was checked by
	// validatePostgRESTFlags, so what reaches here is
	// `update --db-schemas ""`, whose overlay blanked a field the
	// deployed service had filled. The caller typed that value, so
	// ExitUsage is right.
	var missing []string
	if cfg.DbSchemas == "" {
		missing = append(missing, "--db-schemas")
	}
	if cfg.DbAnonRole == "" {
		missing = append(missing, "--db-anon-role")
	}
	if len(missing) > 0 {
		return newExitError(fmt.Sprintf(
			"%s cannot be empty: a PostgREST service requires %s",
			joinFlags(missing), pronoun(len(missing))), ExitUsage)
	}
	return nil
}

// pronoun is "it" or "them", for a message naming one flag or several.
func pronoun(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
