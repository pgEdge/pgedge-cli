package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

// nodeNamePattern is the spec's DatabaseNodeSpec name pattern. An
// instance ID is `<database>-<node>-<suffix>`, which never matches it,
// so an ID is passed through without a read.
var nodeNamePattern = regexp.MustCompile(`^n\d+$`)

// resolveInstanceArg turns a node name into that node's one instance
// ID, read from `database get`. Any other value is returned as typed.
func resolveInstanceArg(
	rt *module.Runtime, cmd *cobra.Command, dbID, arg string,
) (string, error) {
	if !nodeNamePattern.MatchString(arg) {
		return arg, nil
	}
	client, err := clientFromCmd(rt, cmd)
	if err != nil {
		return "", err
	}
	resp, err := client.GetDatabaseWithResponse(
		context.Background(), dbID, &api.GetDatabaseParams{})
	if err != nil {
		return "", networkError("get database", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return "", err
	}
	var matches []api.Instance
	if resp.JSON200 != nil && resp.JSON200.Instances != nil {
		for _, inst := range *resp.JSON200.Instances {
			if inst.NodeName == arg {
				matches = append(matches, inst)
			}
		}
	}
	switch len(matches) {
	case 0:
		return "", &ExitError{
			msg: fmt.Sprintf("database %s has no instance on node %s",
				dbID, arg),
			code: ExitNotFound,
		}
	case 1:
		id := matches[0].Id
		fmt.Fprintf(rt.Stderr, "Node %s is instance %s.\n",
			output.Sanitize(arg), output.Sanitize(id))
		return id, nil
	}
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, fmt.Sprintf("%s (host %s)",
			output.Sanitize(m.Id), output.Sanitize(m.HostId)))
	}
	return "", &ExitError{
		msg: fmt.Sprintf("node %s in database %s has %d instances; "+
			"pass one by ID: %s", arg, dbID, len(matches),
			strings.Join(ids, ", ")),
		code: ExitUsage,
	}
}
