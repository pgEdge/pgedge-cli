# Telemetry and privacy

Every network contact the binary makes, when it makes it, and every
file it leaves on your disk is listed here. The list is an
engineering description of the code. It is not a commitment about
retention or third parties.

## The CLI collects no usage data

The CLI imports no analytics, telemetry, crash-reporting or
usage-tracking library. Nothing records which commands you run, which
flags you pass, how long they took, or whether they failed, and nothing
transmits any of that anywhere.

OpenTelemetry and Prometheus packages do appear in the Go module graph.
They are there for different reasons and neither reports anything.

Prometheus reaches the module graph through a dependency of a
dependency, and no package of it is compiled into the binary at all.

OpenTelemetry is compiled in: seventeen of its packages, reached
through `self update`'s signature verification, where Sigstore's
transparency-log client is built on an OpenAPI runtime that carries
tracing hooks. The generated pgEdge API clients are not the route and
contribute none of it. What makes it inert is that nothing configures
an exporter, so the global tracer resolves to OpenTelemetry's own
no-op implementation and every span it creates is discarded before it
reaches anything. Nothing in the CLI's own source imports either
library.

## What the binary contacts

Seven hosts across six rows, and only one of them is a pgEdge host by
default. Every one of the GitHub and Sigstore calls happens on a
command you ran deliberately.

The complete outbound set:

| Host | Reached by | What for |
|---|---|---|
| The Starfleet API base URL, `https://api.pgedge.com` unless a profile or `--api-url` says otherwise | every `starfleet`, `byoc` and `managed` command | The API call the command exists to make. |
| The Control Plane base URL you configured | every `controlplane` command | The API call the command exists to make. |
| `http://localhost:3000` | every `controlplane` command, when no profile and no `--base-url` supplies one | The same call, against the address the Control Plane serves on by default. |
| `api.github.com` and `github.com` | `pgedge self update` and `pgedge doctor` (not under `--no-version-check`) | Listing releases, and downloading an archive when an update proceeds. |
| `rekor.sigstore.dev` | `pgedge self update` performing a real update | Looking up the transparency-log entry that proves pgEdge's release workflow signed the checksums. |
| `tuf-repo-cdn.sigstore.dev` | `pgedge self update` performing a real update | Refreshing the Sigstore public-good trust root that the signature is checked against. |

Neither Control Plane row is a pgEdge-owned address. That module has
no login and no hosted service, so it reaches either a host you named
or, when you have named none, `http://localhost:3000`, which is where
the Control Plane serves by default. That fallback is the one address
in the table you can reach without having configured anything, and it
is plain HTTP and loopback, so nothing leaves the machine.

One host that turns up in a strings dump of the binary is never
contacted at all. `cli.github.com` appears inside a single error
message, the one telling you where to install `gh` when the CLI cannot
find it, and no code path fetches that URL.

## Nothing runs in the background

The CLI has no startup version check, no periodic check and no
deferred reporting. Contact with GitHub happens on exactly two
commands, and contact with Sigstore on one:

- `pgedge self update` lists releases, and on a real update also
  downloads the archive, refreshes the Sigstore trust root and queries
  Rekor. `--check` returns after the release listing, before any
  download or verification, so it reaches GitHub and neither Sigstore
  host.
- `pgedge doctor` lists releases to fill in its latest-version row,
  under a five-second bound, and downloads nothing. Pass
  `--no-version-check` to skip that lookup.

Every other command in every module talks only to the API base URL it
was configured with. The one thing that does run before the command
tree is even built is a Windows-only cleanup that deletes a stale
backup file left behind by a previous self-update swap, and it touches
no network.

The GitHub lookup itself has two rungs. The CLI first tries the public
GitHub API unauthenticated, and falls back to shelling out to the `gh`
CLI only when that fails. On the second rung the request is made by
`gh` under your own GitHub credentials rather than by the CLI.

## What is written to local disk

Everything the CLI owns lives under `~/.pgedge/cli`, and it creates
that directory at mode 0700. The paths, and what puts them there:

| Path | Written by |
|---|---|
| `~/.pgedge/cli/config.yaml` | `starfleet auth login`, `controlplane config set` and the other config writers. |
| `~/.pgedge/cli/cache/<profile>-<module>.json` | A successful token exchange, one file per profile and module. |
| `~/.pgedge/cli/cache/sigstore/` | `pgedge self update`, caching the Sigstore trust root. |
| Your shell's completion directory, or one line in your shell startup file | `pgedge completion install`, and the regeneration that follows a successful `self update`. |
| The directory the running binary sits in | `pgedge self update`, staging the new binary beside the old one before swapping. |

