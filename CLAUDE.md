# pgedge-cli

Unified `pgedge` CLI for the pgEdge product suite. One binary, one
config, one UX: `pgedge <module> <resource> <verb>`. Two modules:
`starfleet` (Starfleet auth and account resources, with the nested
`byoc` and `managed` infrastructure sub-trees) and `controlplane` (a
self-hosted Control Plane).

> **AI agents — read the bundled reference first.** Run `pgedge llms`
> for the index (global flags, config, profiles, env vars, exit codes,
> and a page per top-level command group, such as `pgedge llms
> inspect`), then `pgedge llms <module>` for the module you are
> working in. Also read `skills/pgedge/SKILL.md`. Do not improvise
> commands from `--help` output or fall back to raw curl.
>
> The command reference is **generated from the cobra tree**. Editing
> text between `<!-- BEGIN GENERATED: ... -->` and `<!-- END GENERATED
> -->` is pointless — `make docs` overwrites it. Change the cobra
> command instead, then run `make docs`. Prose outside the markers is
> hand-written and yours to edit.
>
> Two build gates enforce this: `TestReferenceDocsConform` fails if any
> reference document is out of sync with the live command tree, and
> `TestCommandTreeConformance` fails on UX-standard violations (plural
> resource names, missing help text, non-kebab flags). Keep both green
> alongside any command change.
>
> Two more gate the examples themselves, which nothing read until
> #297:
>
> - `TestShippedRecipesDoNotDiscardAPgedgeStatus` fails when a
>   `pgedge` call's exit status is swallowed by a pipeline, a
>   conditional with no stop, a capture (`$( )`, or a backtick in
>   assignment position) or an `&&`.
> - `TestShippedProceduresDoNotWriteOffAnUncheckedRead` fails when a
>   procedure branches on a read being empty and writes on that
>   branch without testing the read's own status — in prose, which no
>   shell parser can see.
>
> Both walk `skills/` and every reference page (the root `llms.txt`,
> each module's `llms.txt` index and its `llms/**/*.txt` pages), so a
> new document is covered the day it lands. They read fenced blocks, including
> unlabelled ones, but not those labelled `json`, `yaml` or `text`,
> where a `pgedge` line is quoted rather than run. They do NOT read
> the 4-space indented blocks `README.md` and `docs/*.md` use, no rule
> follows a step that delegates to another workflow, and a status
> check is scoped to the window rather than to one read.

## Build & Test

- `make build` — build the `pgedge` binary
- `make test` — all tests, race detector, coverage profile, and the
  90% coverage gate. It runs `scripts/coverage-gate.sh`, the same
  script and the same defaults CI uses, so it fails on a coverage
  number as well as on a failing test
- `make lint` — golangci-lint + gofmt check
- `make lint-docs` — Vale prose linting over Markdown, llms.txt and
  Go doc comments
- `make docs` — regenerate the command reference from the cobra tree
- `make notice third-party-licenses` — regenerate `NOTICE.txt` and
  `THIRD_PARTY_LICENSES.txt` after a dependency change; CI fails on
  drift
- `make docs-check` — report reference drift without rewriting (CI)
- `make test-scripts` — shellcheck + the shell unit-test suites
- `make generate` — regenerate API clients from the OpenAPI specs
  (do not regenerate casually — see CONTRIBUTING.md "OpenAPI specs
  and generated clients")
- `make vendor-spec` — capture the three per-product Starfleet specs
  from the published public contracts
  (`https://api.pgedge.com/{product}/v1/openapi.json`, already
  filtered by saas's own public filter). Fail-closed on any
  `x-pgedge-*` extension, on `x-go-type-import` and on a
  foreign-namespace path, so the vendored specs can only ever
  describe the public API. Hits the network; record each capture in
  `openapi/SOURCE`. Never hand-edit a spec: `make vendor-spec-check`
  reports drift, and `TestVendoredSpecsCarryNoVisibilityMarkers`
  fails the build on a marker that a hand edit reintroduced
- `make test-integration` — env-gated live-API suite
  (`PGEDGE_INTEGRATION=1`, `PGEDGE_INTEGRATION_PROFILE`); not
  part of `make test`, hits the network
- `make test-integration-lifecycle` — creates REAL cloud
  infrastructure, costs money, 40–90 minutes
- `make test-inspect-live` — every `inspect` analysis through the
  built binary against a real Postgres pair (Docker,
  `test/inspectlive/compose.yaml`, `PG_VERSION=18|17|16`), cell
  values checked against fixture rows. The unit tests never run the
  SQL, so this is the one gate on an alias bound to the wrong
  expression. Not in `make test`; CI runs it on each supported
  Postgres major. Six seconds of tests plus a five-minute wait for
  the long-running-queries fixture
- `make build-all` — cross-platform build matrix; `make build-one
  GOOS=... GOARCH=...` is one target of it, which CI runs one per job

## Verifying a change — the compiler is the authority

Every trap below has produced a confidently wrong conclusion here.

- **IDE/LSP diagnostics are advisory, never authoritative.** gopls goes
  stale whenever files are created, moved or deleted outside its
  file-watch loop — which is every agent edit, especially a multi-file
  `git mv`. Running tally: **stale on every occasion so far**, including
  claims that generated symbols were undefined, and one naming a file
  that never existed. If diagnostics contradict a clean build, **believe
  the build.** To test one claim cheaply, ask for that symbol's
  definition; if it resolves, the diagnostic was stale. Never "fix" code
  on a diagnostic alone, and never read an *absence* of diagnostics as a
  clean build.
- **`go build` hides errors twice:** it caps output at 10 per package
  and does not compile test files. Always `go build -gcflags=-e ./...`
  **and** `go vet ./...`. Vet is the instrument for any claim about a
  `_test.go` file.
- **Never pipe a long test run through `tee`** — the exit code becomes
  tee's, so a failing suite looks like it passed. Redirect to a file and
  check `$?`. In zsh `${PIPESTATUS[0]}` is empty (it is `pipestatus`,
  1-indexed), so that workaround fails silently too.
- **Assert where you are and that you found work:** `cd <repo> || exit 1`
  then confirm `go list ./... | wc -l` is non-zero. A stale shell cwd
  makes `go build ./...` match zero packages and report success.
- **Give any comparison harness a positive control.** A harness that
  no-ops looks exactly like a passing comparison — and an `env -u …`
  prefix held in a shell **variable** expands as one word in zsh, so
  every invocation fails while the diffs still "pass".
- **`rc=0` does not prove a gate ran.** `go test -run X` without `-v`
  prints nothing to count, and a fabricated `-run` pattern exits 0
  having run nothing. Count `=== RUN` lines and assert the count.
- **A mutation test tells you nothing until you prove the mutation
  landed** — grep the precise string, confirm the mutated tree compiles
  (rc=0) before believing a failure, and name the code path the test
  actually exercises before concluding an assertion is weak. A mutation
  that "passes" is often one that landed somewhere the test never runs.

## Architecture

- `cmd/pgedge/` — entry point: loads config, resolves the active
  profile, builds the shared `Runtime`, registers modules
- `cmd/gendocs/` — the reference generator behind `make docs`
- `cmd/vendorspec/` — the spec splitter behind `make vendor-spec`
- `internal/module/` — the module contract: `Module` interface +
  `Runtime` struct (config, profile, renderer, stdio). Modules
  never reach into globals; everything arrives via `Runtime`
- `internal/cli/` — root command, global flags, exit codes, confirm
  prompt contract, and the top-level `version`, `doctor`, `profile`,
  `llms` and `completion` commands
- `internal/config/` — `~/.pgedge/cli/config.yaml`, named profiles
  with per-module sections, precedence flag > profile > default
- `internal/auth/` — credential resolution + token cache at
  `~/.pgedge/cli/cache/<profile>-<module>.json`, bound to the minting
  connection (credential AND API base URL), written atomically
- `internal/output/` — shared renderer: text (default), json, yaml
- `internal/docgen/` — renders a command's reference block from cobra
  and reports conformance; the one definition of "conforming"
- `internal/starfleet/`, `internal/controlplane/` — the modules. In each, `api/` is the generated
  client (do not edit by hand), `cmd/` is the Cobra tree, `llms.txt`
  is that module's embedded index and `llms/**/*.txt` its pages, one
  per resource
- `internal/clitest/` — build-gate tests over the full tree
- `openapi/` — four vendored specs (byoc, managed, account,
  control-plane) + codegen config for four generated clients

## The AI-agent reference

`llms.txt` is the index and each module embeds its own pages: an
index at `internal/<module>/llms.txt` (reached through
`Module.Reference()`) and one page per resource under
`internal/<module>/llms/`, whose scope is its path
(`llms/database/mcp.txt` is `pgedge llms <module> database mcp`),
reached through `Module.Documents()`. `internal/reference` derives
that set from the embedded files, so no gate keeps a list of pages:
`clitest.ReferenceFiles()` is the one enumeration. Every index page
ends with a generated routing table, and `TestReferencePagesStayUnderBudget`
holds every page below `reference.PageSizeBudget` (25 KB) except the
pages its `oversized` map excuses by name. A module
that ships no reference must report `ProvidesLLMS: false`.

**The INDEX has a size budget; the module references do not.**
`pgedgecli.IndexSizeBudget` bounds the top-level `llms.txt` and
`TestIndexStaysUnderBudget` enforces it, because the index is the
first read every agent makes and it exists so that looking up one
flag does not cost every module's whole reference. A module reference
is the targeted second read — an agent reaches one only after the
index has told it which — so its size is paid by a caller who already
wanted it, and byoc's 131 KB is that bargain working rather than a
violation. Adding to a module reference needs no budget thought;
adding to the index does.

Command blocks are generated; everything else is hand-written. When a
hand-written claim must quote something a gate would flag, annotate it
at the point of use — never per file:

    <!-- doc-gate: deliberately-wrong <phrase> — <reason> -->
    <!-- doc-gate: correct-for <module> <phrase> — <reason> -->

`deliberately-wrong` asserts the phrase is wrong and quoted on purpose.
`correct-for` asserts it is right, but under another module's scope,
and must name a scope other than the section's own. Each marker excuses
one phrase in one sentence, and a marker that excuses nothing is an
error.

## Reference examples — evidence and gates behind the rule

The block-intro and prose-over-pasted-output rules themselves live in
the `ant-docs-writer` skill (`writing.md`, `agent-pages.md`) — this is
the repo-specific evidence and gate mechanics behind them, not a
restatement of the rules.

**Nothing enforces the intro-label convention**, so it decays quietly
— two bare fences had survived the sweep that declared them
nonconforming. If you find one, fix it while you are in the file.

**Prose beats a pasted block because it's measured, not a style
preference:** #220 swept the **75** example-output blocks the five
references carried and found **36 wrong**, while the two references
that pasted none had none to fix. (How many of the 36 were byoc's is
not settled — that PR's body and its commit message disagree — but the
concentration is not what the rule rests on.) A prose claim naming a
field or a status IS machine-checked: the field-claims and
status-value gates in `internal/clitest` scan every reference, and
`TestEveryModuleReferenceIsScanned` derives that set from the modules
so a new one cannot be missed. A pasted block is checked far more
thinly: its header line against the declared columns, its status and
state CELLS by the same gate that checks them in prose, and its
`Monitor with:` line where a verb declares `--wait`. Everything else in
it — every other value, and any line it is MISSING — is checked by
nothing. So a pasted block is mostly a claim nothing verifies; prose is
a claim something does.

**Scope a prose claim to the verb you checked.** "`database get` opens
with a one-row table carrying `list`'s columns, not a FIELD/VALUE
block" teaches the shape and is gated; the same claim written for
`get` generally is false for `task get`, which really does print a
label/value block — that over-reach is what a reader would then go and
paste.

## Comments: less text beats wrong text

**A missing comment costs a reader one thing — they read the code,
which cannot lie. A wrong comment costs them a false belief they act
on without checking.** Those are not symmetric, so brevity wins by
default, and the more you explain the more you get wrong. Well-written
code leans self-explanatory; a comment is what is left over.

The test for whether a line earns its place: **could a reader check it
by reading the code?** If yes, do not write it — the code says it
better and cannot go stale. If no, it is the kind that earns its
place, and the kind that must be MEASURED and dated when written,
because nothing in the file can catch it being wrong. Measured API
behaviour, why this approach and not the obvious one, and the bug a
line prevents are all of that kind.

Prose about the comment itself is neither, and is the worst case: no
referent anywhere, so nothing can ever check it.

## Review severity — a comment is never Critical or Important

**A code comment is never a Critical or an Important finding.** Not a
stale one, not a wrong count, not a missing caveat, not a claim the
code has outgrown. It is Minor at most. It never blocks a merge and it
never earns its own review round: fix it in passing if you are already
in the file, otherwise let it go.

That is about `//` comments and commit messages. **Text the USER sees
is not a comment** — `--help`/`Long`, `llms.txt`, the skills, `README`
and `docs/` are product, and a wrong claim there is as severe as a
wrong line of code.

**One review, one fix round, then merge.** A second round happens only
when the first found a FUNCTIONAL defect or a gate a mutation escaped.
Never re-review a fix that touched only comments.

Tell every reviewer this in its prompt, and have it report prose as a
single non-blocking list at the end rather than as findings.

This rule was written after #336 took five review cycles and produced
eleven findings, every one a comment and none of them a defect in the
rule the comment described. The functional work was done in round one.

## Standards

- gofmt mandatory; golangci-lint must pass (`make lint`)
- Never use panic (one sanctioned exception: duplicate module
  registration in `internal/module`, an init-time programmer error)
- Comments earn their lines by recording why this approach rather
  than the obvious one, what breaks if it changes, or which bug they
  prevent — not by restating the next line, narrating API behaviour
  at length, or repeating a fact already given on a sibling
  declaration. Keep the facts, cut the words around them: the
  16-line comment `internal/metricfmt.Value` replaced said no more
  than its 5-line successor does. Repo-wide sweep is #200
- **A doc comment must not outgrow the code it explains.** Review
  history, case history and "an earlier version of this said"
  belong in git and in the PR, never in the file
- Tests required for new functionality; table-driven preferred;
  tests touching config/cache must set `t.Setenv("HOME",
  t.TempDir())`
- Coverage gate: >= 90%, computed by `scripts/coverage-gate.sh`
  excluding the generated `internal/{starfleet/{account,byoc,managed},controlplane}/api`
  packages. Touch the script, not the workflow
- **A success carrying no body prints nothing to stdout.** The
  acknowledgement is exit 0 and a sentence on stderr; stdout stays
  byte-empty in text, json and yaml. Never fabricate a response
  object to fill it (#141)
- Resource commands: singular names with plural aliases;
  destructive verbs go through `cli.Confirm` with `--force`;
  every leaf needs Short (<60 chars) and Long with an Example.
  Short is what the generated reference prints, so it is user-facing
- Conventional commits (`feat:`, `fix:`, `docs:`, `chore:`),
  header <= 50 chars; no Co-Authored-By or generated-with lines
- 79-character wrap in markdown; 4-space indent in non-Go files
