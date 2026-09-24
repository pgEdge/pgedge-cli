package cmd

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/spf13/cobra"
)

// byocDatabaseNameMaxLen is the byoc API's ceiling for a database name
// (saas pgutil.ValidateDatabaseName). It is measured in BYTES, as
// saas's own len() is, not runes.
//
// It is NOT the managed limit. Managed names go through
// ValidateK8sCompatibleDatabaseName, which caps at 50 to leave
// headroom for CNPG-derived child resource names; a byoc name is only
// ever a Postgres identifier, so it gets Postgres's 63.
const byocDatabaseNameMaxLen = 63

// validateByocDatabaseName rejects a --name the byoc API would refuse,
// with ExitUsage, before any API call.
//
// It mirrors saas's rule EXACTLY rather than the documented one, and
// the difference matters in both directions.
//
// saas normalises before it validates: the create path does
// strings.ToLower(strings.TrimSpace(name)) and validates THAT
// (clusters/svc/database_service.go). So `--name MyDB` is accepted
// today and creates `mydb`, and `--name " mydb "` is accepted and
// creates `mydb`. A checker that enforced the documented "lowercase"
// rule literally would start rejecting invocations that work now, for
// a rule the server implements by coercion rather than refusal. The
// name is therefore normalised here for the PURPOSE of the check only;
// what goes on the wire is what the user typed, so this function
// changes no request it accepts.
//
// This is deliberately NOT the managed twin's approach. That one
// (validateManagedDatabaseName) narrows to an ASCII regexp, refusing
// input the server would take, because the documented managed contract
// is narrower than the server's tolerance and the SKILL already stated
// the tighter rule. Here the documented and enforced rules agree
// except on case, so there is nothing to narrow to — and narrowing
// anyway would only invent a new way to fail. A client-side pre-check
// exists to save a round trip on a certain rejection; the moment it
// refuses something the server accepts, it stops being a pre-check and
// becomes a second, competing API.
//
// unicode.IsLetter and unicode.IsDigit are used rather than an ASCII
// class for the same reason: saas uses exactly those, so `café` is a
// legal byoc database name and must not be rejected here.
//
// Of the two normalisations, TrimSpace is the one that visibly changes
// which names are accepted. ToLower changes no rune's class — the
// tests below are case-insensitive by construction — and earns its
// place only on the length check, which counts BYTES. Exactly two
// runes in Unicode grow under Go's ToLower (U+023A and U+023E, each
// two bytes lowering to three); twenty-three shrink. So a name can sit
// under the limit as typed and over it as stored, and saas measures
// the stored form. Pinned by the "over the limit only once lowercased"
// case in TestValidateByocDatabaseName; without it, deleting the
// ToLower passes every other test.
//
// Messages quote the name AS TYPED. Reporting the normalised form
// would show the user a string they never wrote — worst on the length
// message, where a 62-byte name is over a 63-byte limit only after
// lowercasing, so the message says so rather than quoting a byte count
// the user cannot reproduce from their own input.
func validateByocDatabaseName(name string) error {
	normalized := strings.ToLower(strings.TrimSpace(name))

	// Empty is checked first, and locally, because saas handles it
	// badly. The API handler rejects a literally empty Name with 400
	// "name required", but a whitespace-only name passes that guard and
	// trims to empty inside the service, where ValidateDatabaseName
	// indexes runes[0] on an empty slice. Refusing it here means the
	// CLI never sends the input that reaches that path.
	if normalized == "" {
		return newExitError(
			"database name is required and cannot be blank", ExitUsage)
	}
	if len(normalized) > byocDatabaseNameMaxLen {
		// "once lowercased" is not padding: the API stores the
		// lowercased form and measures that, and two Unicode runes grow
		// a byte when lowered. Without the clause, a user who counted
		// 62 bytes is told they wrote 93.
		return newExitError(fmt.Sprintf(
			"database name %q is %d bytes once lowercased, over the "+
				"%d-byte limit", name, len(normalized),
			byocDatabaseNameMaxLen), ExitUsage)
	}

	runes := []rune(normalized)
	if !unicode.IsLetter(runes[0]) && runes[0] != '_' {
		return newExitError(fmt.Sprintf(
			"database name %q is invalid: must start with a letter or "+
				"an underscore", name), ExitUsage)
	}
	for _, ch := range runes {
		if unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_' {
			continue
		}
		// Hyphens get their own sentence because they are the mistake
		// this check exists to catch: cluster, node and backup-store
		// names in this same module all take hyphens, so reaching for
		// one here is the natural error.
		hint := ""
		if ch == '-' {
			hint = " (database names take underscores, not hyphens — " +
				"unlike cluster and node names)"
		}
		return newExitError(fmt.Sprintf(
			"database name %q is invalid: %q is not allowed; use "+
				"letters, digits and underscores only%s",
			name, string(ch), hint), ExitUsage)
	}
	return nil
}

