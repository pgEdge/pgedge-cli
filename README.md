<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/img/pgedge-labs-dark.svg">
    <img alt="pgEdge Labs" src="docs/img/pgedge-labs-light.svg" width="320">
  </picture>
</div>

# pgEdge CLI

![CI](https://github.com/pgEdge/pgedge-cli/actions/workflows/ci.yml/badge.svg)

`pgedge` is the unified command-line interface for the pgEdge
product suite: `pgedge <module> <resource> <verb>`.

Modules: `starfleet` (Starfleet authentication and account
resources, plus the nested `byoc` and `managed` infrastructure
sub-trees) and `controlplane` (Control Plane).

## Install

Releases are published on
[GitHub Releases](https://github.com/pgEdge/pgedge-cli/releases). To
ask a question or report a problem, open a
[GitHub issue](https://github.com/pgEdge/pgedge-cli/issues).
[docs/getting-started.md](docs/getting-started.md) covers first steps.

### Hand the install to an AI agent

Paste the prompt below into your coding agent (Claude Code, Cursor,
and the like) and it installs the CLI, wires up shell completion,
and adds the agent skills for the products you use, asking you
which those are along the way. It needs `curl`, plus Node.js for the
skills step, and it stops on any failure rather than improvising.

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

### Install script (Linux, macOS)

Downloads the latest release for your platform, verifies its
checksum (and its cosign signature when `cosign` is installed),
and installs to `/usr/local/bin` (falling back to
`~/.local/bin`):

    curl -fsSL https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/install.sh | sh

To install one release instead of the newest, set `PGEDGE_VERSION`
to its tag:

    curl -fsSL https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/install.sh | PGEDGE_VERSION=<release-tag> sh

### Homebrew (macOS, Linux)

One command:

    brew install pgEdge/tap/pgedge

> The tap's first cask is published with the next release.

### go install

Go builds it from source at the main branch:

    go install github.com/pgEdge/pgedge-cli/cmd/pgedge@main

> Put a release tag in place of `@main` to build that release.
> A binary installed this way reports its version as `dev`.

### Manual download

Grab the archive for your platform from the
[releases page](https://github.com/pgEdge/pgedge-cli/releases),
then verify and extract it:

    # verify integrity
    sha256sum -c checksums.txt --ignore-missing

    # verify authenticity (optional, requires cosign)
    cosign verify-blob \
        --bundle checksums.txt.sigstore.json \
        --certificate-identity-regexp \
            '^https://github\.com/pgEdge/pgedge-cli/\.github/workflows/release\.yml@refs/tags/' \
        --certificate-oidc-issuer \
            'https://token.actions.githubusercontent.com' \
        checksums.txt

    tar -xzf pgedge_*.tar.gz
    install pgedge /usr/local/bin/

Builds are published for `linux` and `darwin` (amd64, arm64) and
`windows` (amd64, arm64). Archive names are lower-case and use Go's
architecture spelling, which `uname` does not reliably match: the
mapping is irregular across platforms and shells, agreeing on some and
not others. Take the name from the releases page rather than
assembling one from `uname` output. Windows archives are `.zip`, and
everything else is `.tar.gz`.

### Build from source

Build the binary in a checkout and run it:

    make build
    ./pgedge --help

### Updating

A downloaded release binary updates itself with `pgedge self update`.
See the Updating section below and
[docs/updating.md](docs/updating.md). A Homebrew install or a binary
inside a git checkout is updated by the tool that owns it. `pgedge
version` prints what you are running.

## Updating

A downloaded release binary updates itself:

    pgedge self update           # verify the newest release and swap it in
    pgedge self update --check   # just say whether one exists

The command checks the download against the release's Sigstore
signature and checksum before that download touches the installed
binary, and it regenerates an installed completion script afterward.
It leaves a Homebrew install or a binary inside a git checkout alone
and names the command to run instead (`brew upgrade pgedge`,
`make build`). `pgedge doctor` also reports the latest release.
Details in [Updating the CLI](docs/updating.md).

## Shell completion

Enable Tab completion for commands, subcommands, and flags:

    pgedge completion install

This detects your shell (bash, zsh, fish or PowerShell) and sets
completion up one of two ways. The install script runs it
automatically, and Homebrew will too once the tap (see above) is
published.

- **File install (default).** Writes a completion script into the
  directory your shell loads completions from, and prints any
  remaining step (a zsh `fpath` line, a PowerShell profile line).
  Your shell loads the script like any other completion file.
  `pgedge self update` regenerates the script, so it keeps up with
  the binary.
- **rc line (`--rc-only`).** Appends one line to your shell's startup
  file in place of the script. The installed binary generates the
  completions at every shell start, so they always match it.

The second route:

    pgedge completion install --rc-only

The lines it adds, if you would rather add one by hand:

    # ~/.bashrc
    eval "$(pgedge completion bash)"

    # ~/.zshrc
    eval "$(pgedge completion zsh)"

    # ~/.config/fish/config.fish
    pgedge completion fish | source

    # PowerShell $PROFILE
    pgedge completion powershell | Out-String | Invoke-Expression

`pgedge completion uninstall` removes whichever of the two is in
place.

Any shell's script can also be printed on its own for manual wiring:

    pgedge completion fish
    pgedge completion powershell

Release archives also ship a `completions/` directory with a script
per shell, including `pgedge.ps1` for Windows zip users who prefer
to wire it up manually.

## AI-agent skills

pgEdge ships agent skills (SKILL.md, the Agent Skills open standard) in
the `skills/` directory of this repository:

- `skills/pgedge`: pgedge CLI orientation and module choice
- `skills/pgedge-starfleet`: Starfleet authentication, invites,
  and team memberships
- `skills/pgedge-byoc`: pgEdge BYOC resources
- `skills/pgedge-managed`: pgEdge-hosted managed databases
- `skills/pgedge-controlplane`: pgEdge Control Plane resources

The `pgedge` binary does not install them, because skills are a layer
above the CLI.

One command installs all five and detects your agent:

    npx skills add pgEdge/pgedge-cli

It writes to `.agents/skills/`, the shared location supporting agents
read, and symlinks it for those with their own convention, so Claude
Code, Cursor, Copilot, Amp, Antigravity and a dozen others pick them
up from a single install. Project scope is the default, putting them
in the repository so teammates and cloud agents share the setup. Add
`--global` for a per-user install. `npx skills list`, `update` and
`remove` manage them afterward, and `skills-lock.json` pins each one
by content hash.

For a single skill:

    npx skills add pgEdge/pgedge-cli --skill pgedge-managed

Manual copy (any agent, no Node required):

    git clone https://github.com/pgEdge/pgedge-cli
    mkdir -p .agents/skills
    cp -r pgedge-cli/skills/* .agents/skills/

Use `~/.claude/skills/` in place of `.agents/skills/` to install for
Claude Code alone.

## Quickstart

Print the command tree, then sign in:

    pgedge --help
    pgedge starfleet auth login

## Documentation

The `docs/` tree carries the guides and the generated command
reference: [getting started](docs/getting-started.md),
[authentication and profiles](docs/auth-and-profiles.md),
[CI and automation](docs/ci.md),
[troubleshooting](docs/troubleshooting.md),
[versions, uninstall and support](docs/support-versioning-and-uninstall.md),
per-module guides under [docs/starfleet/](docs/starfleet/),
[docs/managed/](docs/managed/), [docs/byoc/](docs/byoc/) and
[docs/controlplane/](docs/controlplane/), and per-module reference
pages in [docs/reference/](docs/reference/). AI agents should run
`pgedge llms` instead, the same reference embedded in the binary.

## Versioning

`pgedge version` reports the launcher version plus one per module, and
what its first line reads depends on how the binary was built. The
[versions, uninstall and support](docs/support-versioning-and-uninstall.md)
page has the detail.

## Uninstall

`pgedge completion uninstall` removes the shell completion script. The
[versions, uninstall and support](docs/support-versioning-and-uninstall.md)
page lists everything else the CLI put on the machine and where.

## Development

Build, test and lint the checkout:

    make build test lint

See CONTRIBUTING.md.
