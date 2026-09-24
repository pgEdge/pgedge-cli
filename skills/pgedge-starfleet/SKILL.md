---
name: pgedge-starfleet
description: "Use this skill when the user wants to authenticate with
  pgEdge Starfleet, diagnose a broken Starfleet connection, or manage
  the account itself through the pgedge CLI: API clients, the tenant,
  team invites or team memberships. Triggers on: pgedge starfleet,
  pgedge starfleet auth login, Starfleet authentication, a rejected
  credential, starfleet doctor, creating an API client, inviting a
  teammate, team membership, or calling a Starfleet API path directly.
  The byoc and managed sub-trees borrow this skill's one connection;
  for clusters, databases and services once authenticated, use the
  pgedge-byoc or pgedge-managed skill."
---

# pgEdge Starfleet Skill

The CLI embeds the reference for its own version, and that reference
is the source of every command, flag, output field and behavior this
skill relies on. This skill routes you to the right page and names the
rules an agent must not miss. The pages carry the detail.

## Read the reference first

Before you run a starfleet command, read the index:

```bash
pgedge llms starfleet
```

The index carries credential precedence, the token cache, exit codes,
IDs and the team onboarding sequence. Then read the one page your task
needs, from the table below. The reference states refusals and edge
cases that `--help` omits, so work from it rather than from `--help`
or memory.

## Set up

Confirm the binary runs, then log in and check the connection:

```bash
pgedge version
pgedge starfleet auth login
pgedge starfleet doctor
```

`--profile <name>` selects the tenant on any command. A profile
carries one credential, so a profile is a tenant.

## Find the page for your task

Each page prints with `pgedge llms starfleet <page>`:

| Task | Page |
|---|---|
| Log in, check what is configured, see who the API accepts, log out | `auth` |
| Diagnose a broken connection or read the tenant's plan | `doctor` |
| Create, list, read, update or delete an API client | `client` |
| Send, list or delete a team invite | `invite` |
| List team members, or remove one | `membership` |
| Read or rename the tenant | `tenant` |
| Call a Starfleet API path that no verb covers | `api` |
| Manage BYOC clusters, databases or services | the pgedge-byoc skill |
| Manage Managed databases | the pgedge-managed skill |

## Rules an agent must not miss

Each rule is stated in full on the page named beside it.

- **Credentials come from flags, then the `PGEDGE_CLIENT_ID` /
  `PGEDGE_CLIENT_SECRET` pair, then the profile.** No other `PGEDGE_*`
  variable is read. The pair ignores the profile's API URL, and
  `--profile` alongside it is exit status 2. The token cache is bound to the
  credential and the API URL together. See the index.
- **`auth status` proves only that credentials resolved.** A wrong
  secret still reports `authenticated: true`. `doctor`'s tenant check
  is the one that proves the server accepts the credential. See
  `auth` and `doctor`.
- **Read `doctor -o json`, not its exit status.** `doctor` exits 0
  even when authentication is broken. See `doctor`.
- **`client create` shows the new secret once.** Capture it from that
  command's output, and keep it out of the transcript. To replace a
  secret, create a client and delete the old one. See `client`.
- **Invites are created and accepted in the Starfleet UI.** Every
  CLI credential is an application's, and both steps need a signed-in
  user, so they exit 5 here. Track and clean up invites from the CLI.
  See `invite`.
- **Membership is the signal that an invite was accepted.** See
  `invite` and `membership`.
- **Check a read's exit status before acting on an empty result.** A
  failed read prints nothing on stdout, so an invite that looks absent
  may only be unread. See the index's team onboarding sequence.