// databaseGetColumns are the table headers for database get.
var databaseGetColumns = []string{
	"ID", "NAME", "STATUS", "PG VERSION", "CLUSTER", "CREATED",
}

// databaseListColumns are database list's, and they omit PG VERSION
// because the LIST endpoint does not send it (#219).
//
// The two readers return the same generated Database type, so the
// column was declared once and honest on one of its two readers: `get`
// answers `"pg_version": "18"` while every list row omits the key
// entirely. Measured on prod and a BYOC dev tenant -- eight databases across
// three tenants, blank on list in every one, populated by get. saas
// has two converters reading two sources and the list repository does
// not hydrate the stored config version.
//
// A blank cell cannot say which of three things it means -- unknown,
// unset, or not sent -- so the column is dropped from list rather than
// left to be explained in prose. Ant ruled this on 2026-08-21: drop it
// from list, keep it on get.
//
// TestDatabaseRowMatchesItsColumnSet is what keeps the cells and the
// headers in step; a row carrying one more cell than its header set
// renders an unlabelled column rather than failing.
var databaseListColumns = []string{
	"ID", "NAME", "STATUS", "CLUSTER", "CREATED",
}

// NewDatabaseCmd builds the `pgedge starfleet byoc database` command group. The
// plural "databases" is kept as a plural alias (unlisted in help) so
// existing scripts keep working.
func NewDatabaseCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "database",
		Aliases: []string{"databases"},
		Short:   "Manage pgEdge BYOC databases",
		Long: `database manages the pgEdge BYOC databases that run on a
cluster: the Postgres databases your applications connect to.

Use these commands to list, inspect, create, update, and delete
databases, and to manage the services (MCP, RAG) deployed alongside
them.

Example:
  pgedge starfleet byoc database list
  pgedge starfleet byoc database get f6a7b8c9-d0e1-2345-fabc-456789012345`,
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
		newDatabaseRotatePasswordCmd(rt),
		newDatabaseRestoreCmd(rt),
		newDatabaseLogsCmd(rt),
		newDatabaseMetricsCmd(rt),
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
		clusterID string
		limit     int
		offset    int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List databases",
		Long: `list shows the databases in the active account.

Use it to find a database's ID before running get, update, or
delete. Filter with --cluster-id, and page through large accounts
with --limit and --offset.

Example:
  pgedge starfleet byoc database list
  pgedge starfleet byoc database list --cluster-id <cluster_id> -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Before the client: a bad --limit is knowable locally, so
			// it answers 2 rather than 5 for credentials it never needed.
			// byoc.yaml declares no paging bounds on any list endpoint,
			// hence NoUpperBound: the server clamps at 100 today, but a
			// measured clamp is not a published contract and the CLI must
			// not refuse a value the API would accept.
			limit, sendLimit, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "limit", cli.LimitLowest, cli.NoUpperBound)
			if err != nil {
				return err
			}
			offset, sendOffset, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "offset", cli.OffsetLowest, cli.NoUpperBound)
			if err != nil {
				return err
			}
			cluster, sendCluster, err := cli.OptionalStringFlag(
				cmd.Flags(), "cluster-id",
				"pass the full UUID from `cluster list`, or omit the "+
					"flag to list databases in every cluster")
			if err != nil {
				return err
			}
			// The PARSE belongs here too, not beside the params it
			// fills. An earlier version checked only emptiness before
			// the client and left the UUID parse in the params block
			// below, so `--cluster-id not-a-uuid` answered 5 for
			// missing credentials instead of 2 for a value the caller
			// can see. Found by the flag sweep in internal/clitest,
			// which exists because the positional walk could not.
			var clusterUUID uuid.UUID
			if sendCluster {
				clusterUUID, err = parseUUIDArg(cluster, "cluster ID")
				if err != nil {
					return err
				}
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			params := &api.ListDatabasesParams{}
			if sendCluster {
				params.ClusterId = &clusterUUID
			}
			if sendLimit {
				params.Limit = &limit
			}
			if sendOffset {
				params.Offset = &offset
			}

			resp, err := client.ListDatabasesWithResponse(
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
				rows = append(rows, databaseListRowFrom(d))
			}
			if err := rt.Output.Print(
				rows, databaseListColumns); err != nil {
				return err
			}
			cli.PrintTruncationHint(rt, len(*databases), limit,
				databaseDefaults, "results")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&clusterID, "cluster-id", "",
		"Filter by cluster (full UUID)")
	f.IntVar(&limit, "limit", 0,
		cli.LimitFlagHelp(databaseDefaults))
	f.IntVar(&offset, "offset", 0,
		"Offset into the results for pagination")
	return cmd
}

// --- get ---

func newDatabaseGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <database_id>",
		Short: "Show database details",
		Long: `get shows the details of a single database.

