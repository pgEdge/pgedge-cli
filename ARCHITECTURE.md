# Architecture

One binary, one configuration file, one UX across the pgEdge product
suite: `pgedge <module> <resource> <verb>`. This document describes how
the CLI is put together and why each part is shaped the way it is.

## The shape

`cmd/pgedge/main.go` is the whole of the launcher: it builds the shared
runtime, registers each module, materialises their command trees under
the root command from `internal/cli`, and maps the resulting error onto
an exit code. Its registry is the complete list of top-level product
commands, so it is the first place to look when asking what the binary
can do.

Two modules ship today.

- **`starfleet`** (`internal/starfleet/`) is one product wearing three
  API namespaces. Its root owns authentication and the connection
  (`pgedge starfleet auth login`, `--api-url`, `--client-id`,
  `--client-secret`, `pgedge starfleet doctor`) along with the
  account-level resources `tenant`, `client`, `invite` and
  `membership`. It mounts two sub-trees, `byoc` and `managed`, which
  are separate wire namespaces with colliding resource names:
  `database` exists in both and means different things.
- **`controlplane`** (`internal/controlplane/`) drives a self-hosted
  pgEdge Control Plane. It is genuinely independent, with its own
  connection model (one or more base URLs, optional mutual TLS), its
  own wire format, and no import of anything under
  `internal/starfleet/`.

The rest of the tree divides along the same line, platform above,
modules below:

| Path | Role |
|------|------|
| `cmd/pgedge/` | Entry point and module registration |
| `cmd/gendocs/` | Generates the reference from the command tree |
| `cmd/vendorspec/` | Captures the published public OpenAPI specs |
| `internal/module/` | The module contract: `Module` and `Runtime` |
| `internal/cli/` | Root command, global flags, exit codes, top commands |
| `internal/config/` | The config file, profiles, per-module sections |
| `internal/auth/` | Credential resolution and the token cache |
| `internal/output/` | The shared renderer: text, JSON, YAML |
| `internal/{dryrun,httplog,errbody}/` | Transports below the API clients |
| `internal/apidefaults/` | The default base URL, module-free |
| `internal/docgen/` | Renders and checks a command's reference block |
| `internal/selfupdate/` | The parts behind `pgedge self update` |
| `openapi/` | Vendored specs and codegen configuration |

Inside each module, `cmd/` is the Cobra tree and `llms.txt` is that
module's embedded agent-facing reference. The generated OpenAPI client
lives in an `api/` directory that is never hand-edited: one for
`controlplane` (`internal/controlplane/api/`), and one per namespace
for `starfleet` (`internal/starfleet/{account,byoc,managed}/api/`),
because each namespace is its own vendored spec.

## The module contract

`internal/module/module.go` defines the whole of what a module is:

    type Module interface {
        Name() string
        Short() string
        Command(rt *Runtime) (*cobra.Command, error)
        Describe() ModuleInfo
        Reference() []byte
    }

`Command` takes the runtime as an argument rather than reading a
global, because a module has to be constructible more than once: the
build-time checks assemble the same tree the binary does.

`Describe` returns a `ModuleInfo`: name, short description, version, a
flag for whether the module ships a reference, and a reserved
`ContractVersion` that stays empty, since a contract version has no job
while every module compiles in. It is the seam an out-of-process module
would satisfy with a `__describe` subcommand emitting that shape as
JSON.

`Reference` puts each module's reference document on the interface, so
a module's tree and its documentation arrive together and a module
cannot be registered while quietly forgetting to document itself. The
optional `SubReferenced` extension adds the one level of nesting
`starfleet` needs, so `byoc` and `managed` each serve their own scope.

`Runtime` is everything a module receives from the platform: config,
resolved profile, renderer, standard streams, verbosity, and the
dry-run ledger. Modules never reach into globals, which is what lets
one module run from the binary, from a harness, and one day from a
dispatch path handing the same data across a process boundary.
Connection flags deliberately stay outside `Runtime`, on the module's
own root command: how a product is reached is the module's business,
while how a command runs is the platform's.

Each module reports its own version, injected at build time from
`versions.env` (`STARFLEET_VERSION`, `CONTROLPLANE_VERSION`), on its
own cadence to track the upstream service it talks to. The launcher's
version is the release tag.

## What holds the boundaries

These are rules applied to the package graph, the command tree and the
shipped documents, not conventions anyone has to remember.

