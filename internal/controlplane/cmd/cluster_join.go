package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

type joinOpts struct {
	token, tokenFile, from string
	serverURLs             []string
	wait                   bool
	waitTimeout            int
	waitInterval           int
}

func newClusterJoinCmd(rt *module.Runtime) *cobra.Command {
	o := &joinOpts{}
	cmd := &cobra.Command{
		Use:   "join",
		Short: "Join this host to a cluster",
		Long: `join adds THIS host (the one --base-url points at, which
must be a new, uninitialized control-plane) to an existing cluster.

Name the token's source with exactly one of three flags. --from takes
an existing member's URL: join reads the join token and server URLs
from that member and joins in one step, so the token appears in no
argument and no output. --token-file reads the token from a file,
dropping one trailing newline. --token takes the token as a value.

--server-url (repeatable) lists existing members for the new host to
contact. It is required with --token-file or --token; with --from it
defaults to the member's own server URLs.

The join returns once the host has joined. --wait then polls an
existing member's host list (--from, or the first --server-url) until
the joining host reports healthy.

Example:
  pgedge controlplane cluster join --base-url http://new-host:3000 \
    --from http://existing-1:3000 --wait
  pgedge controlplane cluster join --base-url http://new-host:3000 \
    --token-file join-token.txt --server-url http://existing-1:3000`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runClusterJoin(rt, cmd, o)
		},
	}
	cmd.Flags().StringVar(&o.from, "from", "",
		"Existing member URL to read the join token from")
	cmd.Flags().StringVar(&o.tokenFile, "token-file", "",
		"Read the join token from this file")
	cmd.Flags().StringVar(&o.token, "token", "",
		"Cluster join token")
	cmd.Flags().StringArrayVar(&o.serverURLs, "server-url", nil,
		"Existing cluster member to contact (repeatable; required "+
			"unless --from)")
	cmd.Flags().BoolVar(&o.wait, "wait", false,
		"Wait until the joining host reports healthy")
	cmd.Flags().IntVar(&o.waitTimeout, "wait-timeout", 600,
		"Max seconds to wait when --wait is set")
	cmd.Flags().IntVar(&o.waitInterval, "wait-interval", 3,
		"Polling interval in seconds when --wait is set")
	cli.MarkMutating(cmd)

	return cmd
}

func runClusterJoin(
	rt *module.Runtime, cmd *cobra.Command, o *joinOpts,
) error {
	sources := 0
	for _, name := range []string{"from", "token-file", "token"} {
		if cmd.Flags().Changed(name) {
			sources++
		}
	}
	switch {
	case sources > 1:
		return &ExitError{
			msg:  "--from, --token-file and --token are mutually exclusive",
			code: ExitUsage,
		}
	case sources == 0:
		return &ExitError{
			msg:  "one of --from, --token-file or --token is required",
			code: ExitUsage,
		}
	}
	if o.from == "" && len(o.serverURLs) == 0 {
		return &ExitError{
			msg: "--server-url is required with --token-file or " +
				"--token (at least one)",
			code: ExitUsage,
		}
	}

	token := o.token
	if cmd.Flags().Changed("token-file") {
		t, err := readTokenFile(o.tokenFile)
		if err != nil {
			return err
		}
		token = t
	}
	if o.from == "" && token == "" {
		return &ExitError{msg: "--token is empty", code: ExitUsage}
	}

	conn, err := resolveConnection(rt, cmd)
	if err != nil {
		return err
	}
	memberURL := o.from
	if memberURL == "" {
		memberURL = o.serverURLs[0]
	}
	var member *api.ClientWithResponses
	if o.from != "" || o.wait {
		member, err = newAPIClient(rt, conn, memberURL)
		if err != nil {
			return err
		}
	}

	serverURLs := o.serverURLs
	if o.from != "" {
		tok, err := fetchJoinToken(member, o.from)
		if err != nil {
			return err
		}
		token = tok.Token
		if len(serverURLs) == 0 {
			serverURLs = tok.ServerUrls
		}
		if len(serverURLs) == 0 {
			serverURLs = []string{o.from}
		}
	}

	// Read before the join: afterwards the new host is in the list and
	// nothing distinguishes it from the members it joined.
	var before map[string]bool
	if o.wait {
		hosts, err := listHostsCtx(context.Background(), member)
		if err != nil {
			return err
		}
		before = make(map[string]bool, len(hosts))
		for _, h := range hosts {
			before[h.Id] = true
		}
	}

	if err := sendJoin(rt, cmd, token, serverURLs); err != nil {
		return redactToken(err, token)
	}
	fmt.Fprintf(rt.Stderr, "Join request accepted.\n")
	if !o.wait {
		return nil
	}
	return waitForJoinedHost(rt, member, before,
		o.waitTimeout, o.waitInterval)
}

