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

Most empty BYOC reads come from an unknown component or node name on
`byoc database logs`. The API validates neither name, so both answer
200 with no log blocks. Stderr then carries `No logs found.`, just as
it would for a component that has logged nothing. The
[logs and metrics guide](logs-and-metrics.md) covers this case.
