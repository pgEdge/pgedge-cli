# Troubleshooting BYOC

BYOC produces symptoms the exit code alone does not name, and the
[troubleshooting guide](../troubleshooting.md) carries the exit-code
contract itself.

## Entitlement failures

Not every entitlement failure surfaces as an error. Some byoc lists
answer with an empty array whatever the plan, so an empty result is
not evidence either way. The
[account and clients workflow](../starfleet/account-and-clients.md)
describes both shapes and how to tell them apart.

## An empty log or metrics read

Two BYOC cases account for most empty reads, each of them a name the
API never validates:

- An unrecognized journald log name on `byoc node logs`. The API
  validates no name, so the response is 200 carrying the literal
  `-- No entries --`, which the CLI prints on stdout exactly as it
  prints an idle log. Nothing goes to stderr, and nothing marks the
  two apart.
- An unknown component or node name on `byoc database logs`. Both
  answer 200 with no log blocks, so stderr carries `No logs found.`
  just as it would for a component that has logged nothing.

The [logs and metrics guide](logs-and-metrics.md) covers both.
