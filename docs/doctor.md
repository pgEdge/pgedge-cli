# Health checks

Three commands exist to be run when something is broken: `pgedge
doctor` for the installation, `pgedge starfleet doctor` for the
Starfleet connection, and `pgedge controlplane doctor` for a Control
Plane. `pgedge controlplane config view` is a fourth in the same
spirit, reporting the resolved Control Plane settings rather than
judging them.

## Exit status

All four report rather than fail. An unreachable server, a missing
credential, an uninitialized cluster and a profile field that will not
parse are each printed as a row, at exit 0. That is what makes them
safe to run precisely when the thing they diagnose is broken, and it
also means `pgedge starfleet doctor && <next step>` reads as a gate
and is not one, unless you pass `--strict`.

A script reads their fields under `-o json`. Two forms exit on the
result instead: `pgedge starfleet doctor --strict` exits non-zero
unless the connection is ready, and `pgedge controlplane doctor
--require` exits 1 unless every condition it names holds.
The one failure that does stop them is a broken `--config`, which is
resolved before any command runs and exits 1. The
[exit codes guide](exit-codes.md) covers that class and the rest.

## pgedge doctor

`pgedge doctor` checks the install itself. It authenticates against
nothing and dials no product API, so it never reports that a
connection works, only that one is configured. It prints five fixed
rows, then one Connection row per connection the active profile
configures.

The following table describes the five fixed rows, the Connection row
that repeats per connection, and what makes each one a warning:

| Check | What it reports | Warns when |
|---|---|---|
| Version | The CLI version, Go runtime, OS and architecture. | Never. |
| Latest version | The newest release on GitHub, compared against the installed version. `--no-version-check` skips the lookup and the row reads "not checked". | A newer release exists, or the check could not run. |
| Config | The config directory, the file name in use, and whether that file exists. | The file does not exist. |
| Shell | The shell from `SHELL`, and whether `pgedge` is on the path. | `pgedge` is not on the path. |
| Install method | npm, homebrew, install-script, go-install or unknown, inferred from the executable's resolved path. | Never. |
| Connection | One row per configured connection: module, profile, resolved base URL, and whether credentials are present. | A `starfleet` row has no client ID and secret. |

A `controlplane` connection row stays `ok` without credentials,
because the Control Plane has no login and its mTLS keypair is
optional. Every connection row that could be attempted carries "not
verified" in its detail, because nothing is dialled. Credential
presence is reported, never a credential value.

The Starfleet row is always listed, since a Starfleet base URL
resolves to a default even on a first run. Control Plane rows appear
only when the profile names at least one base URL. The
[configuration guide](configuration.md) covers where that file lives
and how a profile resolves.

Under `-o json` the report carries `version`, `latest_version`,
`config`, `shell`, `install_method` and a `connections` array whose
entries carry `module`, `profile`, `url`, `has_credentials`, `status`
and `detail`. Nothing here describes the token cache: that file
belongs to the starfleet module, so token state is `pgedge starfleet
doctor`'s to report.

The "Latest version" row runs the same release lookup `pgedge self
update` uses, bounded at five seconds. It is the one check that leaves
the machine. On a network that blocks GitHub, that row warns and the
rest of the report is unaffected. Pass `--no-version-check` to
skip the lookup altogether: the row then reads `ok` with "not
checked", and under `-o json` the `latest_version` object carries
`checked: false`. The [updating guide](updating.md) covers the lookup
and its failure modes.

## pgedge starfleet doctor

`pgedge starfleet doctor` is the verdict on the Starfleet connection,
which byoc and managed both share. There is no byoc or managed doctor
for that reason. It prints five rows, and a sixth, BYOC, when the
verdict is `ready`.

The following table describes each row and what a warning means:

