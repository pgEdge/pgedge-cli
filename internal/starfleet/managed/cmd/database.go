package cmd

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/projectlink"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// managedDatabaseNameRE is the CLI's client-side check for a managed
// database's --name, mirroring the rules the managed reference
// (llms/database.txt) documents: the name is also a Kubernetes object
// name, so on top of the Postgres identifier rule (must start with a
// letter) it must be lowercase, letters and digits only, with no
// hyphens or underscores.
var managedDatabaseNameRE = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// managedDatabaseNameMaxLen is the documented length ceiling for a
// managed database name.
const managedDatabaseNameMaxLen = 50

// pgVersions are the Postgres majors the managed create endpoint
// accepts, newest first, spelled with the generated constants.
//
// The API validates these against the majors its Postgres image catalog
// publishes, and the version is fixed for the life of the database — a
// PATCH naming pg_version is refused outright. Validating here spends
// exit 2 rather than a create attempt.
//
// The generated enum type has a Valid() method but no way to
// enumerate its members, so a Valid()-only check could not notice a
// fourth major arriving and would go on refusing a version the API
// accepts. TestPgVersionsMatchTheSpecEnum reads the vendored spec and
// is the assertion that fails in that direction.
//
// This is managed's list alone. byoc takes its versions from the
// API's own supported_pg_versions catalog, which is a different
// endpoint with a different vocabulary — do not share this.
var pgVersions = []string{
	string(api.N18),
	string(api.N17),
	string(api.N16),
}

// validatePgVersion rejects a --pg-version outside the contract's
// enum with ExitUsage, and records the pass in the dry-run ledger.
//
// An empty value is an error, not an absent flag: pg_version is fixed
// for the life of the database, so `--pg-version "$PGV"` with the
// variable unset must not create one on the newest major, a version the
// caller neither chose nor can change. Whether the flag was given at
// all is the call site's question, answered with Changed.
//
// Call this before clientFromCmd, which resolves credentials, or a
// malformed version reports exit 5 rather than exit 2.
func validatePgVersion(rt *module.Runtime, v string) error {
	// Named separately from the enum message: "unknown Postgres version
	// \"\"" tells a caller nothing about what went wrong, and an unset
	// shell variable is the way this value arrives.
	if v == "" {
		return newExitError(fmt.Sprintf(
			"--pg-version given an empty value: name a version (%s), "+
				"or omit the flag to use the newest supported",
			strings.Join(pgVersions, ", ")), ExitUsage)
	}
	if !slices.Contains(pgVersions, v) {
		return newExitError(fmt.Sprintf(
			"unknown Postgres version %q (expected one of: %s); "+
				"see 'pgedge starfleet managed pg-version list'",
			v, strings.Join(pgVersions, ", ")), ExitUsage)
	}
	rt.DryRun.Pass("Postgres version %q accepted", v)
	return nil
}

// validateManagedDatabaseName rejects a --name the documented rules
// would refuse, with exit code ExitUsage, before any API call.
//
// The Control Plane's own validation is server-side only and looser
// than what it documents: a unicode name round-trips to a 400
// "database name contains invalid character", and an ASCII name with a
// hyphen or underscore gets the same opaque round trip for a rule the
// reference already states. The CLI sends only what the documented
// contract allows, not the server's broader tolerance, so a bad name
// fails locally.
func validateManagedDatabaseName(name string) error {
	if len(name) > managedDatabaseNameMaxLen {
		return newExitError(fmt.Sprintf(
			"database name %q is %d characters, over the %d-character "+
				"limit", name, len(name), managedDatabaseNameMaxLen),
			ExitUsage)
	}
	if !managedDatabaseNameRE.MatchString(name) {
		return newExitError(fmt.Sprintf(
			"database name %q is invalid: must be lowercase letters "+
				"and digits only, starting with a letter (no hyphens, "+
				"underscores, or other characters)", name),
			ExitUsage)
	}
	return nil
}

// databaseColumns are the table headers shared by database list and
// get.
var databaseColumns = []string{
	"ID", "NAME", "STATUS", "REGION", "SIZE", "PG VERSION", "CREATED",
}

