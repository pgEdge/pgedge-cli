# Account, tenant and API clients

The account-level half of the `starfleet` module covers the tenant
record a credential authenticates as, the API clients that credential
comes from, and the plan that decides which parts of the CLI work.
None of these commands touch a database.

Every profile carries one client credential, so a profile is a
tenant. The [authentication guide](../auth-and-profiles.md) covers
that rule and how the credential resolves.

## The tenant record

Three commands read and rename the tenant: `tenant list`, `tenant
get <tenant_id>` and `tenant update <tenant_id>`. There is no create
command and no delete command, because the API exposes only read and
rename operations on a tenant.

`list` and `get` print the same six columns: ID, NAME, PLAN, TRIAL,
DOMAIN and CREATED. Three fields have no column and are readable only
under `-o json` or `-o yaml`: `external_id`, `plan_expires_at` and
`updated_at`. The tenant table therefore shows CREATED without a
matching UPDATED, unlike the client table below. An unset optional
field is absent from both formats rather than rendered as null, and
a YAML key always matches the JSON key.

Renaming takes a single flag, because the request body carries a
single field:

    pgedge starfleet tenant update <tenant-id> --name acme-renamed

Running `update` with no `--name` exits 2 with "nothing to update"
and sends no request at all. Every ID argument on these commands is a
full UUID, and a malformed one fails before the CLI sends anything.

## Plan entitlement

The server enforces the tenant's plan, and the refusal arrives in two
different shapes:

| Shape | What you see | Exit code |
|---|---|---|
| A plan-gated command | An error naming the plan as the reason | 5 |
| A command that is not plan-gated | A successful, empty list | 0 |

Commands such as `byoc cloud-account list` and `byoc backup-store
list` are of the first kind. The API answers `plan does not allow
...`, and the CLI reports it at exit 5 alongside the other
authentication failures, substituting its own message and quoting the
server's in a `(server said (400): ...)` tail. Commands such as `byoc
cluster list` and `byoc ssh-key list` are of the second kind. They
answer with an empty array whatever the plan, so an empty result from
them proves nothing about entitlement.

When a list comes back empty and you expected rows, read the plan
before concluding the resources are gone:

    pgedge starfleet doctor -o json

The `tenant` object names the tenant and its `plan`, which the CLI
omits from the JSON and renders as "unknown" in the table when the
record carries no plan at all. Reading the tenant is an authenticated
call, so a resolved tenant also proves the server accepts the
credential. The [exit codes guide](../exit-codes.md) carries the full
contract, and the
[BYOC troubleshooting page](../byoc/troubleshooting.md) keys both
shapes to the symptom you arrived with.

## API clients

An API client is the machine credential the CLI signs in with. Five
commands manage them: `client list`, `client get`, `client create`,
`client update` and `client delete`.

`list` and `get` print six columns: ID, NAME, DESCRIPTION, AUTH0 ID,
CREATED and UPDATED. Timestamp columns show the date alone, so read
`-o json` for the full value.

Creating a client needs a name and a description, because the API
requires both:

    pgedge starfleet client create --name ci \
        --description "CI runner"

`create` is the only client command that renders the secret, and the
API returns that secret exactly once. Where the secret lands depends
on the output format:

- `-o text` writes the secret alone to `stdout`, and every label,
  including the client ID, to `stderr`.
- `-o json` and `-o yaml` write the whole client object to `stdout`,
  keyed `auth0_secret`.

`list`, `get` and `update` cannot render a secret in any format,
because the read and update responses carry no such field. A create
that comes back without one warns on `stderr` rather than printing
null, and a success with no parseable body fails outright: the server
minted that secret and it is already unreachable, so the remedy is to
delete that client and create another.

`update` changes a name or a description, and sends only the flags
you pass, so an omitted flag leaves that field alone. Passing neither
flag exits 2 with "nothing to update" and sends no request.

`delete` is destructive, so it prompts unless you pass `--force`. On
a non-interactive `stdin` without `--force` it exits 2 and issues no
request, so a script fails loudly instead of hanging. Anything still
authenticating with that client stops working immediately, including
this CLI when you delete the client the active profile uses.

## Rotating a client secret

There is no rotate endpoint, so replacing a secret always means
creating the new client, proving it works, and only then deleting the
old one. The [rotate credentials workflow](rotate-credentials.md)
walks those steps in order.

## Credential verification

Two commands answer that question, and they answer different halves
of it. `pgedge starfleet auth status` reports resolution only. It
calls no endpoint, so `authenticated: true` means the credentials
resolved rather than that the server accepts them, and a wrong secret
that resolves reports the same thing. It exits 0 whenever credentials
resolve, 5 when none do, and 2 for a half-supplied
`--client-id`/`--client-secret` pair, which a fresh login will not
fix. `pgedge starfleet doctor` makes the call, and its Tenant row is
the part that proves the credential works. The health checks guide
covers every row.

There is no `pgedge starfleet user` command. The account API does
expose a current-user endpoint, but a client-credentials token
carries a tenant and never a user, so the server rejects that call
whatever the credentials are.

## Next steps

- The [team onboarding workflow](team-onboarding.md) covers invites,
  memberships and owner semantics.
- The [rotate credentials workflow](rotate-credentials.md) covers
  replacing a client secret without an outage.
- The [command reference](../reference/starfleet.md) lists every
  account-level command and its flags.
