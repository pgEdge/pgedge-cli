package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/inspect"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// InspectDeps is the seam a test uses to run an analysis without a
// Postgres. nil Open connects with the real driver.
type InspectDeps struct {
	Open func(ctx context.Context, dbURL string) (*sql.DB, error)
}

// NewInspectCmd builds `pgedge inspect`, which runs one read-only
// diagnostic against any Postgres reachable by connection string. The
// module trees carry the same analyses for a database the API knows,
// resolving the string for you; this is the form for a Control Plane
// database or any other Postgres.
func NewInspectCmd(rt *module.Runtime, deps *InspectDeps) *cobra.Command {
	if deps == nil {
		deps = &InspectDeps{}
	}
	var dbURL string
	cmd := &cobra.Command{
		Use:   "inspect <analysis>",
		Short: "Run a read-only diagnostic against a Postgres database",
		Long: `inspect runs one diagnostic query against the database --db-url
names and prints the rows. Every analysis reads catalog and statistics
views only; nothing is written.

The analyses, and what each reads:

` + analysisList() + `
calls and outliers need the pg_stat_statements extension and are
refused with exit 1 naming it when the database does not have it.
long-running-queries, locks, calls, outliers and replication-lag read
other sessions' rows, which Postgres nulls for a role without
pg_read_all_stats, so connect as a role that has it or the first two
answer empty and replication-lag returns its rows blank. Text
output is a table with the analysis's own columns; -o json prints one
object per row keyed by column. No rows is exit 0 with a sentence on
stderr. A database that refuses the connection is exit 1; one that
accepts it and never answers is exit 3 after 30 seconds.

For a database the API knows, the module verbs resolve the connection
for you: 'pgedge starfleet managed database inspect' and
'pgedge starfleet byoc database inspect'. This top-level form takes a
connection string, so it works for a Control Plane database or any
other Postgres the machine can reach.

Example:
  pgedge inspect table-sizes --db-url "postgresql://app:secret@db.example:5432/app?sslmode=require"
  pgedge inspect unused-indexes --db-url "$DATABASE_URL" -o json`,
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) (
			[]string, cobra.ShellCompDirective,
		) {
			return inspect.Names(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			a, ok := inspect.Lookup(args[0])
			if !ok {
				return &UsageError{Msg: fmt.Sprintf(
					"unknown analysis %q: one of %s", args[0],
					strings.Join(inspect.Names(), ", "))}
			}
			if dbURL == "" {
				return &UsageError{Msg: "--db-url is required: a libpq " +
					"connection string for the database to inspect"}
			}
			return RunInspect(cmd.Context(), rt, deps, a, dbURL)
		},
	}
	cmd.Flags().StringVar(&dbURL, "db-url", "",
		"libpq connection string of the database to inspect (required)")
	return cmd
}

// RunInspect opens dbURL, runs a, and prints the rows through rt's
// renderer. It is shared with the module verbs, which resolve dbURL
// from the API before calling it.
func RunInspect(
	ctx context.Context, rt *module.Runtime, deps *InspectDeps,
	a inspect.Analysis, dbURL string,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	open := inspect.Open
	if deps != nil && deps.Open != nil {
		open = deps.Open
	}
	db, err := open(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect to the database: %w", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := inspect.Run(ctx, db, a)
	if errors.Is(err, inspect.ErrMissingExtension) {
		return fmt.Errorf("%s needs the %s extension, which this "+
			"database does not have", a.Name, a.Extension)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", a.Name, err)
	}
	if len(rows) == 0 {
		fmt.Fprintf(rt.Stderr, "%s: no rows.\n", output.Sanitize(a.Name))
		if rt.Output.Structured() {
			return rt.Output.Print([]inspect.Row{}, nil)
		}
		return nil
	}
	if rt.Output.Structured() {
		return rt.Output.Print(rows, nil)
	}
	out := make([]output.Row, len(rows))
	for i := range rows {
		out[i] = rows[i]
	}
	return rt.Output.Print(out, inspect.Headers(a))
}

// analysisList renders the vocabulary for Long, one per line.
func analysisList() string {
	var b strings.Builder
	for _, a := range inspect.Analyses {
		fmt.Fprintf(&b, "  %-22s %s\n", a.Name, a.Short)
	}
	return b.String()
}