| Check | What it reports | Warns when |
|---|---|---|
| Auth | Which credential source resolved, whether a token is cached, and whether that token is still valid. | Credentials resolve but the token is expired or absent, or a valid cached token was minted for a different connection. It reports `error` when no credentials resolve at all. |
| API connectivity | An unauthenticated GET against the resolved base URL, with the status and latency. | Never. An unreachable endpoint is reported as `error`. |
| Tenant | The tenant the credential authenticates as, its ID and its plan. | The tenant could not be read, or a browser session must renew before it can be read; the next command renews it. |
| BYOC | Whether the tenant can use BYOC: `entitled`, `not-entitled` or `unknown`, the `byoc` field in JSON. Printed only on a `ready` verdict. | The value is `not-entitled` or `unknown`. A failed check reads `unknown`; run any byoc list to see why. |
| Environment | The operating system, architecture, and whether `NO_COLOR` is set. | Never. |
| Verdict | The report in one word, the `verdict` field in JSON. | Never. Any verdict but `ready` is reported as `error`. |

A cached token is bound to the credential and the API base URL that
minted it, so rekeying a profile, overriding either flag for one
invocation, or passing `--api-url` for a different endpoint all produce
the Auth warning about a differently-minted token. The next command
re-authenticates on its own, so this is a warning rather than an error
and needs no action. In JSON,
`auth.token_bound` is `false`. One digest covers both possibilities,
so the message names both rather than guessing which one moved. A
half-supplied `--client-id` and `--client-secret` pair is reported in
`auth.problem` and printed in place of "not authenticated".

The Tenant row is the only one that proves the server accepts the
credential, because reading a tenant is an authenticated call. The API
connectivity row is an unauthenticated probe and says nothing about it. A
profile carries one Starfleet credential that byoc and managed both borrow, so
a profile is a tenant, and the plan decides which modules work. The BYOC
row says whether the tenant can use byoc, so an empty byoc list is not
misread as "no resources yet".

Reading the tenant needs a token, so doctor may exchange one. That
token is used for doctor's reads and never written to the cache, even
under `--api-url`, so running doctor never changes which token a later
command uses. The whole tenant probe is bounded at ten seconds
whatever `--timeout` says, because a diagnostic whose job is to answer
must not hang.

For a script, gate on `--strict` or on `verdict`. Do not gate on
`token_valid`: it describes the token cache, which doctor does not
write, so it stays `false` on a profile with good credentials and no
cached token. The plan does not change the verdict, so read `byoc`
before byoc work.

The following table lists each verdict and the code `--strict` exits
with:

| Verdict | Meaning | `--strict` exit |
|---|---|---|
| `ready` | The tenant read succeeded. | 0 |
| `no-credentials` | No credential resolves, or the keychain cannot be read. | 5 |
| `credentials-misconfigured` | Half a flag or env pair, `--profile` with the env pair, or a bad `--tenant`. | 2 |
| `session-needs-login` | The browser sign-in expired; run `auth login`. | 5 |
| `session-unverified` | The browser sign-in renews on next use, so doctor did not read the tenant; `auth status --verify` renews and checks it. | 5 |
| `endpoint-unreachable` | The API base URL does not answer, so a login will not help. | 1 |
| `credential-rejected` | The API answered the tenant read with 401 or 403, or the token exchange with 400, 401 or 403. | 5 |
| `tenant-unresolved` | The tenant read failed any other way, including a token exchange that timed out or answered 5xx. | 1 |

## starfleet auth status

`pgedge starfleet auth status` answers a narrower question than
doctor: which credential resolved, without calling any endpoint. It
prints `authenticated`, `source`, `problem`, `client_id`, `user`,
`tenant_id`, `api_url`, `token_valid`, `token_bound`, `expires_at` and
`renews`, and the client secret's value never appears in any format.
`user`, `tenant_id` and `renews` describe a browser session. Only the
four booleans are always there: the seven string fields are omitted
when empty, so a run with no credentials configured answers with
`authenticated`, `token_valid`, `token_bound`, `renews` and the
`api_url` that resolved anyway.

