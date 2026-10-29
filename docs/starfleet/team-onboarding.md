# Onboard a teammate

Four steps add someone to your pgEdge Starfleet team and confirm they
joined. The new member accepts from the invitation email rather than
the CLI.

## Commands available from the CLI

Each invite and membership command runs under a CLI credential,
except for two:

| Command | Runs from the CLI |
|---|---|
| `invite list` | Yes |
| `invite get <invite_id>`, `invite get --email <addr>` | Yes |
| `invite delete <invite_id>` | Yes |
| `invite create --email <addr>` | With a browser sign-in only |
| `invite accept <invite_id>` | No, exits 5 |
| `membership list`, `membership list --email <addr>` | Yes |
| `membership delete <membership_id>` | Yes |

`invite create` needs a browser sign-in, from `pgedge starfleet auth
login` with no credential flags. With an API client it exits 5
before sending anything; invite from the Team page in the Starfleet
UI instead. `invite accept` exits 5 however you signed in.

## Invite a teammate

Work through these steps in order.

1. Invite the person, signed in through the browser:

        pgedge starfleet invite create --email teammate@example.com

    The table omits the invite's token and accept URL, because anyone
    holding them can join the team; `-o json` prints them. With an
    API client, invite from the Team page in the Starfleet UI instead.

2. Confirm from the CLI that the invite exists:

        pgedge starfleet invite get --email teammate@example.com

    The table shows ID, EMAIL, INVITED BY, TEAM, EXPIRES and
    CREATED. INVITED BY is blank when the API omits it, and the
    timestamp columns show the date alone.

3. Ask the new member to accept from the link in their invitation
   email, or in the Starfleet UI.

4. Verify the membership:

        pgedge starfleet membership list --email teammate@example.com

    The person's row appears once they have accepted, and presence in
    that list is the signal that the invite was taken up.

## Exit codes and empty reads

There is no `status` field on an invite anywhere in this API.
`membership list` is where a taken-up invite shows, never the invite
itself, so there is no state word to poll and absence is the only
thing left to read.

That makes the exit code the trustworthy signal. With `--email`,
`invite get` and `membership list` give each outcome its own code:

- Exit 0 prints the matching rows.
- Exit 4 means the read succeeded and nothing matches the address,
  compared without regard to case.
- Any other non-zero exit means the read failed, so the invite or
  membership is unverified rather than missing.

The remedy for a member who never appears is revoking the invite:

    pgedge starfleet invite delete <invite-id> --force

Revoking is destructive, so it prompts unless you pass `--force`, and
treating a failed read as an absence revokes a perfectly good invite.
Check the exit code first. On exit 4, wait and read again before
concluding anything, and revoke only when the address itself was
wrong.

## Memberships and owner semantics

`membership list` prints five columns: ID, USER NAME, USER EMAIL,
OWNER and CREATED. Every row it returns is a current member. There is
no membership status field and no `role` field anywhere in this API.

The OWNER column renders the `is_owner` boolean as yes or no, and
stays a boolean under `-o json`. That flag has no second value, so it
separates the tenant's owner from everyone else and says nothing
about what anyone else may do. Nothing distinguishes an administrator
from any other non-owner member, and there is no role to filter on.

Removing a member is destructive, since the person loses access to
the team's resources, so it prompts unless you pass `--force`:

    pgedge starfleet membership delete <membership-id> --force

The argument is the membership's UUID rather than the user's, and `membership
list` is where to read it. There is no `membership get` command, so the list is
the only read.

## Next steps

- The [account and clients workflow](account-and-clients.md) covers
  the tenant record, API clients and plan entitlement.
- The [exit codes guide](../exit-codes.md) keys each exit code to
  what produced it.
- The [command reference](../reference/starfleet.md) lists every
  account-level command and its flags.
