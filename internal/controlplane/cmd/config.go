package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

// newConfigCmd builds `pgedge controlplane config`.
func newConfigCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage the Control Plane connection",
		Long: `config stores and shows the connection controlplane uses to reach a
control-plane for the active profile.

Example:
  pgedge controlplane config set --base-url http://localhost:3000
  pgedge controlplane config view`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(newConfigSetCmd(rt), newConfigViewCmd(rt))
	return cmd
}

func newConfigSetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "set",
		Short: "Save Control Plane connection settings",
		Long: `set writes the given connection settings to the active
profile in ~/.pgedge/cli/config.yaml. Only flags you pass are written;
omitted fields keep their current value.

Example:
  pgedge controlplane config set --base-url https://cp-1:3000 \
    --ca-cert ~/.pgedge/certs/ca.crt \
    --client-cert ~/.pgedge/certs/client.crt \
    --client-key ~/.pgedge/certs/client.key`,
		Annotations: map[string]string{
			cli.AnnotationProfileExempt: "creates the named profile, " +
				"so a new name is the point rather than a typo",
		},
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := rt.Config.ControlplaneProfile(rt.Profile)
			np := &config.ControlplaneProfile{
				BaseURL:            p.BaseURL,
				BaseURLs:           p.BaseURLs,
				CACert:             p.CACert,
				ClientCert:         p.ClientCert,
				ClientKey:          p.ClientKey,
				InsecureSkipVerify: p.InsecureSkipVerify,
				Timeout:            p.Timeout,
			}
			if cmd.Flags().Changed("base-url") {
				urls, _ := cmd.Flags().GetStringArray("base-url")
				np.BaseURLs = urls
				np.BaseURL = "" // one source of truth after a set
			}
			for _, c := range []struct {
				flag string
				dst  *string
			}{
				{"ca-cert", &np.CACert},
				{"client-cert", &np.ClientCert},
				{"client-key", &np.ClientKey},
			} {
				v, set, err := cli.OptionalStringFlag(cmd.Flags(), c.flag,
					"name a readable file, or omit the flag to keep "+
						"the current value")
				if err != nil {
					return err
				}
				if !set {
					continue
				}
				if err := checkReadableFile(c.flag, v); err != nil {
					return err
				}
				*c.dst = v
			}
			if cmd.Flags().Changed("insecure") {
				np.InsecureSkipVerify, _ =
					cmd.Flags().GetBool("insecure")
			}
			if cmd.Flags().Changed("timeout") {
				if d, err := cmd.Flags().GetDuration("timeout"); err == nil {
					np.Timeout = d.String()
				}
			}
			rt.Config.SetControlplaneProfile(rt.Profile, np)
			if err := rt.Config.Save(); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Fprintf(rt.Stderr,
				"Saved controlplane connection for profile %q.\n", rt.Profile)
			return nil
		},
	}
}

type connRow struct{ key, value string }

func (r connRow) Columns() []string { return []string{r.key, r.value} }

func newConfigViewCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "view",
		Short: "Show the resolved Control Plane connection",
		Long: `view prints the connection controlplane would use, with flags folded
in over the stored profile.

Example:
  pgedge controlplane config view
  pgedge controlplane config view -o yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Not propagated: view is where an operator looks to
			// find out what their profile says, so refusing to print
			// it because one field will not parse answers the
			// question with silence. The bad value is shown as a row
			// instead, with the default that is standing in for it.
			c, cerr := resolveConnection(rt, cmd)
			problem := ""
			if cerr != nil {
				problem = cerr.Error()
			}
			if rt.Output.Structured() {
				out := map[string]any{
					"base_urls":            c.baseURLs,
					"ca_cert":              c.caCert,
					"client_cert":          c.clientCert,
					"client_key":           c.clientKey,
					"insecure_skip_verify": c.insecure,
					"timeout":              c.timeout.String(),
				}
				if problem != "" {
					out["problem"] = problem
				}
				return rt.Output.Print(out, nil)
			}
			rows := []output.Row{
				connRow{"base-urls", strings.Join(c.baseURLs, ", ")},
				connRow{"ca-cert", c.caCert},
				connRow{"client-cert", c.clientCert},
				connRow{"client-key", c.clientKey},
				connRow{"insecure", fmt.Sprintf("%t", c.insecure)},
				connRow{"timeout", c.timeout.String()},
			}
			if problem != "" {
				rows = append(rows, connRow{"problem", problem})
			}
			return rt.Output.Print(rows, []string{"SETTING", "VALUE"})
		},
	}
}

// checkReadableFile refuses a path that names nothing readable.
//
// The check belongs where the value is ACCEPTED, not where it is first
// used: httpClientFor already reports `read ca-cert: ...` at exit 2,
// but that arrives on a later command, and an operator standing up a
// self-hosted Control Plane is typing these paths by hand for the
// first time, the same check --config gets.
//
// It opens the file rather than stat-ing it, because a path that
// exists and cannot be read fails at connection time just as surely as
// one that does not exist.
func checkReadableFile(flag, path string) error {
	f, err := os.Open(path) //nolint:gosec // G304: operator-supplied cert path, which is the point
	if err != nil {
		return &cli.UsageError{Msg: fmt.Sprintf(
			"--%s %s: %v", flag, path, err)}
	}
	return f.Close()
}