Because it calls nothing, `authenticated: true` means the credentials
resolved, not that the server accepts them: a wrong secret that merely
resolves reports the same as a real one. `--verify` adds a real read
and a `verified` field, and exits 5 when the server rejects the
credential. Its exit codes are 0 whenever
credentials resolve (even with no cached token), 5 when none resolve,
and 2 for a half-supplied flag pair. The
[exit codes guide](exit-codes.md) explains why an entitlement failure
lands on 5 alongside them.

## pgedge controlplane doctor

`pgedge controlplane doctor` shows the resolved connection and probes
each configured server twice, for its version and for whether a
cluster exists. Run it first when a `controlplane` command cannot
connect.

The following table describes the rows it can print:

| Check | What it reports |
|---|---|
| mTLS | Whether a CA certificate or client certificate is configured. |
| Profile | Printed only when the active profile carries a field that will not parse, naming the offending value. |
| Reachable | One row per configured base URL, with the server's version, or `error` when the server could not be asked. |
| Cluster | Whether a cluster exists. Omitted when no server could be asked. |

This CLI supports Control Plane 0.10.0 and above. A reachable server
below that floor turns its Reachable row into a warning naming both
versions, and the exit code is unaffected. A version string the CLI
cannot parse, such as a development build's `dev`, never warns:
unknown is treated as unknown rather than as below.

The Cluster row reads `initialized`, `uninitialized` with both remedies named
(`cluster init`, or `cluster join` for a host joining an existing cluster), or
a warning that the configured servers disagree. A Control Plane can be
reachable, on a supported version, and still have no cluster, in which case
every command except `version`, `doctor`, `config`, `cluster init` and `cluster
join` is refused with a 409.

Under `-o json` the report always carries `base_urls`, `mtls`,
`insecure`, `reachable` and a `servers` array, and adds `problem` only
when the Profile row appears. The cluster verdict is reported as
`cluster_initialized`, once per entry in `servers` and once at the top
level.
<!-- doc-gate: correct-for controlplane cluster_initialized — a key
controlplane doctor builds into its own json map, not a field of any
generated struct -->
A per-server one is absent when that server could not be asked. The
top-level one summarizes only the servers that answered, and is absent
when none answered or when those that answered disagree. Test for the
key rather than its value in both cases.

`--require` turns the report into a gate. It takes `reachable` (some
server answered), `cluster` (the servers that answered agree a
cluster exists) and `supported` (some server answered, and every one
that answered reports a version at or above the floor; a version that
cannot be read fails), comma-separated. doctor prints the same report,
then exits 1 unless every named condition holds. An unknown or empty
condition list is a usage error at exit 2, raised before any server
is probed:

    pgedge controlplane doctor --require reachable,cluster

Treat an absent verdict as unknown, never as "needs init". Running
`cluster init` against a fleet that already has a cluster is the
outcome to avoid, so read the per-server entries before acting on the
summary.

## controlplane config view

`pgedge controlplane config view` prints the connection a
`controlplane` command would use, with flags folded in over the stored
profile. In text output the rows are base-urls, ca-cert, client-cert,
client-key, insecure and timeout. `-o json` and `-o yaml` carry the
same six as `base_urls`, `ca_cert`, `client_cert`, `client_key`,
`insecure_skip_verify` and `timeout`.

Like the doctors, it exits 0 on a profile field it cannot resolve,
adding a `problem` row naming the bad value and showing the default
standing in for it. Refusing to print a profile because one field will
not parse would answer the question with silence, and that field is
usually what you came to see. The key is omitted rather than empty
when there is nothing wrong, so test for the key and not its value.

## Next steps

- The [troubleshooting guide](troubleshooting.md) is organized by exit
  code, for when a command has already failed.
- The [authentication guide](auth-and-profiles.md) covers credential
  resolution and the token cache these rows describe.
- The [updating guide](updating.md) covers the release lookup behind
  the "Latest version" row.
