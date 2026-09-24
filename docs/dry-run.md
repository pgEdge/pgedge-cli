# Dry runs

Every command that writes to an API accepts `--dry-run`. It runs each
client-side check the CLI has, stops before the write leaves, and
reports the request it would have sent. This page covers what that
proves, what it does not, and which commands do not offer it.

## What a dry run reports

The report is the command's result, so it goes to stdout in the
format `-o` asked for, even on a command that would otherwise print
nothing. In text it has three parts: a `checks passed:` list naming
each check that ran, a `would send:` block carrying the method, the
URL and the request body, and a closing sentence saying nothing was
written and the server was not consulted.

This previews a service deployment without deploying it:

    pgedge starfleet managed database mcp deploy "$DB_ID" \
        --allow-writes --dry-run

A command with no checks of its own says so rather than printing an
empty list.

Under `-o json` and `-o yaml` the report is an object carrying
`dry_run`, `server_validated`, a `checks_passed` array, and a
`request` object with `method`, `url` and the body. The
`server_validated` field is always false, and says in a field what
the text report's closing sentence says in a sentence.

The CLI masks secret values in the previewed body, and the
`Authorization` header too when `--debug` adds request headers. A
previewed body stops at about 8 KB (8,192 characters, cut on a
character boundary) and says so when it clips, so the preview of a
large spec is truncated rather than complete.

## What a dry run proves

A dry run is a client dry run. The CLI assembles the request and
shows it and never sends it, so no server-side validation happens
anywhere. A clean dry run means every check this CLI can perform
passed, not that the API has accepted anything.

Requests that only read still go to the API. That is deliberate,
because the checks worth running are the ones that need a read: a
`deploy` and an `update` cannot tell each other apart without
fetching the resource first. So `--dry-run` needs working
credentials and a reachable API, and it can fail for authentication
or network reasons.

The exit codes a dry run produces are the real run's: 0 when clean,
2 for a bad flag, 4 for an unknown resource, 5 for an entitlement
refusal on a read it made. What it cannot produce is a code only the
server would have reached, so `cmd --dry-run && cmd` is not a gate
on the real run's exit code. An input the API rejects and the CLI
does not check passes the dry run and fails the real call. Treat a
clean dry run as "the checks listed passed", and read the list. The
[exit codes guide](exit-codes.md) covers the codes themselves.

When a check refuses the command, the dry run prints the checks that
had already passed to stderr before the error, so it is clear how
far it got. That partial report is text even under `-o json`,
because there is no complete report to serialize, and it disappears
altogether when the first check is the one that refused, since a
report of nothing adds nothing to the error.

A destructive command's dry run never prompts for confirmation. It
records that the real run would.

## Which commands take the flag

The CLI decides "writes" per operation rather than per HTTP method.
Control Plane declares two state-changing operations as `GET`,
`cluster init` and `task cancel`, and a dry run stops those exactly
as it stops a POST.

The CLI registers `--dry-run` only on commands that write, so on a
read-only command it is an unknown flag at exit 2. Eight commands
change something without writing to a pgEdge API and do not take it
either:

| Command | Why it is exempt |
|---|---|
| `pgedge profile use` | Writes the config file only. |
| `pgedge controlplane config set` | Writes the config file only. |
| `pgedge starfleet auth login` | Writes the config file and the token cache. |
| `pgedge starfleet auth logout` | Removes the token cache and the stored credentials. |
| `pgedge completion install` | Writes a completion script or a shell startup line. |
| `pgedge completion uninstall` | Removes whichever of those is in place. |
| `pgedge controlplane database init` | Prints a starter spec and sends no request at all. |
| `pgedge self update` | Swaps the local binary and writes to no pgEdge API. |

Everything the first seven touch is local and readable, so a preview
would add nothing that opening the file does not. `self update`
carries its own preview instead: `pgedge self update --check`
reports whether a newer release exists and downloads nothing.

## The flag's value

`--dry-run` is a string flag whose value may be omitted, so
`--dry-run` and `--dry-run=checks` are the same thing, and `checks`
is the only accepted value. The CLI rejects `--dry-run=server` by
name, with a message saying neither pgEdge Starfleet nor the Control
Plane exposes a validate endpoint.

## What a byoc cluster create checks

Which checks a command has is a property of that command, and the
report lists the ones it ran. `starfleet byoc cluster create`
provisions real cloud infrastructure and takes minutes, and its dry
run checks some of that command's inputs and leaves the rest alone:

| Input | Checked |
|---|---|
| `--node-location` | Yes, against the contract's enum: only public and private are accepted. |
| `--cloud-account-id` | Yes. It must parse as a UUID and must name an account that exists. |
| `--firewall-rule` | Yes, the rule name, which must be one of http, https, postgres or ssh. |
| `--volume-size` | Yes, in both the flag and the `--node` key: 1 GB or more. |
| `--node` and `--network` keys | Yes, by name, so a typo is exit 2 rather than a silently dropped setting. |
| `--name` | No. byoc validates a database name client-side and a cluster name not at all. |
| `--regions` | No, and it cannot be: no endpoint enumerates the regions. |
| `--instance-type` | No. The accepted values are the cloud provider's and no endpoint enumerates them. |

The CLI checks one subnet rule too. A `--node-location private`
cluster whose `--network` carries `public-subnets` but no
`private-subnets` is AWS-shaped or Azure-shaped, and the CLI refuses
it before any API call. A private cluster with no `--network` at
all, or one using the GCP `subnets` key, passes untouched, because
the server fills the defaults.

The existence check on `--cloud-account-id` costs a read, so its
answer depends on how far the CLI gets: a malformed identifier is
exit 2, an identifier naming nothing is exit 4, and no configured
credentials is exit 5, because the CLI cannot ask.

`cluster update` adds one check `create` has no use for. It reads
the cluster and refuses a `--regions` change that would strand a
region still holding a node or a network.

## Next steps

- The [CI and automation guide](ci.md) covers the rest of running
  the CLI unattended.
- The [exit codes guide](exit-codes.md) explains every code a dry
  run can produce.
- The [output guide](output-and-paging.md) covers the formats the
  report renders in.
