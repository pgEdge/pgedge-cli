# Authentication and profiles

Two questions decide every call the CLI makes: who you are, and where
it points. One credential pair serves the whole `starfleet` tree: sign
in once with `pgedge starfleet auth login` and the account-level
commands, `byoc` and `managed` all share that connection and its
cached token. The `controlplane` module has no login at all. It
targets `--base-url` directly, with optional mTLS flags for a Control
Plane that requires them.

## How credentials resolve

Three sources, in priority order:

- the `--client-id` and `--client-secret` flags on `pgedge starfleet`.
- the `PGEDGE_CLIENT_ID` and `PGEDGE_CLIENT_SECRET` environment
  variables.
- the active profile: its `starfleet` section in
  `~/.pgedge/cli/config.yaml`, with the secret in the OS keychain.

The two flags must be supplied together. One without the other is a
usage error at exit 2, not a partial override that falls back to
config. The two environment variables follow the same rule. Config is
more forgiving: a `starfleet` section carrying only one of the pair is
simply not a complete credential, so resolution falls through to "no
credentials found" instead of erroring on your behalf.

No other environment variable configures a credential, an API URL or
a profile. The [configuration guide](configuration.md) covers the
precedence rule in full, and the handful of environment variables
that change something else.

## Storing the Client Secret

`pgedge starfleet auth login` saves the client secret in the OS
keychain: Keychain on macOS, the Secret Service on Linux, and
Credential Manager on Windows. The client ID and the API URL go in
`~/.pgedge/cli/config.yaml`. Other commands read the secret back from
the keychain, so you type nothing extra.

Where no keychain can be used, `login` writes the secret to the
config file instead and prints a warning. A Linux server without a
desktop session is the usual case. Run `login` with
`--insecure-storage` to choose the config file on purpose, without
the warning.

A secret that an earlier version wrote to the config file keeps
working. The next `pgedge starfleet auth login` moves it into the
keychain and removes it from the file.

The keychain entry belongs to one profile in one config file. A
profile of the same name in a file named with `--config` has its own
entry.

## Using Environment Variables for Credentials

Set both `PGEDGE_CLIENT_ID` and `PGEDGE_CLIENT_SECRET` to run a
command without a stored profile, as a CI pipeline does. The pair acts
as a separate profile that is never saved:

- The CLI takes no credential or API URL from the config file, and
  writes nothing to it. Other settings in the file, such as the output
  format, still apply.
- The CLI calls the production API unless you pass `--api-url`. A
  profile's `api_url` does not apply.
- The CLI caches no token, so each command exchanges the credential
  for a new one.
- `--profile` alongside the pair is a usage error, exit status 2, because
  the two could name different tenants. Unset the variables to use a
  profile.

Flags still win over the pair. `pgedge starfleet auth login` ignores
the pair, and warns when it is set, because every other command uses
the pair instead of the profile that `login` just saved.

## Profiles

A profile is a named set of connection settings in
`~/.pgedge/cli/config.yaml`: a `starfleet` section with an API URL and
credentials, a `controlplane` section with base URLs, or both. Select one with
`--profile` on any command that reads the config, or persistently
with `pgedge profile use`. The built-in default profile is named
`default`.

`pgedge starfleet auth login --profile <name>` is what creates a profile.
A new name works there before it works anywhere else, because every
other command requires the name to already exist: naming an
unconfigured profile fails at exit 1 with a message listing the
profiles that do exist. That rule covers the `--profile` flag,
`current_profile` in the config file, and the arguments to `pgedge
profile show` and `pgedge profile use` alike. `pgedge controlplane config set
--profile <name>` is the other profile-creating command. The
built-in `default` is the carve-out: the flag, `current_profile` and
`profile show` all accept it with no matching section, even on a
fresh install with no config file, and only `pgedge profile use
default` insists on a configured `default` section.

Three commands manage profiles, and all three honor `-o json` and
`-o yaml`:

- `pgedge profile list` shows every profile, its Starfleet API URL, its
  Control Plane base URLs, and which one is active.
- `pgedge profile show [name]` prints one profile's settings. The
  secret's presence is reported, never its value, in any format.
- `pgedge profile use <name>` points `current_profile` at an
  existing profile and saves the change immediately.

Commands that write the config edit it in place: comments, key order
and unrecognized keys are preserved, so it is safe to keep notes in
the file and safe to mix binary versions.

