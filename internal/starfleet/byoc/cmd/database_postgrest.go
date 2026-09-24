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

// PostgREST configuration bounds, mirrored from the field descriptions
// in byoc.yaml (the schema declares no minimum or maximum) so a bad
// value is rejected before the round trip.
const (
	postgrestMinDBPool     = 1
	postgrestMaxDBPool     = 30
	postgrestMinMaxRows    = 1
	postgrestMaxMaxRows    = 10000
	postgrestMinJWTSecrLen = 32
)

// postgrestServiceOpts collects the deploy/update flag values so
// applyPostgRESTService can assemble the request without reaching for
// package-level state.
type postgrestServiceOpts struct {
	dbSchemas       string
	dbAnonRole      string
	dbPool          int
	maxRows         int
	corsOrigins     string
	jwtSecret       string
	jwtAudience     string
	jwtRoleClaimKey string
	targetNodes     []string
}

// NewDatabasePostgRESTCmd builds the `pgedge starfleet byoc database postgrest`
// command group, which manages the PostgREST API deployed on a
// database.
func NewDatabasePostgRESTCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "postgrest",
		Short: "Manage the PostgREST API on a database",
		Long: `postgrest manages the PostgREST service deployed alongside a
database: the REST API generated directly from the database schema.

deploy creates the service; update reconfigures it. Each refuses to
run in the other's place: deploy fails if one is already deployed,
update fails if none is.

Example:
  pgedge starfleet byoc database postgrest deploy <database_id> \
    --db-schemas public --db-anon-role web_anon
  pgedge starfleet byoc database postgrest update <database_id> --max-rows 500`,
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
		Short: "Deploy a PostgREST API on a database",
		Long: `deploy stands up a PostgREST service alongside a database.

Use it to expose the database's schemas as a REST API. The schemas to
expose and the role used for unauthenticated requests are required.
Pass --wait to block until deployment finishes. deploy creates the
service: if one is already deployed on this database, it fails and
points you at update instead of reconfiguring it.

Example:
  pgedge starfleet byoc database postgrest deploy <database_id> \
    --db-schemas public --db-anon-role web_anon
  pgedge starfleet byoc database postgrest deploy <database_id> \
    --db-schemas public,api --db-anon-role web_anon \
    --max-rows 500 --db-pool 20 --wait`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return applyPostgRESTService(
				rt, cmd, args[0], opts, intentDeploy)
		},
	}
	bindPostgRESTFlags(cmd, opts)
	addWaitFlags(cmd)

	_ = cmd.MarkFlagRequired("db-schemas")
	_ = cmd.MarkFlagRequired("db-anon-role")
	cli.MarkMutating(cmd)

	return cmd
}

// --- update ---

