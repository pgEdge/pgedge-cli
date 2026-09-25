# Getting started

Install the `pgedge` CLI and run a first command. Releases are
published on
[GitHub Releases](https://github.com/pgEdge/pgedge-cli/releases).

## Install the binary

On Linux or macOS, the install script is the quickest route and needs
no Go toolchain:

    curl -fsSL https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/install.sh | sh

The script detects your platform and downloads the newest release. It
verifies the archive's checksum, and the release signature when
`cosign` is installed. It installs to `/usr/local/bin`, or to
`~/.local/bin` when `/usr/local/bin` is not writable, then sets up
shell completion. If the install directory is not on your PATH, the
script prints the `export PATH=...` line to add.

On Windows, download the zip for your architecture (amd64 or arm64)
from the [releases page](https://github.com/pgEdge/pgedge-cli/releases).
The README's "Manual download" section covers verifying an archive.

Confirm the binary is on your PATH and runs before going further:

    pgedge version

Three other routes exist, one of which hands the whole job to your AI
agent. They are under [other ways to install](#other-ways-to-install)
below, along with what each of them needs.

## Your first commands

Sign in once per profile. Login prompts for the client ID and secret,
or takes `--client-id` and `--client-secret` for unattended use. It
caches a token, then saves the secret in the OS keychain and the rest
of the profile in `~/.pgedge/cli/config.yaml`:

    pgedge starfleet auth login --profile dev

Login is what creates the `dev` profile. A new `--profile` name works
here before it works on any other command, and every other command
requires the name to be configured already, or to be the built-in
`default`.

One credential pair serves the whole starfleet module, covering its
account-level commands, byoc and managed, because all three share one
connection and one token.

Check the installation and the connection:

    pgedge doctor
    pgedge starfleet doctor --profile dev

Then run a real command, and pick the output format you want with
`-o text|json|yaml`:

    pgedge starfleet tenant list --profile dev
    pgedge profile list

That is the whole loop: install, log in, run a command. The [output
guide](output-and-paging.md) covers the three formats and the [exit codes
guide](exit-codes.md) covers what a non-zero status means.

## Shell completion

Completion is optional and covers bash, zsh, fish and PowerShell:

    pgedge completion install

That writes a completion script into the directory your shell loads
completions from. `--rc-only` is the other route: it writes no script
and appends one line to your shell's startup file instead, so
completion can never be out of step with the installed binary, at
roughly 10 to 20 ms on a warm cache.

    pgedge completion install --rc-only

Either way, `pgedge completion uninstall` takes it back out.

## Set up your AI agent

Install the pgedge skills into whatever agent you use, since Claude
Code, Cursor, Copilot, Amp, and a dozen others read the same location:

    npx skills add pgEdge/pgedge-cli

This clones the repository itself, so it also works for a developer
who installed the binary via `go install` or a release archive and has
no checkout. Project scope
is the default, which puts the skills in the repository you run it
from so teammates and cloud agents share the setup. Add `--global` to
install for your user instead. The README's "AI-agent skills" section
covers updating, removing, and the manual-copy path for agents without
Node.

## The AI-agent reference

`pgedge llms` prints the machine-readable reference that ships inside
the binary: global flags, profiles, exit codes, and routing tables of
modules and of top-level command pages (`pgedge llms inspect`). `pgedge llms <module>` prints one module's index, which
routes on to one page per resource (`pgedge llms starfleet byoc
cluster`). Point an AI agent at these before anything else, because
they are generated from the live command tree.

Working in a checkout, agents should also read
`skills/pgedge/SKILL.md`, the entry point for the bundled Claude Code
skills.

## Other ways to install

Four routes in all, each buying something different. Two of them need
no Go toolchain, so do not install one just to get a binary. What each
route needs:

| Install path | Also needs |
|---|---|
| Hand it to your AI agent | Node.js, for the agent-skills step |
| Download a release archive | nothing further |
| `go install` from main | Go 1.26 or newer |
| Clone and build | Go 1.26 or newer |

On macOS, `brew install node` and `brew install go` cover those. Node
is for `npx skills add`, not for the CLI, which never needs it, and
the README's manual-copy path installs the skills without it.

To download a release archive, follow the README's "Manual download"
section. To ask a question or report a problem, open a
[GitHub issue](https://github.com/pgEdge/pgedge-cli/issues).

### Hand it to your AI agent

Paste the prompt below into your coding agent (Claude Code, Cursor,
and the like) and it runs the install script, wires up shell
completion, and adds the agent skills for the products you use, asking
you which those are along the way. The only prerequisites are `curl`
and Node.js for the skills step. It stops on any failure rather than
improvising.

<!-- install-prompt: begin -->
<!-- This block is duplicated between README.md and
     docs/getting-started.md: GitHub renders one and MkDocs the other,
     and no include mechanism serves both. TestInstallPromptCopiesMatch
     holds the two byte-for-byte, so edit both, or neither. -->

    Install the pgEdge CLI on this machine and set up its agent skills.
    Follow these steps exactly, in order. If a step fails, stop and show
    me the error. Do not improvise an alternative.

    1. Preflight: check `curl` and `npx` exist. If `curl` is missing,
       stop and tell me it is needed for step 2. If `npx` is missing,
       stop and tell me Node.js is needed for step 4.
    2. Install the CLI (no sudo, ever): run
       `curl -fsSL https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/install.sh | sh`.
       The script verifies the release before installing it. Its
       `Installed pgedge <tag> to <path>` line names the binary's full
       path: use that path for `pgedge` in the rest of these steps. If
       the script warns that the directory is not on my PATH, append the
       `export PATH=...` line it prints to the rc file of my login shell
       (`~/.zshrc` for zsh, `~/.bashrc` for bash, creating it if missing,
       and skipping if the line is already there), and tell me you did.
    3. Shell completion: the script runs `pgedge completion install`.
       If its output says a line still needs adding to my rc file, add
       that line once.
    4. Ask me this question and wait for my answer. Do not guess it:
       "Which pgEdge products do you use: Starfleet, Control Plane, or
       both?" Then install the matching agent skills into the current
       project:
       - starfleet: `npx -y skills add pgEdge/pgedge-cli -s pgedge -s pgedge-starfleet -s pgedge-byoc -s pgedge-managed -y`
       - control plane: `npx -y skills add pgEdge/pgedge-cli -s pgedge -s pgedge-controlplane -y`
       - both: `npx -y skills add pgEdge/pgedge-cli -y`
    5. Verify, and show me the output of each: `pgedge version`,
       `pgedge doctor` (both at the full path from step 2), and
       `npx -y skills list`. A doctor warning about missing credentials
       is expected on a fresh install. The next step after this setup
       is `pgedge starfleet auth login` (starfleet) or reading `pgedge llms controlplane`
       (control plane).
<!-- install-prompt: end -->

### `go install` from main

Install, and later update, with one command:

    go install github.com/pgEdge/pgedge-cli/cmd/pgedge@main

The binary lands in `$(go env GOPATH)/bin`, usually `~/go/bin`, which
must be on your PATH. `pgedge version` reports `dev` on this path,
because version stamping happens in the Makefile build below.

### Clone and build

This route needs the Go toolchain and make, and leaves you the
checkout as well as the binary:

    git clone https://github.com/pgEdge/pgedge-cli
    cd pgedge-cli
    make build
    install pgedge /usr/local/bin/

This stamps `pgedge version` with the real commit and per-module
versions, and leaves you a checkout with the AI-agent skills in
`skills/`. Update with `git pull && make build`.

## Keeping the CLI up to date

A binary you downloaded from a release updates itself:

    pgedge self update

It looks up the newest release on GitHub, verifies the release's
Sigstore signature and the archive's checksum before it touches the
installed binary, and swaps the new one into place. `pgedge self
update --check` reports whether an update exists without downloading
anything, and `pgedge doctor` reports the same answer in its "Latest
version" row.

The [updating guide](updating.md) covers the rest: the two-rung GitHub
lookup, the exit codes and deadlines, where the CLI caches the Sigstore trust
root, the three installs the command refuses, and what happens to your
completion scripts after a swap.

## CI and containers

Set `PGEDGE_CLIENT_ID` and `PGEDGE_CLIENT_SECRET` from your
pipeline's secret store, and every invocation uses them with nothing
written to disk. The [CI and automation guide](ci.md) covers the
pair in CI, Docker and Kubernetes, skipping prompts with `--force`,
scripting against the exit-code contract, and two complete pipelines
you can copy.

## Next steps

- The [authentication guide](auth-and-profiles.md) covers how
  credentials resolve, what a profile holds, and how the token cache
  is bound to the connection that minted it.
- The connect an application guides for
  [Managed](managed/connect-an-application.md) and
  [BYOC](byoc/connect-an-application.md) cover getting a database's
  host, port and credentials out of the CLI and into an application.
- The [CI and automation guide](ci.md) covers unattended runs, and
  carries worked GitHub Actions and GitLab CI pipelines.
- The [telemetry and privacy guide](telemetry-and-privacy.md) covers
  what the binary contacts over the network and what it writes to
  local disk.
