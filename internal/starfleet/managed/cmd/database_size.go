package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// --- resize ---

func newDatabaseResizeCmd(rt *module.Runtime) *cobra.Command {
	var size string
	var force bool
	cmd := &cobra.Command{
		Use:   "resize <database_id>",
		Short: "Resize a managed database",
		Long: `resize changes the size of a managed database.

Size is deliberately not settable through 'database update': a
resize moves the database onto different infrastructure and is
initiated asynchronously, so it has its own verb. Without --wait the
command returns as soon as the resize is accepted, not when it
completes.

Resizing is one-way: a database can grow but not shrink, because its
storage cannot. Resizing to the same size is refused too. Run
'pgedge starfleet managed size list' for the sizes on offer -- --size is
checked against that list, so a size it does not carry is refused with
exit 2 rather than sent.

Because it is one-way and moves the database onto different
infrastructure, it prompts for confirmation; use --force to skip it.
On a per-vCPU plan a resize also raises the monthly bill, and a
mistyped --size cannot be undone.

The argument takes a full UUID.

Example:
  pgedge starfleet managed database resize e5f6a7b8-c9d0-1234-efab-567890123456 \
    --size large
  pgedge starfleet managed database resize e5f6a7b8-c9d0-1234-efab-567890123456 \
    --size large --force --wait`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			size, err := cli.RequiredStringFlag(cmd.Flags(), "size",
				"name a size from `pgedge starfleet managed size list`")
			if err != nil {
				return err
			}

			// Before the prompt: a parse is free, so a malformed ID is
			// refused rather than confirmed and then refused.
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}

			// It prompts because a resize moves the database onto
			// different infrastructure, cannot be reversed (a database
			// can grow but not shrink) and on a per-vCPU plan raises the
			// bill for good; `database delete` can at least be recovered
			// from a backup. The endpoint has no force parameter, so
			// --force only skips the prompt.
			if err := cli.Confirm(rt, fmt.Sprintf(
				"Resize database %s to %q? A resize is one-way — a "+
					"database can grow but not shrink — and it moves "+
					"the database onto different infrastructure.",
				id, size), force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			ctx := context.Background()

			// After the prompt, unlike the free blank check above: the
			// catalog check costs a read, which would otherwise be spent
			// on every declined resize, and a scripted resize that
			// forgot --force would get a catalog error instead of the
			// --force refusal it needs. A dry run still reaches it,
			// since cli.Confirm returns early under --dry-run.
			if err := validateSize(ctx, rt, client, size); err != nil {
				return err
			}

			body := api.ResizeManagedDatabaseJSONRequestBody{Size: size}

			// Captured before the mutation so waiting can tell the
			// resize task apart from the database's earlier ones.
			base := captureTaskBaseline(client, id.String())

			// Untyped call: resize has no typed 2xx case. See
			// checkEmptyBodyResponse.
			resp, err := client.ResizeManagedDatabase(
				context.Background(), id, body)
			if err != nil {
				return fmt.Errorf("resize database: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "resize database"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr,
				"Resize to %q requested for database %s.\n",
				size, id)
			return trackMutation(rt, client, id.String(), base)
		},
	}
	addWaitFlags(cmd)
	cmd.Flags().StringVar(&size, "size", "",
		"Target managed size name (e.g. small, large); checked "+
			"against 'size list'")
	_ = cmd.MarkFlagRequired("size")
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cli.MarkMutating(cmd)

	return cmd
}
