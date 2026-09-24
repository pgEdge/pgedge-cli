package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/apicall"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
)

// newAPICmd builds `pgedge controlplane api`, the raw call to any path
// on the Control Plane over the module's own connection.
func newAPICmd(rt *module.Runtime) *cobra.Command {
	var (
		data    string
		query   []string
		headers []string
		include bool
	)
	cmd := &cobra.Command{
		Use:   "api <method> <path>",
		Short: "Call any Control Plane API path over the connection",
		Long: `api sends one request to a path on the Control Plane and prints
the response. It is the escape hatch for an endpoint no verb covers
yet: the same --base-url, mTLS settings, --timeout, failover between
base URLs, exit codes and output formats as every other controlplane
command, with none of their validation or field knowledge.

The method is GET, POST, PUT, PATCH or DELETE. The path starts with /
as the API reference spells it, such as /v1/databases; an absolute URL
is refused. --query adds key=value parameters, --data sends a JSON
body (a literal, @file, or - for stdin) and --header adds a header
other than Authorization.

A non-2xx status maps to the exit code the module's verbs give it. A
2xx with a JSON body prints it indented in text and as is under
-o json or -o yaml; a 2xx with no body prints nothing on stdout and a
sentence on stderr. Any method but GET is a write, and so are the two
GET paths the Control Plane declares state-changing, so --dry-run
reports the request it would have sent instead of sending it.
-i/--include prints the status and response headers on stderr.

Example:
  pgedge controlplane api GET /v1/databases
  pgedge controlplane api GET /v1/hosts -o json
  pgedge controlplane api POST /v1/databases --data @spec.json --dry-run`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := apicall.ReadBody(data, rt.Stdin)
			if err != nil {
				return &ExitError{msg: fmt.Sprintf("--data: %v", err), code: ExitUsage}
			}
			c, err := resolveConnection(rt, cmd)
			if err != nil {
				return err
			}
			baseURL, err := selectBaseURL(rt, c)
			if err != nil {
				return err
			}
			httpClient, err := httpClientFor(rt, c)
			if err != nil {
				return err
			}
			d := apicall.Deps{
				Client:  httpClient,
				BaseURL: baseURL,
				Check:   checkResponse,
				UsageErr: func(m string) error {
					return &ExitError{msg: m, code: ExitUsage}
				},
				Transport: func(err error) error {
					return networkError("call the API", err)
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
