# pgEdge plugin

Agent skills for the `pgedge` command-line tool. They teach an agent to
manage pgEdge Starfleet Managed and BYOC databases, Starfleet accounts,
and a self-hosted pgEdge Control Plane by running `pgedge` commands and
reading the CLI's own reference with `pgedge llms`.

## Skills

- `pgedge`: an orientation to the CLI, and which module to choose
- `pgedge-starfleet`: Starfleet authentication, API clients, invites
  and team memberships
- `pgedge-managed`: Managed databases, their services and their backups
- `pgedge-byoc`: BYOC clusters, databases, services and backups
- `pgedge-controlplane`: a self-hosted Control Plane

## What the skills run

The skills run `pgedge` commands, which call the pgEdge Starfleet API or
the Control Plane you point the CLI at. When `pgedge` is not installed,
the skills tell the agent to say so to the user and install it, with
`brew install pgEdge/tap/pgedge` where Homebrew is present, or otherwise
with the install script from this repository:

    curl -fsSL https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/install.sh | sh

The script downloads a release from GitHub and verifies its checksum.

## Install

In Claude Code:

    /plugin install pgedge --marketplace pgEdge/pgedge-cli

In Codex:

    codex plugin marketplace add pgEdge/pgedge-cli
    codex plugin add pgedge@pgedge-cli

Other agents install with `npx skills add pgEdge/pgedge-cli`.
[Installing the Agent Skills](https://github.com/pgEdge/pgedge-cli/blob/main/docs/agent-skills.md)
covers every route.

## License

PostgreSQL License. See
[LICENSE.md](https://github.com/pgEdge/pgedge-cli/blob/main/LICENSE.md).