// NewDatabaseCmd builds the `pgedge starfleet managed database` command group.
// The plural "databases" is kept as a plural alias (unlisted in help).
func NewDatabaseCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "database",
		Aliases: []string{"databases"},
		Short:   "Manage pgEdge Managed databases",
		Long: `database manages pgEdge Managed databases: single-master
Postgres databases hosted and operated by pgEdge.

Use these commands to list, inspect, create, update, resize and
delete databases, to rotate their role passwords, and to manage the
services (MCP, RAG, PostgREST) deployed alongside them.

Example:
  pgedge starfleet managed database list
  pgedge starfleet managed database get e5f6a7b8-c9d0-1234-efab-567890123456`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newDatabaseListCmd(rt),
		newDatabaseGetCmd(rt),
		newDatabaseConnectionStringCmd(rt),
		newDatabaseInspectCmd(rt),
		newDatabaseCreateCmd(rt),
		newDatabaseUpdateCmd(rt),
		newDatabaseDeleteCmd(rt),
		newDatabaseResizeCmd(rt),
		newDatabaseRotatePasswordCmd(rt),
		newDatabaseMetricsCmd(rt),
		newDatabaseLogsCmd(rt),
		newDatabaseLinkCmd(rt),
		newDatabaseUnlinkCmd(rt),
		newDatabaseEnvCmd(rt),
		NewDatabaseBranchCmd(rt),
		NewDatabaseAllowlistCmd(rt),
		NewDatabaseServiceCmd(rt),
		NewDatabaseMCPCmd(rt),
		NewDatabasePostgRESTCmd(rt),
		NewDatabaseRAGCmd(rt),
	)
	return cmd
}

// --- list ---

