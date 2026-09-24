package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/apicall"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
)

// newAPICmd builds `pgedge starfleet api`, the raw call to any path
// under the Starfleet API base over the module's own connection (#434).
func newAPICmd(rt *module.Runtime, f *conn.Flags) *cobra.Command {
	var (
		data    string
		query   []string
		headers []string
		include bool
	)
	cmd := &cobra.Command{
		Use:   "api <method> <path>",
		Short: "Call any Starfleet API path over the module's connection",
		Long: `api sends one request to a path under the Starfleet API base and
prints the response. It is the escape hatch for an endpoint no verb
covers yet: the same credential, base URL, --timeout, exit codes and
output formats as every other starfleet command, with none of their
validation, paging or field knowledge.

The method is GET, POST, PUT, PATCH or DELETE. The path is relative to
the API base and starts with /, as the API reference spells it, such as
/byoc/v1/clusters or /managed/v1/databases/<id>; an absolute URL is
refused, so the credential only ever reaches the API it was issued
for. --query adds key=value parameters, --data sends a JSON body
(a literal, @file, or - for stdin) and --header adds a header other
than Authorization, which the module sets.

A non-2xx status maps to the same exit code the module's verbs give
it: 5 for an authentication or entitlement refusal, 4 for a 404 with a
resource in it, 3 for a timeout, 1 otherwise. A 2xx with a JSON body
prints it indented in text and byte for byte under -o json; -o yaml
re-encodes it, keeping every digit. A 2xx with no body prints nothing
on stdout and a sentence on stderr. Any
method but GET is a write, so --dry-run reports the request it would
have sent instead of sending it, and -i/--include prints the status
and response headers on stderr.

Example:
  pgedge starfleet api GET /byoc/v1/clusters
  pgedge starfleet api GET /managed/v1/databases --query limit=5 -o json
  pgedge starfleet api POST /managed/v1/databases --data @spec.json --dry-run`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := apicall.ReadBody(data, rt.Stdin)
			if err != nil {
				return conn.NewExitError(fmt.Sprintf("--data: %v", err),
					conn.ExitUsage)
			}
			c, err := conn.Resolve(rt, f.ClientID, f.ClientSecret,
				f.APIURL, f.Timeout)
			if err != nil {
				return err
			}
			d := apicall.Deps{
				Client:  c.HTTPClient,
				BaseURL: c.APIURL,
				Edit:    c.BearerEditor,
				Check:   conn.CheckResponse,
				UsageErr: func(m string) error {
					return conn.NewExitError(m, conn.ExitUsage)
				},
			}
			hreq, err := apicall.Build(cmd.Context(), d, apicall.Request{
				Method: args[0], Path: args[1], Query: query,
				Headers: headers, Body: body,
			})
			if err != nil {
				return err
			}
			return apicall.Do(rt, d, hreq, include)
		},
	}
	cmd.Flags().StringVarP(&data, "data", "d", "",
		"JSON request body: a literal, @file, or - for stdin")
	cmd.Flags().StringArrayVar(&query, "query", nil,
		"Query parameter as key=value (repeatable)")
	cmd.Flags().StringArrayVarP(&headers, "header", "H", nil,
		"Request header as Name: value (repeatable)")
	cmd.Flags().BoolVarP(&include, "include", "i", false,
		"Print the status and response headers on stderr")
	_ = cmd.RegisterFlagCompletionFunc("data", cobra.NoFileCompletions)
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, _ string) (
		[]string, cobra.ShellCompDirective,
	) {
		if len(args) == 0 {
			return apicall.Methods, cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cli.MarkMutating(cmd)
	return cmd
}