- **The platform never imports a module.** A launcher that depends on a
  module drags that module's generated client into every other module's
  closure, and turns a later split into a rewrite rather than a move.
- **Only the root command populates the runtime.** A second population
  point drifts from the first, and the drift surfaces as a nil
  dereference at run time rather than as a compile error.
- **Every registered module has a version entry.** The build stamps
  that value in, so a missing line ships a module reporting `dev`.
- **Every module ships a reference, and every reference matches the
  live tree.** Command blocks are generated from Cobra, so a documented
  flag that no longer exists is drift a generator can see.
- **Hand-written prose names only things that exist.** Flags, fields
  and status values quoted in the references and the skills are checked
  against the structures and the tree they describe.
- **The command tree obeys the UX standard.** Singular resource names
  with plural aliases, kebab-case flags, help text on every leaf,
  `--dry-run` on every mutating verb, and one exit-code vocabulary
  across modules.
- **A success carrying no body writes nothing to stdout.** The
  acknowledgement is exit 0 and a sentence on stderr, so a caller
  parsing stdout never sees a fabricated response object.
- **The vendored specs describe only the public API.** They are
  captured from the published public contracts rather than
  hand-edited, and a build-time scan of the checked-in specs fails on
  a visibility marker a hand edit reintroduced.

Every one of these fails the build. Most derive the set they check from
the command tree or the filesystem; the few that key on names (which
verbs mutate, which directories are platform) keep that list beside
the rule that uses it, so extending one means extending the other.

One convention is not build-checked and rests on review: every
destructive verb goes through the shared confirmation prompt, with
`--force` to skip it.

## Configuration and state on disk

`~/.pgedge/` is shared with other pgEdge tools. The CLI owns exactly
one subdirectory of it, `~/.pgedge/cli/`, and writes nothing at the
root: a file there is a file another tool may want.

- `~/.pgedge/cli/config.yaml`, mode 0600, holds named profiles and the
  current profile. A profile carries per-module sections, `starfleet:`
  and `controlplane:`, and one credential, so a profile is a tenant.
- `~/.pgedge/cli/cache/<profile>-<module>.json` holds a cached access
  token, written through a staging file in the same directory and
  renamed into place, so an interrupted write leaves nothing truncated.
- `~/.pgedge/cli/cache/sigstore` holds the trust root `self update`
  verifies against.

Precedence for any setting is flag, then profile, then built-in
default. `internal/config` resolves it, and `internal/apidefaults` owns
the default base URL so `internal/cli` can report an effective URL
without importing a module.

A cached token is bound to the connection that minted it: the base URL
and the credential are hashed into a fingerprint stored beside the
token. Swapping a key inside a profile therefore mints a fresh token
rather than silently continuing to act as the previous tenant.

## Self update

`pgedge self update` replaces the running binary in place.
`internal/cli/self.go` drives it, `internal/selfupdate/` supplies the
parts.

- **A transport ladder, not a single path.** Releases and assets are
  fetched over unauthenticated HTTPS from GitHub first, falling back to
  executing `gh`. The unauthenticated rung needs no credential and
  starts working, with no code change, the day the repository is
  public; the `gh` rung makes the command usable before then.
- **Verification before anything is written.** The release's checksum
  file is checked against its Sigstore signature, and the archive
  against the entry whose filename field matches exactly. Artifacts are
  signed keylessly in CI, so there is no long-lived key to protect. The
  trust root is refreshed over TUF and cached under `~/.pgedge/cli/`
  rather than the home directory sigstore-go picks by default.
- **Staging beside the target.** The new binary is written to
  `<target>.new` in the target's own directory, then renamed onto the
  target. Same directory means same filesystem, which is what makes the
  rename atomic and, on Linux, possible at all: a memory-backed `/tmp`
  with the binary in `~/.local/bin` is the modal install, and a rename
  across that boundary fails. Windows will not replace a running
  executable, so there the old binary is renamed aside and the next run
  cleans it up.
- **Refusals where the binary is not ours to replace.** A Homebrew
  install and a copy inside a git working tree are each owned by
  something else, so the command declines and names the right tool
  (`brew upgrade`, `make build`).
- **Completion scripts are refreshed afterwards.** Installed scripts
  are regenerated by running the new binary, since this process's own
  tree is the release being replaced. A failure warns rather than
  failing the command: the swap has already succeeded.

`self update` is the one command that prints a body on success, because
the report of what changed is the result being asked for.