func newDatabaseListCmd(rt *module.Runtime) *cobra.Command {
	var (
		region     string
		limit      int
		offset     int
		descending bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List managed databases",
		Long: `list shows the managed databases in the active account.

Use it to find a database's ID before running get, update, resize or
delete. Filter with --region, and page through large accounts with
--limit and --offset.

Example:
  pgedge starfleet managed database list
  pgedge starfleet managed database list --region us-east-1 -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// databaseLimitMax, not the module's own idea of a page:
			// /managed/v1/databases declares {min 1, max 1000} while
			// /managed/v1/backups declares {min 1, max 100} for the
			// same flag name.
			limit, sendLimit, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "limit", cli.LimitLowest, databaseLimitMax)
			if err != nil {
				return err
			}
			offset, sendOffset, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "offset", cli.OffsetLowest, cli.NoUpperBound)
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			params := &api.ListManagedDatabasesParams{}
			if region != "" {
				params.Region = &region
			}
			if sendLimit {
				params.Limit = &limit
			}
			if sendOffset {
				params.Offset = &offset
			}
			if descending {
				params.Descending = &descending
			}

			resp, err := client.ListManagedDatabasesWithResponse(
				context.Background(), params)
			if err != nil {
				return fmt.Errorf("list databases: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			databases := resp.JSON200
			if databases == nil || len(*databases) == 0 {
				fmt.Fprintln(rt.Stderr, "No databases found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*databases))
			for _, d := range *databases {
				rows = append(rows, databaseRowFrom(d))
			}
			if err := rt.Output.Print(rows, databaseColumns); err != nil {
				return err
			}
			cli.PrintTruncationHint(
				rt, len(*databases), limit, databasePageDefaults,
				"results")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&region, "region", "", "Filter by region")
	f.IntVar(&limit, "limit", 0,
		fmt.Sprintf("Maximum number of results to return (%d-%d)",
			cli.LimitLowest, databaseLimitMax))
	f.IntVar(&offset, "offset", 0,
		"Offset into the results for pagination")
	f.BoolVar(&descending, "descending", false,
		"Sort in descending order")
	return cmd
}

// --- get ---

func newDatabaseGetCmd(rt *module.Runtime) *cobra.Command {
	var userType string
	cmd := &cobra.Command{
		Use:   "get [<database_id>]",
		Short: "Show managed database details",
		Long: `get shows the details of a single managed database.

Use it to check a database's status, size, region and connection
details. The argument takes a full UUID. In a folder linked with
'database link', the ID can be left out.

Deployed services are listed underneath, with the URL each one is
reached at. Their configurations carry secrets and are shown only in
-o json or -o yaml.

Pass --user-type to choose which role's credentials come back in the
connection block: admin, app or app_read_only. Omit it and app's come
back, which is the role an application connects as for day-to-day
work. app_read_only is the read-only role RAG connects as, and MCP
unless its writes are allowed.

Example:
  pgedge starfleet managed database get e5f6a7b8-c9d0-1234-efab-567890123456
  pgedge starfleet managed database get e5f6a7b8-c9d0-1234-efab-567890123456 \
    --user-type admin -o yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var wireUserType api.GetManagedDatabaseParamsUserType
			// Changed, not `!= ""`: an explicitly empty --user-type is
			// a value outside the vocabulary, not an absent flag, so
			// `--user-type "$ROLE"` with the variable unset is refused
			// like `--user-type bogus` rather than answered for the
			// default role.
			if cmd.Flags().Changed("user-type") {
				if userType == "" {
					return newExitError(
						"--user-type given an empty value: name a role "+
							"(admin, app or app_read_only), or omit the flag to use "+
							"app", ExitUsage)
				}
				var err error
				wireUserType, err = parseUserType(userType)
				if err != nil {
					return err
				}
			}

			id, _, err := databaseArg(rt, args, 0)
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			params := &api.GetManagedDatabaseParams{}
			if wireUserType != "" {
				params.UserType = &wireUserType
			}

			resp, err := client.GetManagedDatabaseWithResponse(
				context.Background(), id, params)
			if err != nil {
				return fmt.Errorf("get database: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			d := resp.JSON200
			if d == nil {
				fmt.Fprintln(rt.Stderr, "No database data returned.")
				return nil
			}
			return printDatabaseDetail(rt, d)
		},
	}
	cmd.Flags().StringVar(&userType, "user-type", "",
		"Role whose credentials to return: admin, app or "+
			"app_read_only (default app)")
	return cmd
}

// --- create ---

func newDatabaseCreateCmd(rt *module.Runtime) *cobra.Command {
	var (
		name        string
		region      string
		size        string
		displayName string
		pgVersion   string
		options     []string
		allow       []string
		myIP        bool
		open        bool
		link        bool
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a managed database",
		Long: `create provisions a new managed database.

Use it to stand up a Postgres database pgEdge hosts and operates.
There is no cluster to choose: --region and --size describe where it
runs and how large it is.

--region may be omitted while the API publishes a single region: the
CLI reads the region list and sends the only value. If several are
published, omitting it is an error — a region is fixed for the life of
the database, so the CLI will not choose between them.

Creating a managed database requires a payment method on the
account.

--size and --region are each checked against the list the API
publishes for it, so a value its own list does not carry is refused
with exit 2 before the create is sent. Each check needs one read, so
both report exit 5 rather than exit 2 when no credentials are
configured -- the CLI cannot know a size is wrong without asking.

--pg-version is fixed for the life of the database: it cannot be
changed later, and omitting it takes the newest supported major. A
version outside the set the API publishes is refused locally with
exit 2, before any request, and so is an empty value — omit the flag
rather than passing "" to take the default.

A new database is closed: no address can connect until a rule is
added. Give --allow <cidr> (repeatable), --my-ip, or --open to set
the Postgres endpoint's allowlist at create time; with none of them
the database is created closed and the CLI says so. --open admits
every address and cannot be combined with the other two. Services
get their own allowlists when they are deployed.

--link links the current folder to the new database once it is
available, as 'database link' does, so 'pgedge env pull' can write
its DATABASE_URL next. It needs --wait or --follow, and a folder
already linked to another database is refused before the create is
sent.

Example:
  pgedge starfleet managed database create --name mydb \
    --region us-east-1 --size small
  pgedge starfleet managed database create --name mydb \
    --region us-east-1 --size large --pg-version 16
  pgedge starfleet managed database create --name mydb \
    --region us-east-1 --size small --my-ip
  pgedge starfleet managed database create --name myapp \
    --size small --my-ip --wait --link`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateManagedDatabaseName(name); err != nil {
				return err
			}
			var linkDir string
			if link {
				if !waitFlag && !followFlag {
					return newExitError("--link needs --wait or --follow: "+
						"a database still being created has no connection "+
						"to write", ExitUsage)
				}
				wd, err := projectFolder()
				if err != nil {
					return err
				}
				existing, err := projectlink.Read(wd)
				if err != nil {
					return newExitError(fmt.Sprintf("read project link: %v", err), ExitGeneral)
				}
				if existing != nil {
					return newExitError(fmt.Sprintf(
						"%s already links database %s; run 'database unlink' "+
							"first, or create without --link",
						existing.Path, existing.DatabaseID), ExitGeneral)
				}
				linkDir = wd
				rt.DryRun.Pass("%s holds no link", wd)
			}
			rt.DryRun.Pass("database name %q accepted", name)

			// Both client-side checks run BEFORE the client is
			// built, and that ordering is the whole point of them.
			// clientFromCmd resolves credentials, so a check placed
			// after it answers exit 5 "no credentials found" for a
			// malformed value — the wrong code — and a check placed
			// after the first request has already spent the round
			// trip it exists to save.
			//
			// Changed, not `pgVersion != ""`: an explicitly empty value
			// is a value, and only the flag set can tell it from an
			// absent flag.
			if cmd.Flags().Changed("pg-version") {
				if err := validatePgVersion(rt, pgVersion); err != nil {
					return err
				}
			}

			// --allow alone can be sized before the client is built;
			// --my-ip cannot, since its one entry comes from a read
			// that needs the client, so its bound is checked again
			// after that read below.
			if err := checkAllowlistBounds(
				newRules(allow, ""), ""); err != nil {
				return err
			}

			// Before the client, for the reason the checks above
			// are: a name the caller typed too long must answer 2,
			// not exit 5 for credentials it never needed. update
			// takes the same limit.
			if cmd.Flags().Changed("display-name") {
				if err := conn.ValidateDisplayName(
					displayName); err != nil {
					return err
				}
			}

			// The blank halves of --size and --region, refused here
			// while it is still free. Whether the value NAMES a real
			// size or region needs the catalog, and that read cannot
			// happen before the client -- see catalog.go. Emptiness
			// can, so it stays at exit 2 with the checks above.
			size, err := cli.RequiredStringFlag(cmd.Flags(), "size",
				"name a size from `pgedge starfleet managed size list`")
			if err != nil {
				return err
			}
			region, sendRegion, err := cli.OptionalStringFlag(
				cmd.Flags(), "region",
				"name a region from `pgedge starfleet managed region "+
					"list`, or omit the flag while the API publishes "+
					"only one")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			ctx := context.Background()
			if sendRegion {
				if err := validateRegion(
					ctx, rt, client, region); err != nil {
					return err
				}
			} else {
				region, err = resolveSoleRegion(rt, client)
				if err != nil {
					return err
				}
			}
			if err := validateSize(ctx, rt, client, size); err != nil {
				return err
			}

			body := api.CreateManagedDatabaseJSONRequestBody{
				Name:   name,
				Region: region,
				Size:   size,
			}
			if cmd.Flags().Changed("display-name") {
				body.DisplayName.Set(displayName)
			}
			if cmd.Flags().Changed("pg-version") {
				v := api.CreateManagedDatabaseInputPgVersion(pgVersion)
				body.PgVersion = &v
			}
			if cmd.Flags().Changed("options") {
				body.Options = &options
			}

			var rules []api.IPAllowlistRule
			switch {
			case open:
				rules = []api.IPAllowlistRule{{Cidr: allowlistOpenCIDR}}
			case len(allow) > 0 || myIP:
				inputs := allow
				if myIP {
					mine, err := fetchClientIP(rt, client)
					if err != nil {
						return err
					}
					inputs = append(inputs, mine)
				}
				rules = newRules(inputs, "")
				if err := checkAllowlistBounds(rules, ""); err != nil {
					return err
				}
			}
			if rules != nil {
				body.IpAllowlist = &api.IPAllowlist{Rules: rules}
			}

			resp, err := client.CreateManagedDatabaseWithResponse(
				context.Background(), body)
			if err != nil {
				return fmt.Errorf("create database: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			d := resp.JSON200
			if d == nil {
				fmt.Fprintln(rt.Stderr,
					"Database created (no details returned).")
				return nil
			}

			if rt.Output.Structured() {
				if err := rt.Output.Print(d, nil); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(rt.Stderr,
					"Database %q created (id: %s, status: %s).\n",
					d.Name, output.Sanitize(d.Id), output.Sanitize(d.Status))
			}
			switch rules {
			case nil:
				fmt.Fprintf(rt.Stderr, "Created closed: no address can "+
					"connect until you allow one.\nAllow yours:  pgedge "+
					"starfleet managed database allowlist add %s --my-ip\n",
					output.Sanitize(d.Id))
			default:
				warnIfOpen(rt, rules, "", d.Id)
			}
			// A brand-new database has no prior tasks, so the first
			// task seen for it is the one this create spawned.
			if err := trackMutation(rt, client, d.Id, taskBaseline{}); err != nil {
				return err
			}
			if linkDir == "" {
				return nil
			}
			path, err := projectlink.Write(linkDir, projectlink.Link{
				Module: projectlink.ModuleManaged, DatabaseID: d.Id,
			})
			if err != nil {
				return newExitError(fmt.Sprintf("database %s was created, "+
					"but the link was not written: %v; run 'database link %s'",
					d.Id, err, d.Id), ExitGeneral)
			}
			fmt.Fprintf(rt.Stderr, "Linked %s to database %s. Wrote %s.\n",
				output.Sanitize(linkDir), output.Sanitize(d.Id), output.Sanitize(path))
			return nil
		},
	}
	f := cmd.Flags()
	addWaitFlags(cmd)
	f.StringVar(&name, "name", "", "Database name")
	f.StringVar(&region, "region", "",
		"Region to provision the database in (e.g. us-east-1); "+
			"checked against 'region list'; optional while the API "+
			"publishes only one, and required as soon as it "+
			"publishes more")
	f.StringVar(&size, "size", "",
		"Managed size name (e.g. small, large); checked against "+
			"'size list'")
	f.StringVar(&displayName, "display-name", "",
		fmt.Sprintf("Display name for the database, at most %d "+
			"characters", conn.DisplayNameMaxLen))
	f.StringVar(&pgVersion, "pg-version", "",
		"Postgres major version to create the database on: "+
			strings.Join(pgVersions, ", ")+
			" (fixed for the life of the database; "+
			"defaults to the newest supported version)")
	f.StringSliceVar(&options, "options", nil,
		"Comma-separated list of options")
	f.StringArrayVar(&allow, "allow", nil,
		"IPv4 address or CIDR block allowed to reach the Postgres "+
			"endpoint; repeat for more than one")
	f.BoolVar(&myIP, "my-ip", false,
		"Also allow the address the API sees this command arriving from")
	f.BoolVar(&open, "open", false,
		"Admit every address (one 0.0.0.0/0 rule); never the default")
	f.BoolVar(&link, "link", false,
		"Link the current folder to the new database (needs --wait or --follow)")
	cmd.MarkFlagsMutuallyExclusive("open", "allow")
	cmd.MarkFlagsMutuallyExclusive("open", "my-ip")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("size")
	cli.MarkMutating(cmd)

	return cmd
}

// resolveSoleRegion supplies --region when the API publishes exactly
// one, and refuses when it publishes several.
//
// The API requires a region and has no default, so something must be
// sent; that is not a reason to make a human type the only value on
// offer. Today `region list` returns one.
//
// It refuses rather than guessing whenever there is a choice to get
// wrong. A region is fixed for the life of the database, so an implicit
// pick from two cannot be undone, and refusing means that the day a
// second region appears, scripts relying on the resolution fail loudly,
// naming the regions to choose between.
//
// This runs after clientFromCmd because it needs the API, unlike the
// name and pg-version checks above. It costs one read, and only on a
// create that omitted the flag.
func resolveSoleRegion(
	rt *module.Runtime, client *api.ClientWithResponses,
) (string, error) {
	resp, err := client.ListManagedRegionsWithResponse(context.Background())
	if err != nil {
		return "", fmt.Errorf("list regions: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return "", err
	}

	var names []string
	if resp.JSON200 != nil {
		for _, r := range *resp.JSON200 {
			names = append(names, r.Region)
		}
	}

	switch len(names) {
	case 0:
		return "", newExitError(
			"--region is required: the API published no regions to "+
				"choose from", ExitUsage)
	case 1:
		rt.DryRun.Pass("region %q is the only one published", names[0])
		return names[0], nil
	default:
		slices.Sort(names)
		return "", newExitError(fmt.Sprintf(
			"--region is required: the API publishes %d regions (%s). "+
				"A region is fixed for the life of the database, so the "+
				"CLI will not choose one for you",
			len(names), strings.Join(names, ", ")), ExitUsage)
	}
}

// --- update ---

func newDatabaseUpdateCmd(rt *module.Runtime) *cobra.Command {
	var (
		displayName        string
		options            []string
		deletionProtection bool
	)
	cmd := &cobra.Command{
		Use:   "update <database_id>",
		Short: "Update a managed database",
		Long: `update changes a managed database's display name, options
or deletion protection.

Only the flags you pass are changed. Size is not updatable here —
use 'pgedge starfleet managed database resize'. Services are managed by the
mcp, rag, postgrest and service subcommands.

With --deletion-protection, the API refuses to delete the database
until it is turned off again. That is enforced server-side, so it
holds for the web UI and any other client too, not just this one.

The argument takes a full UUID.

Example:
  pgedge starfleet managed database update e5f6a7b8-c9d0-1234-efab-567890123456 \
    --display-name "My Database"
  pgedge starfleet managed database update e5f6a7b8-c9d0-1234-efab-567890123456 \
    --options key1,key2
  pgedge starfleet managed database update e5f6a7b8-c9d0-1234-efab-567890123456 \
    --deletion-protection
  pgedge starfleet managed database update e5f6a7b8-c9d0-1234-efab-567890123456 \
    --deletion-protection=false`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before the client: a name the caller typed too long
			// must answer 2, not exit 5 for credentials it never
			// needed. create takes the same limit.
			if cmd.Flags().Changed("display-name") {
				if err := conn.ValidateDisplayName(
					displayName); err != nil {
					return err
				}
			}

			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			body := api.UpdateManagedDatabaseJSONRequestBody{}
			if cmd.Flags().Changed("display-name") {
				body.DisplayName.Set(displayName)
			}
			if cmd.Flags().Changed("options") {
				body.Options = &options
			}
			// Only sent when asked for. The field is a plain
			// *bool, so leaving it nil is what "don't change it"
			// looks like on the wire — sending false would turn
			// protection off on every unrelated update.
			if cmd.Flags().Changed("deletion-protection") {
				body.DeletionProtection = &deletionProtection
			}

			resp, err := client.UpdateManagedDatabaseWithResponse(
				context.Background(), id, body)
			if err != nil {
				return fmt.Errorf("update database: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			d := resp.JSON200
			if d == nil {
				fmt.Fprintf(rt.Stderr, "Database %s updated.\n", output.Sanitize(args[0]))
				return nil
			}
			fmt.Fprintf(rt.Stderr,
				"Database %q updated (id: %s, status: %s).\n",
				d.Name, output.Sanitize(d.Id), output.Sanitize(d.Status))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&displayName, "display-name", "",
		fmt.Sprintf("Display name for the database, at most %d "+
			"characters", conn.DisplayNameMaxLen))
	f.StringSliceVar(&options, "options", nil,
		"Comma-separated list of options")
	f.BoolVar(&deletionProtection, "deletion-protection", false,
		"Refuse deletion until this is turned off again")
	cli.MarkMutating(cmd)

	return cmd
}

// --- delete ---

func newDatabaseDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force, deleteBranches bool
	cmd := &cobra.Command{
		Use:   "delete <database_id>",
		Short: "Delete a managed database",
		Long: `delete tears down a managed database.

Deletion is destructive, so it prompts for confirmation unless
--force is given. --force only skips the prompt; a database with
branches is refused on its own. Pass --delete-branches to also
delete every branch first -- branch data cannot be recovered. The
argument takes a full UUID.

Example:
  pgedge starfleet managed database delete e5f6a7b8-c9d0-1234-efab-567890123456
  pgedge starfleet managed database delete e5f6a7b8-c9d0-1234-efab-567890123456 \
    --force
  pgedge starfleet managed database delete e5f6a7b8-c9d0-1234-efab-567890123456 \
    --delete-branches --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before the prompt. A parse costs nothing, so confirming
			// an operation whose ID cannot name anything wastes the
			// operator's answer -- and, on a scripted run without
			// --force, buries the real fault under a prompt refusal.
			// It also makes the shipped example reachable by
			// TestShippedExamplesAreNotMalformed, which waives the
			// destructive-verb refusal and so cannot see a bad ID
			// sitting behind it.
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Delete database %s? This cannot be undone.", id)
			if deleteBranches {
				prompt = fmt.Sprintf(
					"Delete database %s and every one of its branches? "+
						"This cannot be undone.", id)
			}
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			// Captured before the mutation so waiting can tell the
			// delete task apart from the database's earlier ones.
			base := captureTaskBaseline(client, id.String())

			// The API's force parameter, a cascading delete, comes
			// from --delete-branches, not --force, which only skips
			// the prompt. Otherwise a script that always passed
			// --force would start destroying branches the day one
			// gets created.
			var params *api.DeleteManagedDatabaseParams
			if deleteBranches {
				params = &api.DeleteManagedDatabaseParams{Force: &deleteBranches}
			}

			// Untyped call: delete has no typed 2xx case. See
			// checkEmptyBodyResponse.
			resp, err := client.DeleteManagedDatabase(
				context.Background(), id, params)
			if err != nil {
				return fmt.Errorf("delete database: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "delete database"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr, "Database %s deleted.\n", id)
			return trackMutation(rt, client, id.String(), base)
		},
	}
	addWaitFlags(cmd)
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cmd.Flags().BoolVar(&deleteBranches, "delete-branches", false,
		"Also delete every branch first; data cannot be recovered")
	cli.MarkMutating(cmd)

	return cmd
}

// --- row adapter ---

type databaseRow struct {
	id, name, status, region, size, pgVersion, created string
}

func (r databaseRow) Columns() []string {
	return []string{
		r.id,
		r.name,
		output.ColorStatus(r.status),
		r.region,
		r.size,
		r.pgVersion,
		r.created,
	}
}

// printDatabaseDetail renders a single database in text mode: the
// summary row, a Services section when the API returns any, and an
// always-printed Allowlists summary, in the nested-section shape
// `controlplane database get` uses rather than a second one.
//
// Services are listed because `database get` is where a user looks for
// "how do I call this", but only the four columns `service list` shows.
// The configs carry secrets, the MCP bearer token and embedding API
// keys, and this endpoint is the one path that returns them, so they
// stay in `-o json`, where a caller has asked for the raw object.
func printDatabaseDetail(rt *module.Runtime, d *api.ManagedDatabase) error {
	rows := []output.Row{databaseRowFrom(*d)}
	if err := rt.Output.Print(rows, databaseColumns); err != nil {
		return err
	}
	// display_name has no column in the shared list/get table, so it
	// is printed as a line rather than an eighth column, keeping
	// `database list` its width, and only when set: an absent display
	// name is the common case and a blank label would be noise.
	if dn, err := d.DisplayName.Get(); err == nil && dn != "" {
		fmt.Fprintf(rt.Output.Out, "\nDisplay name: %s\n",
			output.Sanitize(dn))
	}
	if d.Services != nil && len(*d.Services) > 0 {
		svcRows := make([]output.Row, 0, len(*d.Services))
		for _, svc := range *d.Services {
			svcRows = append(svcRows, serviceRow(svc))
		}
		fmt.Fprintf(rt.Output.Out, "\nServices\n")
		if err := rt.Output.Print(svcRows, serviceColumns); err != nil {
			return err
		}
	}
	// Always printed: closed is the notable posture on a fresh
	// database, and a section that vanished when empty would hide it.
	fmt.Fprintf(rt.Output.Out, "\nAllowlists\n")
	return rt.Output.Print(allowlistSummaryRows(d), allowlistSummaryColumns)
}

func databaseRowFrom(d api.ManagedDatabase) databaseRow {
	pgVersion := ""
	if d.PgVersion != nil {
		pgVersion = *d.PgVersion
	}
	return databaseRow{
		id:        d.Id,
		name:      d.Name,
		status:    d.Status,
		region:    d.Region,
		size:      d.Size,
		pgVersion: pgVersion,
		created:   output.FormatTime(d.CreatedAt),
	}
}
