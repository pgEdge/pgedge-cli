package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// --- restore ---

func newDatabaseRestoreCmd(rt *module.Runtime) *cobra.Command {
	var (
		provider     string
		nodeName     string
		repositories []string
		targetNodes  []string

		// pgBackRest restore-command options, all optional.
		delta           bool
		pgBackRestForce bool
		set             string
		target          string
		targetExclusive bool
		restoreType     string

		force bool
	)
	cmd := &cobra.Command{
		Use:   "restore <database_id>",
		Short: "Restore a database from a backup",
		Long: `restore rebuilds a BYOC database from a pgBackRest backup.

This is the counterpart to 'backup create'. The restore runs through
the Control Plane against the backup repository you name, so find the
repository first with 'backup-repository list --database-id <id>'.

Restoring overwrites the database's current contents, so it prompts
for confirmation unless --force is given. The database must be in a
modifiable state; the API refuses the restore otherwise.

--node-name and at least one --repository are required. Repeat
--repository to pass more than one; --target-nodes takes a
comma-separated list, or repeats, like every other --target-nodes
in this tree.

The pgBackRest restore command has its own force option. It is
exposed here as --pgbackrest-force to keep it distinct from --force,
which only skips the confirmation prompt.

The argument takes a full UUID.

Example:
  pgedge starfleet byoc database restore f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --node-name n1 --repository 9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d
  pgedge starfleet byoc database restore f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --node-name n1 --repository 9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d \
    --type time --target "2024-06-19T20:00:00Z" --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {

			body := api.RestoreDatabaseJSONRequestBody{
				Provider: provider,
				RestoreConfig: api.PgBackrestRestoreConfig{
					NodeName:     nodeName,
					Repositories: repositories,
				},
			}
			if len(targetNodes) > 0 {
				trimmed := trimSpaces(targetNodes)
				body.TargetNodes = &trimmed
			}
			// restore_command is all-optional: send it only when at
			// least one of its options was actually set, so an
			// untouched restore does not ship an empty object.
			if rc := restoreCommandFrom(cmd, delta, pgBackRestForce, set,
				target, targetExclusive, restoreType); rc != nil {
				body.RestoreCommand = rc
			}

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

			// --repository is checked and sent canonically, like every
			// other input naming a pgEdge resource.
			//
			// The earlier argument for leaving it alone -- that
			// PgBackrestRestoreConfig declares `repositories` as a
			// plain string array with no format -- is the argument
			// this repo explicitly REJECTS for cloud_account_id twenty
			// lines into buildClusterCreateBody: byoc.yaml types that
			// create field as a bare string too, while the path
			// parameter that reads the same resource types it as a
			// UUID, so the UUID is the spec's own shape for the field.
			// The same holds here -- `backup-repository get
			// <repository_id>` parses this identifier as a UUID, and
			// BackupRepository carries an `id` and no name field, so
			// the API publishes no non-UUID spelling for a repository
			// and a check cannot refuse anything it would accept.
			//
			// It is also the only UUID-carrying input in the tree whose
			// name does not end `-id`, so the suffix-derived sweep
			// population cannot see it and this is the only thing
			// standing behind it.
			canonicalRepos := make([]string, 0, len(repositories))
			for _, raw := range repositories {
				repoID, err := parseUUIDArg(raw, "repository ID")
				if err != nil {
					return err
				}
				canonicalRepos = append(canonicalRepos, repoID.String())
			}
			rt.DryRun.Pass("%d repository ID(s) are UUIDs",
				len(canonicalRepos))
			body.RestoreConfig.Repositories = canonicalRepos

			prompt := fmt.Sprintf(
				"Restore database %s from repository %s? This "+
					"overwrites its current contents.",
				id, joinStrings(repositories))
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

			// Untyped call: the handler answers RespondOK(ctx, nil)
			// against a spec declaring no content, so success is 200
			// carrying the JSON literal `null`. See
			// checkEmptyBodyResponse.
			resp, err := client.RestoreDatabase(
				context.Background(), id, body)
			if err != nil {
				return fmt.Errorf("restore database: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "restore database"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr,
				"Restore started for database %s.\n", output.Sanitize(args[0]))
			return trackMutation(rt, client, id.String(), priorTaskID)
		},
	}
	f := cmd.Flags()
	f.StringVar(&provider, "provider", "pgbackrest",
		"Backup provider to restore from")
	f.StringVar(&nodeName, "node-name", "",
		"Node whose backup is restored")
	f.StringArrayVar(&repositories, "repository", nil,
		"Backup repository UUID from backup-repository list (repeatable)")
	f.StringSliceVar(&targetNodes, "target-nodes", nil,
		"Nodes to restore onto (comma-separated or repeatable; "+
			"defaults to all nodes)")
	f.BoolVar(&delta, "delta", false,
		"pgBackRest delta restore: only replace files that differ")
	f.BoolVar(&pgBackRestForce, "pgbackrest-force", false,
		"pgBackRest force option (not the confirmation --force)")
	f.StringVar(&set, "set", "",
		"pgBackRest backup set to restore, for example 20240619-195803F")
	f.StringVar(&target, "target", "",
		"pgBackRest recovery target, paired with --type; "+
			"an RFC3339 time with offset for --type time")
	f.BoolVar(&targetExclusive, "target-exclusive", false,
		"Stop recovery before the target rather than including it")
	f.StringVar(&restoreType, "type", "",
		"pgBackRest recovery type, for example time or immediate")
	f.BoolVar(&force, "force", false, "Skip the confirmation prompt")
	_ = cmd.MarkFlagRequired("node-name")
	_ = cmd.MarkFlagRequired("repository")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// restoreCommandFrom builds the optional pgBackRest restore command,
// returning nil when the caller set none of its options. Booleans are
// read through Flags().Changed rather than by value: false is a
// meaningful setting for delta, force and target_exclusive, and
// sending it is different from omitting it.
func restoreCommandFrom(cmd *cobra.Command,
	delta, pgBackRestForce bool, set, target string,
	targetExclusive bool, restoreType string,
) *api.PgBackrestRestoreCommand {
	f := cmd.Flags()
	names := []string{"delta", "pgbackrest-force", "set", "target",
		"target-exclusive", "type"}
	touched := false
	for _, n := range names {
		if f.Changed(n) {
			touched = true
			break
		}
	}
	if !touched {
		return nil
	}

	rc := &api.PgBackrestRestoreCommand{}
	if f.Changed("delta") {
		rc.Delta = &delta
	}
	if f.Changed("pgbackrest-force") {
		rc.Force = &pgBackRestForce
	}
	if f.Changed("set") {
		rc.Set = &set
	}
	if f.Changed("target") {
		rc.Target = &target
	}
	if f.Changed("target-exclusive") {
		rc.TargetExclusive = &targetExclusive
	}
	if f.Changed("type") {
		rc.Type = &restoreType
	}
	return rc
}
