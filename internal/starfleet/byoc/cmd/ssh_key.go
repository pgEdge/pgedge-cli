package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

// sshKeyColumns are the table headers shared by ssh-key list and get.
var sshKeyColumns = []string{"ID", "NAME", "CREATED"}

// NewSSHKeyCmd builds the `pgedge starfleet byoc ssh-key` command group. The
// plural "ssh-keys" is kept as a plural alias (unlisted in help) so
// existing scripts keep working.
func NewSSHKeyCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ssh-key",
		Aliases: []string{"ssh-keys"},
		Short:   "Manage pgEdge BYOC SSH keys",
		Long: `ssh-key manages the SSH public keys registered on your pgEdge
BYOC account.

Use these commands to list, inspect, create, and delete SSH keys.
Registered keys can be attached to clusters for node access.

Example:
  pgedge starfleet byoc ssh-key list
  pgedge starfleet byoc ssh-key create --name laptop \
    --public-key "$(cat ~/.ssh/id_ed25519.pub)"`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newSSHKeyListCmd(rt),
		newSSHKeyGetCmd(rt),
		newSSHKeyCreateCmd(rt),
		newSSHKeyDeleteCmd(rt),
	)
	return cmd
}

// --- list ---

func newSSHKeyListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List SSH keys",
		Long: `list shows the SSH keys registered on the active account.

Use it to find an SSH key's ID before running get or delete.

Example:
  pgedge starfleet byoc ssh-key list
  pgedge starfleet byoc ssh-key list -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListSshKeysWithResponse(
				context.Background())
			if err != nil {
				return fmt.Errorf("list ssh keys: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			keys := resp.JSON200
			if keys == nil || len(*keys) == 0 {
				fmt.Fprintln(rt.Stderr, "No SSH keys found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*keys))
			for _, k := range *keys {
				rows = append(rows, sshKeyRowFrom(k))
			}
			return rt.Output.Print(rows, sshKeyColumns)
		},
	}
}

// --- get ---

func newSSHKeyGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <ssh_key_id>",
		Short: "Show SSH key details",
		Long: `get shows the details of a single SSH key.

Use it to confirm an SSH key's name and creation time. The
argument is the key's UUID.

Example:
  pgedge starfleet byoc ssh-key get b0c1d2e3-f4a5-6789-bcde-890123456789
  pgedge starfleet byoc ssh-key get b0c1d2e3-f4a5-6789-bcde-890123456789 -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "SSH key ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetSshKeyWithResponse(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("get ssh key: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			k := resp.JSON200
			if k == nil {
				fmt.Fprintln(rt.Stderr, "No SSH key data returned.")
				return nil
			}
			rows := []output.Row{sshKeyRowFrom(*k)}
			return rt.Output.Print(rows, sshKeyColumns)
		},
	}
}

// --- create ---

