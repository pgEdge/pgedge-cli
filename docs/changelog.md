# Changelog

## Unreleased

- `starfleet invite create --email <addr>` creates an invite with a
  browser sign-in. The table omits the invite's token and accept URL;
  `-o json` and `-o yaml` print them. An API client still exits 5.
- byoc `--wait` and `--follow` wait for the cluster, database or
  backup store to reach its end state, not only for the task, and exit
  1 naming any other status it settles on. `starfleet byoc database
  wait` waits on its own.
- byoc `database mcp apply`, `rag apply` and `postgrest apply` deploy
  a service, or update the one already there.
- A byoc write refused because the database is busy names the
  database's current status.
- byoc `ingress create --database-id` takes the cluster and region
  from the database, and `ingress service register --service` reads
  the service ID from it.
- `starfleet doctor` reports a one-word `verdict`, and `--strict`
  exits non-zero unless it is `ready`.
- `starfleet doctor` reports whether the tenant can use BYOC, so an
  empty byoc list is no longer ambiguous.
- `starfleet auth status --verify` reads the tenant with the
  credential and exits 5 when the server rejects it.
- `starfleet membership list --email` and `starfleet invite get
  --email` look a person up by address, and exit 4 when none matches.
- `controlplane cluster join --from <member>` reads the join token
  from an existing member and joins in one step, `--token-file` reads
  it from a file, and `--wait` waits for the new host to be healthy.
- `controlplane doctor --require reachable,cluster,supported` exits 1
  unless every named condition holds.
- `controlplane database instance start`, `stop` and `restart` take a
  node name.
- `controlplane task list --all` reads every page.
- `controlplane database get --failure` prints the latest task's
  error.
- Managed `--wait` and `--follow` wait for the database or branch to
  reach its end state, not only for the task, and exit 1 naming any
  other status it settles on. `starfleet managed database wait` waits
  on its own.
- `managed backup create --wait` waits for the backup itself to
  complete.
- `managed database mcp apply` and `rag apply` deploy a service, or
  update the one already there.
- `managed database mcp deploy` and `rag deploy` take `--allow`,
  `--my-ip` and `--open`, so a new service starts with its allowlist.
- `--dbname` on `managed database connection-string`, `env pull` and
  `inspect` reaches another database inside the same managed database.
- `starfleet client create --secret-file <path>` writes the new
  secret to a file only you can read, and prints the client without
  it.
- `starfleet auth login` reads the secret from `--client-secret-stdin`
  or `--client-secret-file`, so it stays out of the command line.
- `starfleet managed database mcp connection` prints a database's MCP
  endpoint and token, and nothing else.
- `starfleet byoc database env pull` writes a node's `DATABASE_URL`
  into `.env`.
- `byoc database service remove` refuses a service type the database
  does not have.
- `controlplane --follow` prints a failed task's error.
- Asynchronous deletes and password rotations report that the request
  was accepted, rather than that the work is done.

## 0.5.0-beta.3

- `brew install pgEdge/tap/pgedge` installs the CLI.
- `controlplane database update` asks for confirmation before a spec
  removes nodes, and `--force` skips it.

## 0.5.0-beta.2

- `starfleet managed database link` ties a project folder to a
  database, and `pgedge env pull` writes its `DATABASE_URL` into
  `.env`. In a linked folder, the read commands use the linked
  database when you leave out the ID.
- Run in a terminal without an ID, `link` lists your databases and
  their branches to choose from, then offers to write `.env`.
- `env pull --branch <branch-id>` writes one branch's `DATABASE_URL`
  and leaves the link as it is.
- The `pgEdge/pgedge-cli` GitHub Action installs a verified release on
  Linux and macOS runners.
- `install.sh` installs the release named in `PGEDGE_VERSION`, and
  skips shell completion when `CI` is set.
- Releases are signed with a Sigstore bundle. To upgrade from
  0.5.0-beta.1, run `install.sh` again, because that release's
  `pgedge self update` checks the older signature format.
- `byoc ssh-key create` refuses DSA keys, which nodes reject.

## 0.5.0-beta.1

The first public release.

- `pgedge starfleet` authenticates against the pgEdge Starfleet API
  and manages account resources, with two infrastructure sub-trees:
  `managed` for pgEdge-hosted databases and `byoc` for clusters and
  databases in your own cloud account.
- `pgedge controlplane` drives a self-hosted pgEdge Control Plane.
- `pgedge inspect` runs diagnostic analyses against any Postgres
  reachable by connection string.
- `pgedge llms` prints the reference for AI agents, and the agent
  skills install with `npx skills add pgEdge/pgedge-cli`.
- Every write accepts `--dry-run`, every read accepts `-o json` and
  `-o yaml`, and named profiles hold per-environment settings.
- `pgedge self update` and `install.sh` verify each release's
  checksum and Sigstore signature before installing it.
