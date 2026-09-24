# Onboard a teammate

Four steps add someone to your pgEdge Starfleet team and confirm they
joined, and two of them happen in the Starfleet UI rather than the
CLI.

## Commands available from the CLI

Each invite and membership command either runs under a CLI credential or
refuses outright:

| Command | Runs from the CLI |
|---|---|
| `invite list` | Yes |
| `invite get <invite_id>` | Yes |
| `invite delete <invite_id>` | Yes |
| `invite create` | No, exits 5 |
| `invite accept <invite_id>` | No, exits 5 |
| `membership list` | Yes |
| `membership delete <membership_id>` | Yes |

`invite create` and `invite accept` both need a signed-in user. The
CLI authenticates with a client ID and secret, which names an
application rather than a person, so Starfleet refuses those two
operations whatever the credentials are. Both commands recognize that
before sending anything and exit 5 with a message pointing at the UI.
No `-o` value changes it, and a retry cannot help, because the 5 is a
property of the operation rather than of your credentials.

## Invite a teammate

Work through these steps in order.

1. Invite the person from the Team page in the Starfleet UI. This is
   the first of the two steps the CLI cannot do.

2. Confirm from the CLI that the invite exists:

        pgedge starfleet invite list

    The table shows ID, EMAIL, INVITED BY, TEAM, EXPIRES and
    CREATED. INVITED BY is blank when the API omits it, and the
    timestamp columns show the date alone.

3. Ask the new member to accept from the link in their invitation
   email, or in the Starfleet UI. This is the other step the CLI
   cannot do.

4. Verify the membership:

        pgedge starfleet membership list

    The person's address appears in the USER EMAIL column once they
    have accepted, and presence in that list is the signal that the
    invite was taken up.

## Exit codes and empty reads

There is no `status` field on an invite anywhere in this API.
`membership list` is where a taken-up invite shows, never the invite
itself, so there is no state word to poll and absence is the only
thing left to read.

That makes the exit code the trustworthy signal, because under the
default `-o text` a failed read and an empty successful read look
identical on `stdout`:

- A successful but empty read prints "No invites found." on `stderr`
  and exits 0, so the absence is real at the moment of the read.
- A failed read prints nothing on `stdout` and exits non-zero, so the
  invite is unverified rather than missing.

Under `-o json` and `-o yaml` the two look different, since an empty
success still prints an empty array while a failure prints nothing.
The exit code is the reliable test in every format.

The remedy for a member who never appears is revoking the invite:

    pgedge starfleet invite delete <invite-id> --force

Revoking is destructive, so it prompts unless you pass `--force`, and
treating a failed read as an absence revokes a perfectly good invite.
Check the exit code first. On exit 0 with the address still absent,
wait and read again before concluding anything, and revoke only when
the address itself was wrong.

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
