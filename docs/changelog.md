# Changelog

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
- `brew install pgEdge/tap/pgedge` installs the CLI.
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
