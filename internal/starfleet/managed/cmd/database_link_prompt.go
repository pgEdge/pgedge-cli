package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/term"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
)

// stdinIsTerminal is a variable so tests can stand in for a person.
var stdinIsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// branchReady is the status of a branch that can be connected to, as
// captured from live branch lists.
const branchReady = "available"

// promptLinkTarget asks which database to link, then which of its
// branches when it has any, and names the choice. A nil branch ID means
// the database itself.
func promptLinkTarget(rt *module.Runtime, client *api.ClientWithResponses, in *bufio.Reader) (
	id, branchID uuid.UUID, name string, err error,
) {
	resp, err := client.ListManagedDatabasesWithResponse(
		context.Background(), &api.ListManagedDatabasesParams{})
	if err != nil {
		return uuid.Nil, uuid.Nil, "", fmt.Errorf("list databases: %w", err)
	}
	if err := checkResponse(resp.StatusCode(), string(resp.Body)); err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	if resp.JSON200 == nil || len(*resp.JSON200) == 0 {
		return uuid.Nil, uuid.Nil, "", newExitError("this account has no managed "+
			"databases; create one and link it with 'pgedge starfleet managed "+
			"database create --name <db-name> --link'", ExitGeneral)
	}
	dbs := *resp.JSON200

	fmt.Fprintln(rt.Stderr, "Databases:")
	for i, d := range dbs {
		fmt.Fprintf(rt.Stderr, "  %d) %s  %s  %s  %s\n", i+1,
			output.Sanitize(d.Name), output.Sanitize(d.Id),
			output.Sanitize(d.Region), output.Sanitize(d.Status))
	}
	n, err := choose(rt, in, len(dbs), false, func(int) string { return "" })
	if err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	db := dbs[n-1]
	id, err = parseUUIDArg(db.Id, "database ID")
	if err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	// The branches are always listed: the database list omits
	// branch_count, which only a single database's GET carries.
	br, err := client.ListBranchesWithResponse(context.Background(), id, &api.ListBranchesParams{})
	if err != nil {
		return uuid.Nil, uuid.Nil, "", fmt.Errorf("list branches: %w", err)
	}
	if err := checkResponse(br.StatusCode(), string(br.Body)); err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	if br.JSON200 == nil || len(*br.JSON200) == 0 {
		return id, uuid.Nil, targetName(db.Name, id, uuid.Nil), nil
	}
	branches := *br.JSON200

	fmt.Fprintf(rt.Stderr, "Branches of %s:\n", output.Sanitize(db.Name))
	fmt.Fprintln(rt.Stderr, "  Enter) the database itself")
	for i, b := range branches {
		if b.Status == branchReady {
			fmt.Fprintf(rt.Stderr, "  %d) %s  %s  %s\n", i+1,
				output.Sanitize(b.Name), output.Sanitize(b.Id), output.Sanitize(b.Status))
		} else {
			fmt.Fprintf(rt.Stderr, "  %d) %s  %s  %s  (not ready)\n", i+1,
				output.Sanitize(b.Name), output.Sanitize(b.Id), output.Sanitize(b.Status))
		}
	}
	n, err = choose(rt, in, len(branches), true, func(i int) string {
		if s := branches[i-1].Status; s != branchReady {
			return s
		}
		return ""
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	if n == 0 {
		return id, uuid.Nil, targetName(db.Name, id, uuid.Nil), nil
	}
	if branchID, err = parseUUIDArg(branches[n-1].Id, "branch ID"); err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	return id, branchID, targetName(db.Name, id, branchID), nil
}

// choose reads a number from 1 to limit, asking again after an answer
// that is not one, or whose branch notReady names a status other than
// available. optional is the branch question, where Enter answers 0.
func choose(rt *module.Runtime, in *bufio.Reader, limit int, optional bool,
	notReady func(int) string,
) (int, error) {
	for {
		if optional {
			fmt.Fprintf(rt.Stderr, "Branch [Enter, 1-%d]: ", limit)
		} else {
			fmt.Fprintf(rt.Stderr, "Database [1-%d]: ", limit)
		}
		line, err := in.ReadString('\n')
		answer := strings.TrimSpace(line)
		if err != nil && (err != io.EOF || answer == "") {
			return 0, &cli.UsageError{Msg: "aborted: nothing chosen"}
		}
		if answer == "" && optional {
			return 0, nil
		}
		n, convErr := strconv.Atoi(answer)
		if convErr != nil || n < 1 || n > limit {
			fmt.Fprintf(rt.Stderr, "Enter a number from 1 to %d.\n", limit)
			continue
		}
		if s := notReady(n); s != "" {
			fmt.Fprintf(rt.Stderr, "That branch is %s, not available; choose another.\n",
				output.Sanitize(s))
			continue
		}
		return n, nil
	}
}

// askWriteEnv asks whether to write .env now, until the answer is yes
// or no. Enter is yes; ended input is no.
func askWriteEnv(rt *module.Runtime, in *bufio.Reader) bool {
	for {
		fmt.Fprint(rt.Stderr, "Write DATABASE_URL to .env now? [Y/n]: ")
		line, err := in.ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		switch {
		case answer == "y" || answer == "yes" || (answer == "" && err == nil):
			return true
		case answer == "n" || answer == "no" || err != nil:
			return false
		}
		fmt.Fprintln(rt.Stderr, "Answer y or n.")
	}
}
