# Changelog

## Unreleased

- The CLI installs from npm as `@pgedge/cli`, and `pgedge self update`
  refuses an npm install, naming the npm command to run instead.

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