Use it to check a database's status, Postgres version, and
cluster. The argument takes a full UUID.

Example:
  pgedge starfleet byoc database get f6a7b8c9-d0e1-2345-fabc-456789012345
  pgedge starfleet byoc database get f6a7b8c9-d0e1-2345-fabc-456789012345 -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetDatabaseWithResponse(
				context.Background(), id, &api.GetDatabaseParams{})
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
			rows := []output.Row{databaseRowFrom(*d)}
			return rt.Output.Print(rows, databaseGetColumns)
		},
	}
}

// --- create ---

func newDatabaseCreateCmd(rt *module.Runtime) *cobra.Command {
	var (
		name      string
		clusterID string
		pgVersion string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a database",
		Long: `create provisions a new database on an existing cluster.

Use it to stand up a Postgres database applications can connect
to. Pass --wait to block until provisioning finishes.

Example:
  pgedge starfleet byoc database create --name mydb --cluster-id <cluster_id>
  pgedge starfleet byoc database create --name mydb --cluster-id <cluster_id> \
    --pg-version 16 --wait`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Before the client, so a bad name costs no round trip and
			// no token exchange.
			if err := validateByocDatabaseName(name); err != nil {
				return err
			}
			// An explicitly empty --pg-version is refused rather than
			// treated as absent. byoc publishes no enum to check a
			// version against — its spec declares pg_version as a bare
			// string and the supported set lives in the config-version
			// catalog — so this is the ONLY thing that can be checked
			// locally, and it is worth checking: pg_version is absent
			// from `database update`, so the version is fixed at create
			// as it is on managed, and `--pg-version "$PGV"` with the
			// variable unset would otherwise take the API's default
			// silently (#243).
			if cmd.Flags().Changed("pg-version") && pgVersion == "" {
				return newExitError(
					"--pg-version given an empty value: name a version "+
						"(e.g. 16), or omit the flag to use the API "+
						"default", ExitUsage)
			}
			// Recorded, not just performed. Without this the dry-run
			// report takes its empty-ledger branch and prints "none —
			// this command has no client-side checks", which this very
			// change made false. That line exists precisely so an
			// operator can tell an unchecked verb from a checked one
			// before trusting a clean dry run, so leaving it wrong is
			// worse than having no check at all.
			rt.DryRun.Pass("database name %q accepted", name)

			// The contract declares cluster_id as a bare string, so
			// anything typed here reached the server unexamined: an ID
			// prefix came back as "cluster not found or not available",
			// which reads like the cluster is busy rather than like the
			// ID was short (#257). Checked before the client for the
			// same reason as the name above.
			cluster, err := parseUUIDArg(clusterID, "cluster ID")
			if err != nil {
				return err
			}
			clusterID = cluster.String()
			// See the note on `ingress create`: the ledger is what a
			// dry run reports, and this verb has two checks now.
			rt.DryRun.Pass("cluster ID %s well-formed", clusterID)

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			body := api.CreateDatabaseJSONRequestBody{
				Name:      name,
				ClusterId: &clusterID,
			}
			if cmd.Flags().Changed("pg-version") {
				body.PgVersion = &pgVersion
			}

			resp, err := client.CreateDatabaseWithResponse(
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
				// Accepted, but no body to read an id from — nothing to
				// track.
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
			return trackMutation(rt, client, d.Id, "")
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "Database name")
	f.StringVar(&clusterID, "cluster-id", "",
		"Cluster to deploy the database on (full UUID)")
	f.StringVar(&pgVersion, "pg-version", "",
		"Postgres version (e.g. 16)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("cluster-id")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// --- update ---

func newDatabaseUpdateCmd(rt *module.Runtime) *cobra.Command {
	var (
		displayName string
		options     []string
	)
	cmd := &cobra.Command{
		Use:   "update <database_id>",
		Short: "Update a database",
		Long: `update changes a database's display name or options.

Use it to rename a database or adjust its option list after
creation. The argument takes a full UUID.

Example:
  pgedge starfleet byoc database update f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --display-name "My Database"
  pgedge starfleet byoc database update f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --options key1,key2`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before the client: a name the caller typed too long
			// must answer 2, not exit 5 for credentials it never
			// needed. The same limit as create, which used to accept
			// 40 characters that this verb would then refuse (#268).
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

			body := api.UpdateDatabaseJSONRequestBody{}
			if cmd.Flags().Changed("display-name") {
				body.DisplayName.Set(displayName)
			}
			if cmd.Flags().Changed("options") {
				body.Options = &options
			}

			resp, err := client.UpdateDatabaseWithResponse(
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
	cli.MarkMutating(cmd)

	return cmd
}

// --- delete ---

func newDatabaseDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <database_id>",
		Short: "Delete a database",
		Long: `delete tears down a database.

Deletion is destructive, so it prompts for confirmation unless
--force is given. The argument takes a full UUID. Pass --wait to
block until teardown finishes.

Example:
  pgedge starfleet byoc database delete f6a7b8c9-d0e1-2345-fabc-456789012345
  pgedge starfleet byoc database delete f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --force --wait`,
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
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			var priorTaskID string
			if tracking() {
				priorTaskID, err = newestSubjectTaskID(
					context.Background(), client, id.String())
				if err != nil {
					return err
				}
			}

			resp, err := client.DeleteDatabase(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("delete database: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "delete database"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr, "Database %s deleted.\n", id)
			return trackMutation(rt, client, id.String(), priorTaskID)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// --- row adapter ---

type databaseRow struct {
	id, name, status, pgVersion, clusterID, created string
	// showPGVersion selects which of the two header sets this row is
	// rendered against, and it is a field rather than two row types so
	// that the cell ORDER has exactly one definition. Two adapters
	// would have to agree about where CLUSTER sits, and #219 exists
	// because one declaration served two readers with different data.
	showPGVersion bool
}

func (r databaseRow) Columns() []string {
	cells := []string{
		r.id,
		r.name,
		output.ColorStatus(r.status),
	}
	if r.showPGVersion {
		cells = append(cells, r.pgVersion)
	}
	return append(cells, r.clusterID, r.created)
}

// databaseRowFrom adapts an api.Database into a table row.
func databaseRowFrom(d api.Database) databaseRow {
	return databaseRow{
		id:            d.Id,
		name:          d.Name,
		status:        d.Status,
		pgVersion:     output.DerefString(d.PgVersion),
		clusterID:     d.ClusterId,
		created:       output.FormatTime(d.CreatedAt),
		showPGVersion: true,
	}
}

// databaseListRowFrom is databaseRowFrom for the LIST reader, which
// renders one column fewer (#219). pgVersion is still carried, unread,
// rather than dropped: the field costs nothing, and the day the list
// endpoint starts sending it the fix is one bool.
func databaseListRowFrom(d api.Database) databaseRow {
	r := databaseRowFrom(d)
	r.showPGVersion = false
	return r
}
