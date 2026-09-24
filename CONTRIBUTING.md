# Contributing to pgedge

## Getting Started

1. Fork the repository
2. Clone your fork
3. Create a feature branch: `git checkout -b feat/my-feature`
4. Install pre-commit hooks: `pre-commit install`

## Development

```bash
make build            # Build the binary
make test             # Tests, race detector, and the 90% coverage gate
make lint             # Run golangci-lint
make notice           # Regenerate NOTICE.txt after a dependency change
make third-party-licenses
                      # Regenerate THIRD_PARTY_LICENSES.txt, same trigger
make generate         # Regenerate API clients from OpenAPI specs
make vendor-spec      # Capture the Starfleet OpenAPI specs
make test-integration # Env-gated tests against a live API
make test-inspect-live # Every inspect analysis on a Postgres pair
```

`make test-integration` requires `PGEDGE_INTEGRATION=1` and reads
`PGEDGE_INTEGRATION_PROFILE` (default `dev`) for the config
profile to test against. It hits the network, so it is excluded
from `make test`.

`make test-inspect-live` starts two Postgres containers from
`test/inspectlive/compose.yaml`, runs each `inspect` analysis through
the built binary against them and checks the cell values against rows
the suite created. It needs Docker, takes about six minutes and is
excluded from `make test`; CI runs it on Postgres 18, 17 and 16.
`PG_VERSION=17` picks the image locally, and `GOTESTFLAGS=-short` skips
the one test that waits for a query to turn five minutes old.

## OpenAPI specs and generated clients

The CLI vendors three per-product OpenAPI specs in `openapi/`
(`byoc.yaml`, `managed.yaml`, `account.yaml`), captured from the
published public contracts rather than fetched at build time. saas
serves each one unauthenticated at
`https://api.pgedge.com/{product}/v1/openapi.json`, already filtered
to the public, enterprise-maximal surface by its own
`GeneratePublicSpec` — enterprise is the union of what any tenant can
be served, one CLI binary serves every tenant, and a verb outside the
caller's plan fails with the API's own entitlement error.
`openapi/SOURCE` records every capture. Re-capture with
`make vendor-spec` (production by default; both it and
`make vendor-spec-check`, which reports drift without rewriting, hit
the network) and regenerate with `make generate`; do not hand-edit
either the specs or the generated clients.

