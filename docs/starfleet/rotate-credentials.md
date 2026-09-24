# Rotate the CLI's own credential

The CLI can rotate the API client credential it signs in with.

The starfleet connection signs in with an API client, and the API
returns a client's secret exactly once, at creation. Nothing can
fetch it again. Rotation is therefore replacement:

1. Create the replacement client:

        pgedge starfleet client create --name ci-2026 \
            --description "CI credential" -o json

    Under `-o json` the whole client object goes to stdout with the
    secret under `auth0_secret`. In text mode the secret alone goes
    to stdout and every label goes to stderr, so redirecting stdout
    captures exactly the secret.

2. Sign the profile in with the new pair:

        pgedge starfleet auth login --profile <name> \
            --client-id <new-id> --client-secret <new-secret>

    The cached token is bound to the credential that minted it, so
    the next command exchanges a fresh token, and nothing keeps
    running as the old client.

3. Confirm the new credential works. `starfleet doctor` exits 0 even
   when the connection is broken, so its exit status is not the
   confirmation. Read the fields:

        pgedge starfleet doctor -o json

    Expect `auth.authenticated` and `tenant.resolved` both `true`.
    A resolved tenant proves the server accepts the credential. If it
    is `false`, check `api.reachable` first to rule the endpoint
    out.

4. Only then delete the old client:

        pgedge starfleet client delete <old-client-id> --force

    The old client is the working credential until the new one has
    proven itself, and a deleted client cannot be recovered, only
    replaced.

## Next steps

- The [authentication guide](../auth-and-profiles.md) covers the
  token cache and credential binding in full.
- The [CI and automation guide](../ci.md) covers scripting these
  steps unattended.
- The [starfleet command reference](../reference/starfleet.md) lists
  the flags.
