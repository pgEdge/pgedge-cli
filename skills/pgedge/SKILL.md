---
name: pgedge
description: >
  Use when a user works with the pgEdge CLI itself — installing it,
  checking versions, choosing a module, configuring profiles/auth, or
  getting oriented. pgedge is a unified CLI with two top-level modules
  — starfleet (Starfleet authentication and account resources, plus the byoc
  and managed infrastructure sub-trees nested under it) and controlplane (a
  self-managed Control Plane) — each with its own skill; starfleet's byoc
  and managed sub-trees additionally have their own skills.
  Triggers on: pgedge, pgedge CLI, pgedge install, pgedge version,
  pgedge profiles, "which pgedge module".
---

# pgedge CLI (root)

`pgedge` is one binary that dispatches to modules:
`pgedge <module> <resource> <verb>`.

## Modules

- **starfleet** — Starfleet. Authentication and account resources
  (login, team invites, team memberships) at the top level, plus two
  infrastructure sub-trees nested under it:

    - `pgedge starfleet byoc` — pgEdge BYOC infrastructure in *your own*
      cloud account (clusters, databases, MCP/RAG/PostgREST services,
      backups, ingresses).
    - `pgedge starfleet managed` — databases pgEdge hosts and operates for
      you. No clusters or nodes to place them on; a region and a size
      instead.

  Every starfleet command — the account-level verbs and both sub-trees —
  shares the one connection `pgedge starfleet auth login` establishes.
- **controlplane** — a self-managed pgEdge Control Plane (cluster init/join,
  distributed databases, backups, switchover/failover). Independent
  of `starfleet`: no login, targets `--base-url` directly. The
  control-plane server is yours to run: the `pgedge-controlplane`
  skill's "Get a Control Plane running" has the image and its
  requirements.

**Choosing between them:** starfleet or controlplane first — is pgEdge running this
for you (`starfleet`), or are you running the Control Plane yourself on
your own servers (`controlplane`)? Within `starfleet`, whose infrastructure is it?
Someone else's account you administer → `byoc`. pgEdge's → `managed`.
`byoc` and `managed` both borrow `starfleet`'s credential, and the
tenant's **plan** decides which of the two works — `pgedge starfleet
doctor` reports it.

## Orientation

- **Version:** `pgedge version` prints the launcher version plus each
  module's version. Use `-o json` to parse.
- **Full reference:** `pgedge llms` prints the index and routes to
  `pgedge llms <module>`, which routes on to one page per resource
  (`pgedge llms starfleet byoc cluster`). Prefer those short reads
  over improvising from `--help`.
- **Config/profiles:** `~/.pgedge/cli/config.yaml`, named profiles with
  per-module sections; precedence flag > profile > default. Select
  with `--profile`, or with `current_profile` in the file. Either way
  the name must already be configured, or be the built-in `default`;
  anything else exits 1 naming the profiles that are. Two exceptions:
  `starfleet auth login` and `controlplane config set` create the named profile, and
  `pgedge profile use default` needs a `default` section in the
  file.
- **Broken `current_profile`:** if the file names a profile that does
  not exist, every command exits 1 rather than falling back to the
  production URL. `pgedge profile list` still runs and marks the row
  `yes (unresolved)`; `pgedge profile use <name>` repairs it.
- **Diagnostics:** `pgedge starfleet doctor` (the Starfleet connection byoc
  and managed both borrow) and `pgedge controlplane doctor` (Control Plane).
  Inside a database, `database inspect <id> table-sizes` on managed and
  byoc, or `pgedge inspect table-sizes --db-url <uri>` for any Postgres,
  runs one read-only diagnostic; `pgedge llms inspect` lists every
  analysis.
- **Dry runs:** every verb that writes accepts `--dry-run`. It runs the
  client-side checks, stops before the write, and prints the request it
  would have sent. Reads still go out, so it needs credentials; nothing
  server-side is validated (no API exposes a validate endpoint), which
  makes this a CLIENT dry run in kubectl's sense. So `cmd --dry-run &&
  cmd` is **not** a gate on the real run: an input only the API can
  reject passes the dry run and fails the real call. Read the checks it
  lists rather than trusting the exit code. On a read-only verb the flag
  is unknown (exit 2).

## Installing the CLI

On Linux or macOS, the install script installs the newest release:

```bash
curl -fsSL https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/install.sh | sh
```

It verifies the archive's checksum, and the release signature when
`cosign` is installed. It installs to `/usr/local/bin`, or to
`~/.local/bin` when that is not writable, and sets up shell
completion. The other options are documented in
`docs/getting-started.md` of the pgEdge/pgedge-cli repository:

- **Hand it to an agent** — that document and `README.md` each carry
  the same install prompt, written for exactly this reader. It runs
  the install script, enables shell completion and installs these
  skills in one pass, asking which products the user wants.
- **A release archive** — download it from the repository's
  [GitHub Releases](https://github.com/pgEdge/pgedge-cli/releases)
  page, verify it, then extract it. This is the Windows route.
- **From source** —
  `go install github.com/pgEdge/pgedge-cli/cmd/pgedge@main`, or clone
  the repository and run `make build`.

Questions and problem reports go to
[GitHub issues](https://github.com/pgEdge/pgedge-cli/issues).

## Installing these skills

Skills are a layer above the CLI, distributed from the `skills/`
directory of this repository.

**Recommended — the skills CLI.** One command installs all five and
detects your agent:

```bash
npx skills add pgEdge/pgedge-cli
```

It writes to `.agents/skills/` — the shared location every supporting
agent reads — and symlinks it for the ones with their own convention,
so Claude Code, Cursor, Copilot, Amp, Antigravity and a dozen others
pick the skills up from a single install. Project scope is the
default, which puts them in the repository so teammates and cloud
agents get the same setup; add `--global` to install for your user
instead.

Useful follow-ups:

```bash
npx skills list                 # what is installed, and where
npx skills update               # pull newer versions
npx skills remove pgedge-byoc   # drop one
```

Installing writes `skills-lock.json`, which pins each skill by content
hash — commit it, and `npx skills experimental_install` restores the
same set.

To take just one:

```bash
npx skills add pgEdge/pgedge-cli --skill pgedge-managed
```

**Manual copy** (any agent, no Node required):

```bash
git clone https://github.com/pgEdge/pgedge-cli
mkdir -p .agents/skills
cp -r pgedge-cli/skills/* .agents/skills/
```

Use `~/.claude/skills/` instead of `.agents/skills/` if you want them
for Claude Code alone rather than every agent.