The capture is fail-closed: every product is validated before any
file is written, and the run aborts on an `x-pgedge-*` key anywhere
in a contract (the upstream filter should have removed every
visibility marker, so one appearing means that filter regressed), on
`x-go-type-import` (it names an `internal` package of the saas module,
which Go will not let this module import, so the generated client
would not compile — a bare `x-go-type` is expected: it names `UUID`,
which each module's `api/types.go` supplies locally), and on a path
outside the product's own namespace.
`TestVendoredSpecsCarryNoVisibilityMarkers` re-checks the committed
files on every build, catching a hand edit the capture would not see.

The retired bare `/v1` surface is not served and not vendored, and
neither is `/billing/v1`, which has no spec upstream. Every path in a
published contract must sit under that product's prefix, and one that
does not aborts the capture; `TestVendoredSpecsAreCanonical` asserts
the same thing over the committed files.

The self-hosted Control Plane's own `/v1` is unrelated and untouched —
`control-plane.json` is a different product's API.

### Generated response parsers mis-read an empty-bodied success

Every generated `Parse*Response` ends in a catch-all case —
`strings.Contains(rsp.Header.Get("Content-Type"), "json") && true` —
that unmarshals the body into the spec's `Error` model for *any*
status, 2xx included. Where an operation also has no typed 2xx case, a
success carrying an empty body has nothing else to match, so it lands
on the catch-all and `json.Unmarshal` of zero bytes fails. A call that
succeeded is then reported as `unexpected end of JSON input`.

Whether it fires depends only on whether the server sends a JSON
`Content-Type` alongside the empty 2xx. It is not hypothetical: saas's
`DeleteClient` handler answers `ctx.JSON(http.StatusNoContent, nil)`,
and because echo writes the content type before setting the status
while `net/http` keeps it on a 204, `pgedge starfleet client delete`
reported successful deletions as failures until it was fixed.

Commands whose success carries no body therefore call the *untyped*
generated operation and pass the status and body to
`checkEmptyBodyResponse`, which bypasses only the response parser and
keeps the generated request builder. A test in each module
(`TestEmptyBodySuccessIsNotReportedAsFailure` and its account and controlplane
counterparts) drives every such command through a `204` carrying a
JSON content type and requires exit 0, so a handler that switches to
`ctx.JSON` cannot break them silently.

The catch-all itself is not patched out. It comes from the
oapi-codegen template, `internal/*/api` is not edited by hand, and
`make generate` would revert any change. It remains present in the
parsers for operations that *do* have a typed 2xx case, where nothing
reads the catch-all and it is inert.

## Pull Requests

- Use conventional commit messages (`feat:`, `fix:`, `docs:`,
  `chore:`, etc.)
- Record each behaviour change as an entry at the top of
  `docs/changelog.md` (a contributor ledger, not published on the
  docs site); docs-only changes need none
- `make test` and `make lint` must both pass
- A dependency change needs `make notice third-party-licenses`; CI
  fails on a stale `NOTICE.txt` or `THIRD_PARTY_LICENSES.txt`
- Add tests for new functionality
- Keep PRs focused — one feature or fix per PR

## Flag vocabulary

One concept, one name. These spellings were ruled in the
arguments-vs-flags contract review (2026-08-23/24) and are the
citable convention; `TestNoFlagRevivesALostSpelling` in
`internal/clitest` fails a new flag that registers a spelling a
ruling retired.

| Concept | Spelling |
|---|---|
| Bound one request | `--timeout` |
| Bound a wait | `--wait-timeout` (default 600 seconds everywhere) |
| Poll cadence while waiting | `--wait-interval` |
| Metrics lookback (byoc) | `--interval`, the API parameter's own name |
| Metrics lookback (managed) | `--window` |
| Absolute time window | `--start-time` / `--end-time` |
| Filter a list by creation time | `--created-after` / `--created-before` |
| Sort order | `--descending`, defaulting false |
| Skip the confirmation prompt | `--force`, and nothing else |
| Server-side check waivers | `--force-unmodifiable`, `--force-lost` |
| A provider region | `--region`, never positional |

`managed backup list` alone defaults `--descending` true — its help
says why. A new server-side waiver takes a force-prefixed name for
the check it removes, never plain `--force`.

Multi-value flags: a plural name that splits on commas
(`StringSlice`); a singular, repeat-only `StringArray` for values
that may contain a comma, or that are simply repeat-only
(`--repository`, `--server-url`). One known stray pre-dates the
rule: `--backup-store-id` on `cluster create`/`update` is a
comma-splitting Slice with a singular name — unruled, so do not
copy it.

Deliberate non-uniformities, each ruled rather than pending: starfleet's
`--api-url` (one URL) and controlplane's `--base-url` (a repeatable HA list)
stay different — renaming either breaks profiles for a cosmetic win;
task filters keep each API's own wire words (`--subject-id`/
`--subject-kind` on starfleet, `--entity-id`/`--scope` on controlplane) so `-o
json` agrees with the flag that filtered it; and `byoc node logs`
speaks journald's own vocabulary (`--since`, `--until`, `--reverse`,
`--priority`, `--grep`), because that verb wraps journalctl.

Verbs: `delete` destroys a resource, `remove` detaches a child from
its parent, and `register`/`deregister` are the ingress-service
pair. Positional arguments name identity — one per ancestor in the
ownership chain — and everything else is a flag.

## Code Style

- `gofmt` mandatory for all Go code
- Follow existing patterns in the codebase
- Table-driven tests preferred

## License

By contributing, you agree that your contributions will be
licensed under the PostgreSQL License.