func newSSHKeyCreateCmd(rt *module.Runtime) *cobra.Command {
	var (
		name      string
		publicKey string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an SSH key",
		Long: `create registers a new SSH public key on your account.

Use it to add a key that can later be attached to clusters for
node access. Both --name and --public-key are required.

Example:
  pgedge starfleet byoc ssh-key create --name laptop \
    --public-key "$(cat ~/.ssh/id_ed25519.pub)"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Before the client, because nothing is sent. A key that
			// is not a key was stored and reported as created; the
			// mistake then surfaced when someone could not reach a
			// node, which is the furthest point from the cause, and
			// the value is copied into node configuration so it
			// outlives the moment it could have been corrected (#290).
			if err := validatePublicKey(publicKey); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			body := api.CreateSshKeyJSONRequestBody{
				Name:      name,
				PublicKey: publicKey,
			}

			resp, err := client.CreateSshKeyWithResponse(
				context.Background(), body)
			if err != nil {
				return fmt.Errorf("create ssh key: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			k := resp.JSON200
			if rt.Output.Structured() {
				return rt.Output.Print(k, nil)
			}
			if k == nil {
				fmt.Fprintln(rt.Stderr,
					"SSH key created (no details returned).")
				return nil
			}
			fmt.Fprintf(rt.Stderr, "SSH key %q created (id: %s).\n",
				k.Name, output.Sanitize(k.Id))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "SSH key name")
	f.StringVar(&publicKey, "public-key", "",
		"SSH public key in authorized_keys form "+
			"(e.g. \"ssh-ed25519 AAAA... comment\")")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("public-key")
	cli.MarkMutating(cmd)

	return cmd
}

// --- delete ---

func newSSHKeyDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <ssh_key_id>",
		Short: "Delete an SSH key",
		Long: `delete removes an SSH key from your account.

Deletion is destructive, so it prompts for confirmation unless
--force is given. The argument is the key's UUID.

Example:
  pgedge starfleet byoc ssh-key delete b0c1d2e3-f4a5-6789-bcde-890123456789
  pgedge starfleet byoc ssh-key delete b0c1d2e3-f4a5-6789-bcde-890123456789 \
    --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "SSH key ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Delete SSH key %s? This cannot be undone.", id)
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.DeleteSshKey(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("delete ssh key: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "delete ssh key"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr, "SSH key %s deleted.\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cli.MarkMutating(cmd)

	return cmd
}

// --- row adapter ---

type sshKeyRow struct {
	id, name, created string
}

func (r sshKeyRow) Columns() []string {
	return []string{r.id, r.name, r.created}
}

// sshKeyRowFrom adapts an api.SshKey into a table row.
func sshKeyRowFrom(k api.SshKey) sshKeyRow {
	return sshKeyRow{
		id:      k.Id,
		name:    k.Name,
		created: output.FormatTime(k.CreatedAt),
	}
}

// validatePublicKey refuses a --public-key that is not exactly one SSH
// public key in authorized_keys form.
//
// ParseAuthorizedKey is the parser sshd uses, so what it accepts a node
// will accept, and it tolerates the trailing comment a caller pasting
// out of a .pub file has. Four of its tolerances are wrong for a field
// that stores ONE key, and each is refused here:
//
//   - MORE THAN ONE LINE. A second line parses as nothing at all: the
//     first key is registered, the rest is discarded, and the command
//     reports success. `$(cat a.pub b.pub)` is how that arrives.
//     Rejecting on the line count also disposes of a bare CR, which
//     otherwise truncates the line and hides whatever follows it.
//   - A SECOND KEY ON THE SAME LINE. ParseAuthorizedKey splits at the
//     first whitespace after the base64 field and hands everything
//     beyond it back as the COMMENT, so `key1 key2` parses cleanly
//     with an empty rest. The discriminator is that a real comment
//     ("me@example") does not itself parse as a key.
//   - A TYPE TOKEN THAT DISAGREES WITH THE BLOB. x/crypto decodes the
//     base64 and never compares the two -- its own source says the
//     duplicated type "is ignored here" (keys.go). So
//     `ssh-rsa <ed25519 blob>` parses, and OpenSSH does not:
//     `ssh-keygen -l` on that line exits 255 with "is not a public key
//     file". Storing it is #290's failure mode exactly -- discovered
//     when someone cannot reach a node, long after the cause.
//   - authorized_keys OPTIONS (`no-pty,command="..." ssh-ed25519 ...`).
//     They are a server-side access rule, not part of a key.
//
// It is NOT aligned with the Starfleet UI's validator for the same field
// (product-ui, src/http/schemas/createSshKeySchema.ts, a hand-written
// structural walk because the browser has no SSH parser). Measured
// over 44 inputs, this side is stricter on every difference but ONE:
// the CLI accepts an SSH CERTIFICATE, which x/crypto parses natively
// and the UI's length-prefixed walk cannot traverse. The UI accepts a
// two-key value in any spelling, a trailing comment line, an ssh-dss
// key, and embedded CR/VT/FF junk. ssh-dss is refused by x/crypto
// itself, not by anything here.
func validatePublicKey(v string) error {
	reject := func(why string) error {
		return &cli.UsageError{Msg: fmt.Sprintf(
			"--public-key is not an SSH public key: %s (expected one "+
				"key in authorized_keys form, e.g. "+
				"\"ssh-ed25519 AAAA... comment\")", why)}
	}
	trimmed := strings.TrimSpace(v)
	if strings.ContainsAny(trimmed, "\n\r") {
		return reject("it spans more than one line")
	}
	key, comment, opts, _, err := ssh.ParseAuthorizedKey([]byte(trimmed))
	if err != nil {
		return reject(err.Error())
	}
	if len(opts) > 0 {
		return reject("it carries authorized_keys options (" +
			strings.Join(opts, ", ") + ")")
	}
	if comment != "" {
		if _, _, _, _, err := ssh.ParseAuthorizedKey(
			[]byte(comment)); err == nil {
			return reject("it carries more than one key")
		}
	}
	// key.Type() is the algorithm read out of the DECODED blob, so
	// comparing it with the token the caller typed is what x/crypto
	// declines to do. It matches for every legitimate key, SSH
	// certificates included.
	if token := strings.Fields(trimmed)[0]; token != key.Type() {
		return reject(fmt.Sprintf(
			"it is labeled %q but the key material is %q, which "+
				"OpenSSH will not parse", token, key.Type()))
	}
	return nil
}
