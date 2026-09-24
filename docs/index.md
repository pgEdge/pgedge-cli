# pgEdge CLI

`pgedge` is the unified command-line interface for the pgEdge
product suite: `pgedge <module> <resource> <command>`.

Modules: `starfleet` (Starfleet authentication and account
resources, plus the nested `byoc` and `managed` infrastructure
sub-trees) and `controlplane` (Control Plane).

## Quickstart

Build the binary, print the command tree, then sign in:

    make build
    ./pgedge --help
    ./pgedge starfleet auth login

Configuration lives at `~/.pgedge/cli/config.yaml` with named
profiles. See `examples/config.yaml` in the repository for a
sample.

Every command's usage, description, declared flags and a worked
example are on the command reference pages: [pgedge](reference/pgedge.md),
[starfleet](reference/starfleet.md), [starfleet byoc](reference/starfleet-byoc.md),
[starfleet managed](reference/starfleet-managed.md) and
[controlplane](reference/controlplane.md). They are generated from the
live command tree.

The core guides cover what every module shares:
[authentication and profiles](auth-and-profiles.md),
[configuration and environment](configuration.md),
[exit codes](exit-codes.md),
[output formats and paging](output-and-paging.md),
[dry runs](dry-run.md),
connecting an application on
[Managed](managed/connect-an-application.md) or
[BYOC](byoc/connect-an-application.md),
[ORM and framework integration](orm-and-frameworks.md),
[inspecting a database](inspect-a-database.md),
[AI agents](ai-agents.md),
[CI and automation](ci.md), [troubleshooting](troubleshooting.md),
[the error catalog](error-catalog.md) and
[versions, uninstall and support](support-versioning-and-uninstall.md).

Each module has its own guides:

- Starfleet account:
  [account, tenant and API clients](starfleet/account-and-clients.md),
  onboarding a teammate and rotating the CLI's own credential.
- Managed: from [provisioning a database](managed/provision.md)
  through loading, migrating and exporting data, roles, services,
  backups, logs and metrics, monitoring, and incident runbooks.
- BYOC: from [provisioning a cluster](byoc/provision.md) through
  cloud accounts, clusters, databases, services, backups, logs and
  metrics, monitoring, and incident runbooks.
- Control Plane: from
  [standing up a local server](controlplane/local-server.md) through
  databases, high availability, backups, monitoring, and incident
  runbooks.

### Control Plane (controlplane)

The `controlplane` module targets a base URL and builds a database from
a spec:

    pgedge controlplane config set --base-url http://localhost:3000
    pgedge controlplane version
    pgedge controlplane database init --nodes 3 > spec.yaml
    pgedge controlplane database init -i > spec.yaml
    pgedge controlplane database init -i -o json | \
      pgedge controlplane database create mydb -f -
    pgedge controlplane database create my-db -f spec.yaml --wait

The `-o json` flag requires `-i`, emitting the populated spec as JSON
for piping into `create`. Without `-i`, the command exits with a usage
error. Non-interactive `init` is YAML-only.

For HA, pass multiple `--base-url` flags: the CLI probes each in order
and uses the first server that answers `GET /v1/version`.

    pgedge controlplane database list --base-url http://cp-1:3000 \
      --base-url http://cp-2:3000

## Shell completion

Enable Tab completion for commands, subcommands and flags:

    pgedge completion install

Detects your shell (bash, zsh, fish or PowerShell) and sets completion
up one of two ways:

- **File install (default).** Writes a completion script into the
  directory your shell loads completions from, and prints any
  remaining step (a zsh `fpath` line, a PowerShell profile line).
  Nothing runs at shell start, and `pgedge self update` regenerates
  the script.
- **rc line (`--rc-only`).** Appends one line to your shell's startup
  file and writes no script, so completion can never be out of step
  with the installed binary. That costs roughly 10-20 ms on a warm
  cache.

The second route, and the lines it adds:

    pgedge completion install --rc-only

    # ~/.bashrc
    eval "$(pgedge completion bash)"

    # ~/.zshrc
    eval "$(pgedge completion zsh)"

    # ~/.config/fish/config.fish
    pgedge completion fish | source

    # PowerShell $PROFILE
    pgedge completion powershell | Out-String | Invoke-Expression

`pgedge completion uninstall` removes whichever of the two is in
place. Any shell's script can also be printed on its own with
`pgedge completion <shell>` for manual wiring.

## Development

Build, test and lint the checkout:

    make build test lint

See CONTRIBUTING.md in the repository for contribution guidelines,
including how the vendored OpenAPI specs in `openapi/` and the
clients generated from them are maintained.
