# Installing the Agent Skills

You can install pgEdge's agent skills so that a coding agent runs the
`pgedge` CLI correctly. A skill is a `SKILL.md` file in the Agent
Skills open standard. pgEdge ships five skills in the
`plugins/pgedge/skills/` directory of the repository:

| Skill | Covers |
|---|---|
| `pgedge` | CLI orientation, choosing a module, profiles and installation. |
| `pgedge-starfleet` | pgEdge Starfleet authentication, API clients, team invites and memberships. |
| `pgedge-byoc` | BYOC clusters, databases, services, ingresses, backups and SSH keys. |
| `pgedge-managed` | Managed databases, their services and their backups. |
| `pgedge-controlplane` | A self-hosted Control Plane: cluster formation, databases, backups and failover. |

Skills are a layer above the CLI, so you install them separately from
the `pgedge` binary. When an agent finds no `pgedge` on the PATH, the
skills tell it to install the CLI first.

## Choosing an Install Route

Every route installs the same five skills. This table lists the routes:

| Route | Agents | Also needs |
|---|---|---|
| `npx skills add` | Claude Code, Codex, Cursor, Copilot, Amp, Antigravity and a dozen others | Node.js |
| Claude Code plugin | Claude Code | Nothing more |
| Codex plugin | Codex | Nothing more |
| Copy by hand | Any agent | `git` |

A plugin installs the skills of one release, the release its
marketplace names, so they match a binary of that release. A route
that reads the main branch, `npx skills add` or a copy by hand, takes
the newest skills instead.

## Installing with npx skills

The `skills` installer detects your agent. Install all five skills for
your user, so every project has them:

    npx skills add pgEdge/pgedge-cli --global

The installer writes to `.agents/skills/` in your home directory, the
shared location that supporting agents read. For an agent with a
convention of its own, it adds a symlink to that location.

To share the setup with teammates and cloud agents, run the command
without `--global` from your project. The skills then land in that
project, and `skills-lock.json` pins each one by content hash. Commit
both.

To install a single skill, name it:

    npx skills add pgEdge/pgedge-cli --skill pgedge-managed

To match the skills to an installed release, name its tag:

    npx skills add 'pgEdge/pgedge-cli#<release-tag>' --global

`pgedge version` prints the release with its `v` prefix stripped, so
add the `v` back for the tag. Afterward, `npx skills list`,
`npx skills update` and `npx skills remove` manage the skills.

## Installing the Claude Code Plugin

In Claude Code, the repository is also a plugin marketplace. One
command installs all five skills as the `pgedge` plugin:

    /plugin install pgedge --marketplace pgEdge/pgedge-cli

To update the plugin to a newer release, run
`claude plugin update pgedge@pgedge-cli`, then restart Claude Code. To
remove it, run `claude plugin uninstall pgedge@pgedge-cli`.

## Installing the Codex Plugin

In Codex, the repository is also a plugin marketplace. Add the
marketplace, then install the `pgedge` plugin from it:

    codex plugin marketplace add pgEdge/pgedge-cli
    codex plugin add pgedge@pgedge-cli

To update the plugin to a newer release, run
`codex plugin marketplace upgrade pgedge-cli`, then run
`codex plugin add pgedge@pgedge-cli` again. To remove it, run
`codex plugin remove pgedge@pgedge-cli`.

## Copying the Skills by Hand

This route works with any agent and needs no Node.js. Copy the skills
into your project:

    git clone https://github.com/pgEdge/pgedge-cli
    mkdir -p .agents/skills
    cp -r pgedge-cli/plugins/pgedge/skills/* .agents/skills/

To install for Claude Code alone, use `~/.claude/skills/` in place of
`.agents/skills/`. If your agent reads neither location, copy the
skills to the place its own documentation names. To remove the skills,
delete the copied directories.

## Loading the Skills

An agent session that is already running loads new skills after a
restart. In Claude Code, `/reload-plugins` loads them without one.

## Next Steps

The skills point the agent at the CLI's built-in reference. These
pages cover what comes next:

- [Working with AI Agents](ai-agents.md) covers the built-in reference
  and deploying an MCP server.
- [Getting Started](getting-started.md) covers installing the CLI.