`starfleet auth login` also writes one OS keychain entry per profile
and config file, under the service name `pgedge-cli`, unless it
stores the secret in the config file instead.
`starfleet auth logout` removes it.

Diagnostics never reach a file. `--verbose` and `--debug` write to
`stderr` and the CLI opens no log file anywhere.

### The client secret lives in the OS keychain

`pgedge starfleet auth login` stores the client secret in the OS
keychain. `config.yaml` holds the `client_id`, the API URL, the
Control Plane certificate paths and your output preference. A program
running as your user can read your keychain entries, so the keychain
keeps the secret out of backups and copied files, not away from
software on your machine.

`config.yaml` stores `client_secret` as a plain YAML string in three
cases: `login` could not use a keychain, `login` ran with
`--insecure-storage`, or an earlier version wrote it there. The CLI
writes the file at mode 0600 and
re-applies that mode on every save, so a file that had been loosened
is tightened again the next time a command writes it. The write is
atomic in the same way as the cache write below: staged beside the
file, then renamed over it, so an interrupted save leaves the previous
config intact rather than a truncated one. One consequence is that a
config directory you cannot write to refuses the save, even when the
file itself is writable. A staging file left by a process killed
mid-save carries the same secrets at the same 0600 mode and is named
`.config.yaml.tmp` followed by digits.

### The token cache holds a live token in clear text

Each cache file carries three fields and nothing else: `access_token`,
`expires_at`, and `binding_fingerprint`. The access token is stored
unencrypted, in a file written at mode 0600 inside a directory at
0700. The keychain holds the client secret, never the token.

`binding_fingerprint` is the reason your client secret is not in that
file. It is a SHA-256 digest over a domain-separation prefix followed
by the resolved API base URL, the client ID and the client secret,
each preceded by a NUL byte, rendered as hex behind a `sha256:`
prefix. The digest is one-way and is never printed. The only thing the
CLI does with it is compare it against a fingerprint recomputed from
the connection it is about to dial, so a cached token cannot be
replayed against a host that did not mint it. A cache file with no
fingerprint counts as a mismatch and costs one silent re-exchange rather than
trusting an unbound token.

The cache write is atomic. The CLI creates a temporary file in the
same directory, sets 0600 on it, writes, then renames it over the
target, and `starfleet auth logout` sweeps any staging file an
interrupted write left behind. There is no `fsync`, so a power loss
can leave an unreadable cache file, whose only consequence is that the
next command re-authenticates.

## Secrets in diagnostic output

`--verbose` and `--debug` print HTTP traffic, and both redact rather
than echo. A password embedded in a URL's userinfo is masked before
the request line is printed, and at `--verbose` an `Authorization`
header is printed as a mask rather than its value. At `--debug`, where
request and response bodies are dumped, a JSON body carrying any key
the CLI classifies as credential-bearing has that value masked, and a
body it cannot parse well enough to mask precisely is replaced with a
summary of its top-level keys rather than printed.

That classification covers `password`, so a database connection block
dumped under `--debug` does not print the database password. The list
also covers `access_token`, `client_secret`, the cloud-provider keys
and the service-config secrets.

## Avoiding the GitHub and Sigstore calls

The Rekor and Sigstore trust-root calls belong to `self update` alone,
so an install that never runs that command never makes them. Building
from a checkout with `make build`, or installing with `go install`,
both leave you with a binary you update through the tool that put it
there, and the [updating guide](updating.md) covers which installs
`self update` refuses outright.

`pgedge doctor` reaches `api.github.com` for its latest-version row
unless you pass `--no-version-check`, which skips that lookup and
leaves every other check in place. With the flag, `doctor` contacts
nothing off the machine. Avoiding GitHub entirely then means avoiding
`self update`, not `doctor`.

## Next steps

- The [configuration guide](configuration.md) covers what the config
  file holds and the short list of environment variables the CLI
  reads.
- The [authentication guide](auth-and-profiles.md) covers how
  credentials resolve and what the token cache binding protects
  against.
- The [updating guide](updating.md) covers the release lookup, the
  signature verification and the installs `self update` refuses.
