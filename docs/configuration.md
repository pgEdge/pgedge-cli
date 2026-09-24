# Configuration and environment

The CLI keeps its settings in one file, settles a disagreement
between two sources by a fixed order, and honors a short list of
environment variables. The [authentication guide](auth-and-profiles.md)
covers credential resolution and the token cache, and this page covers
the file itself.

## Precedence

Three sources feed every setting, in one order everywhere:

- a flag on the command line, which wins.
- the active profile in the config file.
- the CLI's own built-in default.

The pgEdge Starfleet credential has one more source: the
`PGEDGE_CLIENT_ID` and `PGEDGE_CLIENT_SECRET` environment variables,
which rank between the flags and the profile. The
[authentication guide](auth-and-profiles.md) covers them. No
environment variable configures an API URL or a profile, because an
exported variable is invisible state. The CLI ignores an exported
`PGEDGE_*` URL, so a setting that appears not to take effect is a
setting written in the wrong place.

## The config file

Configuration lives at `~/.pgedge/cli/config.yaml`. It holds named
profiles, the name of the active one, and output preferences. A
complete file looks like this:

    current_profile: dev
    profiles:
      dev:
        starfleet:
          api_url: https://api.pgedge.com
          client_id: xxxx
      lab:
        controlplane:
          base_urls:
            - https://cp-1.example:8443
            - https://cp-2.example:8443
          ca_cert: /etc/pgedge/ca.pem
          client_cert: /etc/pgedge/client.pem
          client_key: /etc/pgedge/client-key.pem
          timeout: 45s
    output:
      format: text

`starfleet` is the only credential section: one product, one
credential, so the `byoc` and `managed` command groups read it too
rather than carrying sections of their own. `controlplane` needs no
credential, because the Control Plane has no login.

The `base_urls` list is what `pgedge controlplane config set` writes,
and the CLI probes each entry in order and uses the first server that
answers. The CLI also reads a singular `base_url`.

`output.format` sets the default for `-o` across every profile. See
the [output guide](output-and-paging.md) for what the three formats
render.

Commands that write the config edit it in place rather than
regenerating it, so they preserve comments, key order, indentation
and any key the running version does not recognize. It is safe to
keep notes in the file, and safe to let an older binary write a
config a newer one created.

## Naming a profile or a file

`--profile` selects a profile for one invocation and `--config`
names the file the profiles live in. The CLI checks both before the
command runs, and both refuse an empty value at exit 2.

Writing `--profile "$P"` with the variable unset would otherwise
resolve
`current_profile`, and a profile is a tenant, so the silent answer
would be a different account. Omitting the flag is how you ask for
the active profile.

`--config` gets one more check. A path that does not exist is exit 1,
and so is a path naming a directory. Only the default
`~/.pgedge/cli/config.yaml` may be absent, because a first run has
no config file yet, which also means `--config` cannot create a
config at a new path. Create the file, and any parent directory,
first.

Naming a profile that is not configured is an error at exit 1 on
most commands, and a few are exempt from that one rule. `version`,
`llms` and `help` never read the config for profile resolution, and
neither does anything under `completion`, which reads the shell
environment instead. `starfleet auth login` and `controlplane config
set` are how a profile comes to exist, so they accept a name with no
matching section and create it. The exemption covers that one rule
and nothing else: every one of those commands still fails on an
empty `--profile` or `--config`, and on a `--config` path that is
missing or unreadable.

## Environment variables

These variables reach the CLI. Only the first two supply a
credential, and none supplies an API URL or a profile.

| Variable | Effect |
|---|---|
| `PGEDGE_CLIENT_ID` | The pgEdge Starfleet client ID, used only when `PGEDGE_CLIENT_SECRET` is set too. |
| `PGEDGE_CLIENT_SECRET` | The pgEdge Starfleet client secret, used only when `PGEDGE_CLIENT_ID` is set too. |
| `NO_COLOR` | Disables color in text output, the same as `--no-color`. |
| `HOME` | Locates the config file, the token cache and the completion install paths. |
| `XDG_CONFIG_HOME` | Locates the fish completion script and the PowerShell profile fallback, and nothing else. |
| `SHELL` | Drives completion's shell autodetect and the shell row in `pgedge doctor`. |
| `PGEDGE_COMPLETION_DESCRIPTIONS` | Trims descriptions from shell completion suggestions when set to a false value. |
| `PGEDGE_ACTIVE_HELP` | Recognized by the completion protocol but currently inert, because the CLI ships no active-help messages. |
| `HTTPS_PROXY` | Routes outbound HTTPS through a proxy. Read by Go's HTTP transport, not by this CLI. |
| `HTTP_PROXY` | The same for plain HTTP, which in practice means a Control Plane reached over `http://`. |
| `NO_PROXY` | Host patterns to reach directly, bypassing the two above. |
| `COBRA_COMPLETION_DESCRIPTIONS` | The same effect as the `PGEDGE_` spelling above, which takes precedence when both are set. |
| `COBRA_ACTIVE_HELP` | The global spelling of `PGEDGE_ACTIVE_HELP`, and inert for the same reason. |
| `BASH_COMP_DEBUG_FILE` | Names a file the emitted completion scripts write debug output to. Present in all four shells' scripts. |
| `TMPDIR` | Chooses where `self update` stages a downloaded release before verifying and swapping it. |

Nothing in this CLI mentions the proxy trio. Go's default HTTP
transport reads all three,
and every client here ends at that transport, so setting `HTTPS_PROXY`
routes the API calls, the Control Plane calls, `self update`'s release
downloads and its signature verification alike. All three are read in
either case, so the lowercase spellings work as well as the uppercase
ones shown here.

Loopback is the exception, and it is not `NO_PROXY` doing it: Go
never proxies a request to `localhost` or any loopback address,
whatever the three variables say. A Control Plane on `localhost:3000`
is therefore always reached directly.

That the CLI does not read them itself is why they do not appear in
`pgedge doctor` and why a typo in one produces a connection failure
rather than a configuration error.

On Linux, `SSL_CERT_FILE` and `SSL_CERT_DIR` also reach TLS
verification for every HTTPS call, because Go's certificate loading
reads them there. They do nothing on macOS, where verification goes
through the system keychain instead.

`HOME` is the one that matters in a container. Mounting the config
at the path `HOME` resolves to is how an unattended run picks it up
with no flags at all. The [CI guide](ci.md) covers the pattern.

## Next steps

- The [authentication guide](auth-and-profiles.md) covers how
  credentials resolve and how the token cache is bound.
- The [CI and automation guide](ci.md) covers supplying a config to
  an unattended run.
- The [exit codes guide](exit-codes.md) explains why a broken config
  is exit 1 while an empty `--config` is exit 2.
