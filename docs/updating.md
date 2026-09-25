# Updating the CLI

A binary downloaded from a release updates itself. `pgedge self
update` finds the newest release on GitHub, verifies its signature and
checksum, and swaps the new binary into place over the old one.

## Running an update

The command takes no arguments, and prints a report of what it
replaced:

    pgedge self update

It resolves the running binary first, then fetches the release list,
prompts `update <old> -> <new>?`, downloads the archive and three
verification files, checks the signature and the checksum, extracts
the binary and renames it over the installed one. Progress goes to
`stderr`, and the report is the command's `stdout` body, a deliberate
exception to the rule that a success carrying no response body prints
nothing.

Run it with the same privileges you used to install. A binary in
`/usr/local/bin` needs whatever write access putting it there needed.

Three flags change what happens:

- `--check` reports whether an update exists and downloads nothing.
- `--version <tag>` updates to a named release instead of the newest.
- `--force` skips the confirmation prompt.

`pgedge doctor` reports the same answer as `--check` in its "Latest
version" row, so a routine health check also tells you when you are
behind.

## Where releases come from

Releases are fetched through a two-rung ladder: the unauthenticated
GitHub REST API first, then the `gh` CLI. The first rung needs no
GitHub account or sign-in. The command tries `gh` only when that
first rung fails, and `gh` then uses your own signed-in session.

The following table shows what each failure exits with, using the
codes the [exit codes guide](exit-codes.md) defines:

| Condition | Exit code |
|---|---|
| `gh` is installed but its session is not authenticated | 5 |
| `gh` is missing, or any other `gh` failure | 1 |
| No route to GitHub at all | 1 |
| A deadline expires | 3 |

The deadlines behind that last row differ by step: 30 seconds to list
releases, 5 seconds for the check `pgedge doctor` runs, 10 minutes for
a download, and 30 seconds apiece for the post-swap version probe and
for each completion script regenerated after it. An offline machine
reports a network error at exit 1 even though `gh` itself blames its
token when offline. The HTTP rung failing before any status came back
is what tells the two apart.

`--verbose` and `--debug` reach both rungs. The HTTP rung logs its
requests and responses (a release archive's body is never dumped, even
under `--debug`), and the `gh` rung logs each command it runs with its
exit status, so a misbehaving run shows which rung it was on.

## Verification

Every release ships a `checksums.txt` and a Sigstore bundle,
`checksums.txt.sigstore.json`, which holds its signature, the signing
certificate and the transparency-log entry. `self update` downloads
both and verifies the bundle against the public Sigstore trust root.
Then it verifies the archive's checksum against the signed list. Only
then does it extract and swap.

Verifying refreshes the Sigstore trust root over the network and
caches it under `~/.pgedge/cli/cache/sigstore`. Besides that cache, a
temporary download directory and the installed binary itself, the only
other paths the command writes are the completion scripts it refreshes
after the swap, described below.

## Installs it will not touch

Three installs belong to another tool, so `self update` refuses them
before any network call and names the tool to use instead:

- A Homebrew install. Run `brew upgrade pgedge` instead.
- An install by npm or another Node package manager. Run `npm
  install -g @pgedge/cli@latest` instead, or `@beta` for a
  pre-release.
- A binary inside a git working tree, which is what `make build`
  produces. Run `git pull && make build` instead.

No refusal takes an override, because forcing one would leave
the CLI managing a binary something else owns. `--check` is the one
path a refusal does not stop: it swaps nothing, so it still reports
the answer and exits 0, repeating the refusal on `stderr` as a note.

A binary from `go install` is not refused, but reports `dev` as its
version, so `self update` always sees a newer release than the one you
have.

## After the swap

The report renders as a table with COMPONENT, PREVIOUS and NEW
columns: one row for the launcher, then one per built-in module.
`-o json` and `-o yaml` carry the same data as `previous`, `new` and a
`modules` array. Under `--check` they also carry `update_available`,
which is `true` when a newer release exists and `false` when the
binary is already current.

The new binary regenerates any completion script `pgedge completion
install` wrote, so Tab completion picks up new commands and flags
without another step. Four paths under your home directory are checked
and rewritten when a script is found there:

- `~/.local/share/bash-completion/completions/pgedge` for bash.
- `~/.zsh/completions/_pgedge` for zsh.
- `pgedge.fish` under fish's completions directory, which follows
  `XDG_CONFIG_HOME` and otherwise sits under `~/.config`.
- the pgEdge script in your PowerShell profile directory.

A shell with no script installed is left alone and nothing is created,
and a script that cannot be refreshed is a warning rather than a
failed update. The `--rc-only` route needs nothing regenerated: it
asks the installed binary for its completions at every shell start.

## Next steps

- The [getting started guide](getting-started.md) covers the install
  routes this command updates.
- The [exit codes guide](exit-codes.md) explains the codes this
  command shares with the rest of the CLI, and the
  [troubleshooting guide](troubleshooting.md) is organized by them.
