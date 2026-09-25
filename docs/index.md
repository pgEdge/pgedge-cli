# pgEdge CLI

`pgedge` is one command-line interface for the whole pgEdge product
suite. Every command takes the form `pgedge <module> <resource>
<command>`. The two modules are the top-level command groups:

- `starfleet` covers pgEdge Starfleet authentication and account
  resources. The nested `byoc` and `managed` sub-trees of `starfleet`
  cover infrastructure.
- `controlplane` covers the Control Plane.

## Building the CLI and Signing In

To start, build the binary, print the command tree and sign in:

    make build
    ./pgedge --help
    ./pgedge starfleet auth login

Your configuration lives in `~/.pgedge/cli/config.yaml`, which holds
named profiles. For a sample, see `examples/config.yaml` in the
repository.

The command reference pages are generated from the live command tree.
Each page gives the usage, description, declared flags and a worked
example for every command it covers:

| Commands | Reference page |
|---|---|
| `pgedge` | [pgedge command reference](reference/pgedge.md) |
| `pgedge starfleet` | [pgedge starfleet command reference](reference/starfleet.md) |
| `pgedge starfleet byoc` | [pgedge starfleet byoc command reference](reference/starfleet-byoc.md) |
| `pgedge starfleet managed` | [pgedge starfleet managed command reference](reference/starfleet-managed.md) |
| `pgedge controlplane` | [pgedge controlplane command reference](reference/controlplane.md) |

The core guides cover what every module shares, grouped by task:

| Task | Core guides |
|---|---|
| Setting up and running commands | [Authentication and profiles](auth-and-profiles.md), [Configuration and environment](configuration.md), [Exit codes](exit-codes.md), [Output formats and paging](output-and-paging.md), [Dry runs](dry-run.md) |
| Connecting an application | [Connecting an Application to a pgEdge Starfleet Managed Database](managed/connect-an-application.md), [Connecting an Application to a pgEdge Starfleet BYOC Database](byoc/connect-an-application.md), [ORM and framework integration](orm-and-frameworks.md) |
| Inspecting and automating | [Inspect a database](inspect-a-database.md), [AI agents](ai-agents.md), [CI and automation](ci.md) |
| Getting help | [Troubleshooting](troubleshooting.md), [Error catalog](error-catalog.md), [Versions, uninstall and support](support-versioning-and-uninstall.md) |

Each module also has its own guides:

| Module | First guide | Other guides cover |
|---|---|---|
| pgEdge Starfleet account | [Account, tenant and API clients](starfleet/account-and-clients.md) | Onboarding a teammate and rotating the CLI's own credential |
| Managed | [Provision a managed database](managed/provision.md) | Loading, migrating and exporting data, roles, services, backups, logs and metrics, monitoring and incident runbooks |
| BYOC | [Provision a BYOC cluster and database](byoc/provision.md) | Cloud accounts, clusters, databases, services, backups, logs and metrics, monitoring and incident runbooks |
| Control Plane | [Stand up a local Control Plane](controlplane/local-server.md) | Databases, high availability, backups, monitoring and incident runbooks |

### Creating a Control Plane Database

Set the `controlplane` module's base URL, then build a database from a
spec:

    pgedge controlplane config set --base-url http://localhost:3000
    pgedge controlplane version
    pgedge controlplane database init --nodes 3 > spec.yaml
    pgedge controlplane database init -i > spec.yaml
    pgedge controlplane database init -i -o json | \
      pgedge controlplane database create mydb -f -
    pgedge controlplane database create my-db -f spec.yaml --wait

With `-i`, the `-o json` flag prints the populated spec as JSON, ready
to pipe into `create`. Without `-i`, `-o json` makes the command exit
with a usage error. Non-interactive `init` emits YAML only.

For high availability, pass more than one `--base-url` flag. The CLI
tries each URL in order. The CLI then uses the first server that
answers `GET /v1/version`:

    pgedge controlplane database list --base-url http://cp-1:3000 \
      --base-url http://cp-2:3000

## Enabling Shell Completion

To turn on Tab completion for commands, subcommands and flags, run:

    pgedge completion install

The command detects your shell: bash, zsh, fish or PowerShell. The
command then sets up completion in one of two ways:

- **File install (default).** The command writes a completion script
  into the directory your shell loads completions from. The command
  then prints any step left for you to take. Examples are a zsh `fpath`
  line and a PowerShell profile line. Your shell loads the script like
  any other completion file, and `pgedge self update` regenerates it.
- **rc line (`--rc-only`).** The command appends one line to your
  shell's startup file in place of the script. Completion then always
  matches the binary you have installed.

To use the rc line, run the first command below. The line that
command adds depends on your shell:

    pgedge completion install --rc-only

    # ~/.bashrc
    eval "$(pgedge completion bash)"

    # ~/.zshrc
    eval "$(pgedge completion zsh)"

    # ~/.config/fish/config.fish
    pgedge completion fish | source

    # PowerShell $PROFILE
    pgedge completion powershell | Out-String | Invoke-Expression

To remove completion, run `pgedge completion uninstall`. This command
removes whichever of the two setups is in place. To wire completion by
hand, print one shell's script with `pgedge completion <shell>`.

## Developing the CLI

To build, test and lint the checkout, run:

    make build test lint

To contribute, follow the guidelines in CONTRIBUTING.md in the
repository. That file also explains how the vendored OpenAPI specs in
`openapi/` and the clients generated from them are maintained.