func newDatabasePostgRESTUpdateCmd(rt *module.Runtime) *cobra.Command {
	opts := &postgrestServiceOpts{}
	cmd := &cobra.Command{
		Use:   "update <database_id>",
		Short: "Update the PostgREST API on a database",
		Long: `update changes the PostgREST configuration on a database.

update requires a deployed PostgREST service on this database; if
none is deployed, it fails and points you at deploy instead of
creating one.

Only the flags you pass are changed; every other setting is read from
the deployed service and preserved. Pass --wait to block until the
change finishes.

--jwt-secret is write-only and never returned by the API, so it cannot
be preserved across an update: pass it again whenever you change other
JWT settings.

Example:
  pgedge starfleet byoc database postgrest update <database_id> --max-rows 500
  pgedge starfleet byoc database postgrest update <database_id> \
    --db-schemas public,api --wait`,
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
func bindPostgRESTFlags(cmd *cobra.Command, opts *postgrestServiceOpts) {
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
	f.StringSliceVar(&opts.targetNodes, "target-nodes", nil,
		"Node names to deploy on (e.g. n1,n2). "+
			"Auto-selects if cluster has one node")
}

// --- shared implementation ---

func applyPostgRESTService(
	rt *module.Runtime, cmd *cobra.Command, dbID string,
	opts *postgrestServiceOpts, intent serviceIntent,
) error {
	// Before the client, which resolves credentials: a malformed ID
	// checked after it would exit 5 "no credentials found".
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

	if err := guardServiceIntent(rt,
		db, api.ServiceServiceTypePostgrest, intent,
		cmd.Parent().CommandPath()); err != nil {
		return err
	}

	// Start from the deployed configuration so an update touches only
	// the flags the caller passed; the API rejects an update missing
	// the required db_schemas or db_anon_role.
	cfg := existingPostgRESTConfig(db)
	if err := applyPostgRESTFlags(cmd, opts, &cfg); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(db.ClusterId)
	if err != nil {
		return newExitError(fmt.Sprintf(
			"invalid cluster ID %q on database: %v", db.ClusterId, err), ExitGeneral)
	}

	// Placement is preserved like every other setting: on an update
	// without --target-nodes, keep the hosts the service is already on.
	hostIDs, err := resolveServicePlacement(
		client, db, clusterID, api.ServiceServiceTypePostgrest,
		opts.targetNodes)
	if err != nil {
		return err
	}

	newSvc := api.ServiceConfig{
		ServiceType:     api.ServiceConfigServiceTypePostgrest,
		PostgrestConfig: &cfg,
		HostIds:         &hostIDs,
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
		return fmt.Errorf("apply PostgREST service: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return err
	}

	if err := printUpdatedDatabase(rt, resp.JSON200); err != nil {
		return err
	}
	fmt.Fprintf(rt.Stderr,
		"PostgREST service applied to database %s.\n", output.Sanitize(dbID))
	return trackMutation(rt, client, dbID, priorTaskID)
}

// existingPostgRESTConfig returns a copy of the PostgREST configuration
// already deployed on the database, or a zero config when no PostgREST
// service is present.
func existingPostgRESTConfig(db *api.Database) api.PostgRESTServiceConfig {
	svc := findService(db, api.ServiceServiceTypePostgrest)
	if svc == nil || svc.PostgrestConfig == nil {
		return api.PostgRESTServiceConfig{}
	}
	return *svc.PostgrestConfig
}

// validatePostgRESTFlags refuses every PostgREST flag value a caller
// can get wrong on their own, with ExitUsage. Call it BEFORE
// clientFromCmd, or `--db-pool 0` with no credentials exits 5.
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
		len(opts.jwtSecret) < postgrestMinJWTSecrLen {
		return newExitError(fmt.Sprintf(
			"--jwt-secret must be at least %d characters",
			postgrestMinJWTSecrLen), ExitUsage)
	}

	// deploy alone: an update inherits both fields from the deployed
	// service, which is not readable yet, so applyPostgRESTFlags
	// answers for it. MarkFlagRequired tests only Changed, so
	// `--db-schemas ""` satisfies cobra and arrives here.
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

	// What reaches here is `update --db-schemas ""`: the overlay just
	// blanked a field the deployed service had filled with a value the
	// caller typed, so ExitUsage.
	var missing []string
	if cfg.DbSchemas == "" {
		missing = append(missing, "--db-schemas")
	}
	if cfg.DbAnonRole == "" {
		missing = append(missing, "--db-anon-role")
	}
	if len(missing) > 0 {
		return newExitError(fmt.Sprintf(
			"%s %s required to deploy a PostgREST service",
			joinFlags(missing), pluralIs(len(missing))), ExitUsage)
	}
	return nil
}

// joinFlags renders a flag list for an error message.
func joinFlags(flags []string) string {
	if len(flags) == 1 {
		return flags[0]
	}
	out := ""
	for i, f := range flags {
		switch {
		case i == 0:
			out = f
		case i == len(flags)-1:
			out += " and " + f
		default:
			out += ", " + f
		}
	}
	return out
}

// pluralIs picks the verb form for a list of n items.
func pluralIs(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