// readTokenFile refuses a control character rather than sanitizing
// it: rewriting a credential's bytes sends a different token.
func readTokenFile(path string) (string, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-named token file
	if err != nil {
		return "", &ExitError{
			msg:  fmt.Sprintf("read --token-file: %v", err),
			code: ExitUsage,
		}
	}
	tok := strings.TrimSuffix(string(raw), "\n")
	tok = strings.TrimSuffix(tok, "\r")
	if tok == "" {
		return "", &ExitError{
			msg:  fmt.Sprintf("--token-file %s is empty", path),
			code: ExitUsage,
		}
	}
	for _, r := range tok {
		if r < 0x20 || r == 0x7f {
			return "", &ExitError{
				msg: fmt.Sprintf("--token-file %s holds more than "+
					"one line or a control character; it must hold "+
					"the token alone", path),
				code: ExitUsage,
			}
		}
	}
	return tok, nil
}

func fetchJoinToken(
	member *api.ClientWithResponses, from string,
) (*api.ClusterJoinToken, error) {
	resp, err := member.GetJoinTokenWithResponse(context.Background())
	if err != nil {
		return nil, networkError("get join token from --from", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.Token == "" {
		return nil, &ExitError{
			msg: fmt.Sprintf("%s returned no join token",
				output.Sanitize(from)),
			code: ExitGeneral,
		}
	}
	return resp.JSON200, nil
}

func sendJoin(
	rt *module.Runtime, cmd *cobra.Command, token string,
	serverURLs []string,
) error {
	client, err := clientFromCmd(rt, cmd)
	if err != nil {
		return err
	}
	resp, err := client.JoinCluster(context.Background(),
		api.JoinClusterJSONRequestBody{
			Token: token, ServerUrls: serverURLs,
		})
	if err != nil {
		return networkError("join cluster", err)
	}
	return checkEmptyBodyResponse(resp, "join cluster")
}

// redactToken keeps the token out of an error message even when a
// server echoes it in prose, where the body redaction keyed on the
// "token" field cannot see it.
func redactToken(err error, token string) error {
	if err == nil || token == "" ||
		!strings.Contains(err.Error(), token) {
		return err
	}
	return &ExitError{
		msg:  strings.ReplaceAll(err.Error(), token, "[REDACTED]"),
		code: cli.ExitCode(err),
	}
}

func listHostsCtx(
	ctx context.Context, member *api.ClientWithResponses,
) ([]api.Host, error) {
	resp, err := member.ListHostsWithResponse(ctx)
	if err != nil {
		return nil, networkError("list hosts", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	return resp.JSON200.Hosts, nil
}

// waitForJoinedHost polls until every host absent from before is
// healthy, with waitForTask's deadline and exit-3 contract.
func waitForJoinedHost(
	rt *module.Runtime, member *api.ClientWithResponses,
	before map[string]bool, timeout, interval int,
) error {
	if interval < 1 {
		interval = 1
	}
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	expired := &ExitError{
		msg: fmt.Sprintf("timed out after %ds waiting for the joining "+
			"host to report healthy", timeout),
		code: ExitTimeout,
	}
	for {
		if time.Now().After(deadline) {
			return expired
		}
		ctx, cancel := context.WithDeadline(
			context.Background(), deadline)
		hosts, err := listHostsCtx(ctx, member)
		cancel()
		if err != nil {
			if time.Now().After(deadline) {
				return expired
			}
			return err
		}
		joined, healthy := 0, 0
		for _, h := range hosts {
			if before[h.Id] {
				continue
			}
			joined++
			state := string(h.Status.State)
			if state == string(api.HostStatusStateHealthy) {
				healthy++
			}
			fmt.Fprintf(rt.Stderr, "Host %s: %s.\n",
				output.Sanitize(h.Id), output.Sanitize(state))
		}
		if joined > 0 && healthy == joined {
			return nil
		}
		if joined == 0 {
			fmt.Fprintln(rt.Stderr,
				"Waiting for the joining host to be listed...")
		}
		time.Sleep(time.Duration(interval) * time.Second)
	}
}