## A profile is a tenant

A profile carries one Starfleet credential, and an API client belongs
to one tenant, so selecting a profile selects a tenant. The `byoc`
and `managed` sub-trees borrow that same credential rather than
holding one of their own, which has two consequences:

- The tenant's plan gates the `byoc` and `managed` sub-trees, so a
  correctly spelled command against the wrong profile can
  authenticate cleanly and still return nothing.
- The same database name can exist under two tenants, so a name
  identifies a resource only within one profile.

Run `pgedge starfleet doctor` to see which tenant the active profile
resolves to and which plan it holds. The
[account and clients workflow](starfleet/account-and-clients.md)
covers the tenant record and what a plan entitles you to.

## Which identity is active

`pgedge starfleet auth whoami` reads the API and reports the identity
it answers your credential as, one label and value per line: the
credential's own ID, the API client's name, description and record
UUID, then the tenant's name and UUID, the plan, a tenant count on the
rare credential that reaches more than one, and the API URL in effect.
A trial plan reads as the plan name followed by `(trial)`. `-o json`
and `-o yaml` carry the same facts under the same keys.

    pgedge starfleet auth whoami
    pgedge starfleet auth whoami -o json

This is the command to reach for when a correctly spelled command
returns nothing or refuses on the plan, because the plan it prints is
the one doing the gating. It is also how you tell one profile's
credential from another's by name rather than by comparing opaque
IDs.

The two client identifiers are not interchangeable:

- `Client ID` is the credential itself, the value `--client-id`
  takes, and the value `pgedge starfleet client list` shows in its
  AUTH0 ID column.
- `Client record` is the record's UUID, the argument
  `pgedge starfleet client get` takes.

`whoami` is what maps one to the other. Without it, matching a
configured credential to a named client means reading the client list
and comparing that column by eye.

`whoami` calls the API, so it needs a working credential and exits 5
without one, or 2 when only one of `--client-id` and `--client-secret`
is supplied, which is the command being malformed rather than the
credential being absent. That is what separates it from `auth status`,
which
resolves credentials locally and calls nothing: `status` reports what
is configured, `whoami` reports what the server accepts.

For the same reason `whoami` is the wrong tool while authentication
itself is the suspected problem. It caches the token it mints, as
every command that calls the API does, and under `--api-url` the
cached token binds to the host it dialled. `pgedge starfleet doctor`
is the command built for that case: it mints a token without writing
the cache, and it reports rather than fails when no credential
resolves.

## The token cache

A successful login exchanges the credential for a token and caches it
at `~/.pgedge/cli/cache/<profile>-starfleet.json`, shared by the whole starfleet
tree. The cached token is bound to the connection that minted it: the
credential and the API base URL together. Change the profile's
`client_id` or `client_secret`, override either with a flag, or point
`--api-url` somewhere else, and the next command discards the cache
and exchanges a fresh token against the host it is about to call. A
token minted by one endpoint is never sent to another, and the CLI
never silently runs as a previous credential.

`pgedge starfleet auth logout` removes the active profile's cached token
file (and any staging file an interrupted write left beside it), and
deletes the stored `client_id` and `client_secret` from the profile
and the secret from the OS keychain. Other profiles' tokens and
credentials are left alone.

`pgedge starfleet auth status` reports the resolution state without
making a server call, and `pgedge starfleet doctor` reports the whole
connection. Both are safe to run when authentication is the suspected
problem. The [health checks guide](doctor.md) describes every field
and row they print, and the [troubleshooting guide](troubleshooting.md)
covers the failures that send you to them.

## The --config flag

`--config` names the file the profiles live in, and the path is
checked before the command runs: a path that does not exist is exit
1, a path naming a directory is exit 1, and an empty value is exit
2. Only the default `~/.pgedge/cli/config.yaml` may be absent, because a
first run has no config file yet. `--config` cannot create a config
at a new path, not even through the two profile-creating commands.
Create the file, and any parent directory, first. The
[configuration guide](configuration.md) covers the file's layout and
the same rules for `--profile`.

## Next steps

- The [configuration guide](configuration.md) covers the config
  file, precedence and environment variables.
- The [getting started guide](getting-started.md) covers installing
  the CLI and the first login.
- The [CI and automation guide](ci.md) covers supplying credentials
  to unattended runs.
- The [command reference](reference/starfleet.md) lists every starfleet
  command and its flags.
