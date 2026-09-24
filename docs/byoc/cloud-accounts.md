# Register a BYOC cloud account

A BYOC cloud account holds the credential pgEdge uses to provision
infrastructure inside your own AWS, Azure or GCP account. Nothing else
in BYOC works without one. `cluster create` takes a
`--cloud-account-id` and checks that the account exists before it
provisions anything. Registering the account is therefore the first
step on a new tenant.

You need administrative access to the cloud account you are about to
register.

## Provider requirements

The credential flags on `cloud-account create` depend on `--type`, and
the command refuses a type it does not recognize before it sends
anything. The following table shows the flags each provider requires:

| Provider | `--type` | Required flags | Optional flags |
|---|---|---|---|
| AWS | `aws` | `--role-arn` | `--name`, `--description` |
| Azure | `azure` | `--tenant-id`, `--subscription-id`, `--azure-client-id`, `--azure-client-secret` | `--resource-group`, `--name`, `--description` |
| GCP | `gcp` | `--project-id`, `--service-account` | `--name`, `--description` |

The Azure and GCP identifiers are the provider's own, not pgEdge
UUIDs. As a result, the CLI forwards them unchecked. `--project-id`
in particular is a GCP project name and can never be a UUID.

A missing credential flag, or a `--type` outside the three, is
refused locally at exit status 2, with nothing sent. Every other
malformed input gets the same exit status. The
[Exit codes](../exit-codes.md) guide covers the full contract.

## Register an AWS account

AWS is the one provider with a preparation step. pgEdge needs an IAM
role in your account, and CloudFormation is how you create it.

1. Print the CloudFormation deep link:

        pgedge starfleet byoc cloud-account cloudformation-template

    The command prints one URL, an AWS console deep link. The URL
    holds the template location, your tenant ID and an external ID
    as query parameters, not the template body itself. Open the URL
    in a browser signed in to the AWS account that will host the
    clusters. The console opens directly on CloudFormation's Quick
    create stack page, prefilled with the template, a stack name of
    `pgedge-permissions-<random-suffix>` and every parameter.

2. Select "I acknowledge that AWS CloudFormation might create IAM
   resources with custom names," then select "Create stack."

3. Once creation finishes, open the stack's Outputs tab.

4. Read the `RoleArn` value shown there.

5. Register the account, and keep the ID the confirmation prints:

        pgedge starfleet byoc cloud-account create \
            --name prod-aws \
            --type aws \
            --role-arn arn:aws:iam::<account>:role/<role-name>

    In text output the confirmation goes to stderr. The confirmation
    names the new account's ID and type. Pass `-o json` to capture
    the whole record on stdout instead.

## Register an Azure or GCP account

Azure and GCP need no template step. Create a service principal or a
service account with permission to build infrastructure, then pass its
identifiers to `cloud-account create`.

Registering a GCP project takes the project ID and the service
account's email address:

    pgedge starfleet byoc cloud-account create \
        --name prod-gcp \
        --type gcp \
        --project-id my-gcp-project \
        --service-account pgedge@my-gcp-project.iam.gserviceaccount.com

Azure takes four values. The client secret is write-only. The API
never reads the client secret back. Store the client secret wherever
you keep the rest of your secrets before you run the command:

    pgedge starfleet byoc cloud-account create \
        --name prod-azure \
        --type azure \
        --tenant-id <azure-tenant-id> \
        --subscription-id <azure-subscription-id> \
        --azure-client-id <azure-client-id> \
        --azure-client-secret <azure-client-secret> \
        --resource-group <resource-group>

## Read and remove accounts

`cloud-account list` prints one row per account. Each row lists the
ID, name, provider type and creation date. `cloud-account get` prints
the same columns for a single account as a one-row table. Neither takes
`--limit` or `--offset`, so the list is always the whole set.

A cloud account has no status field in any output. Provider detail
such as the role ARN or project ID lives in a generic properties map.
Only `-o json` returns the map, and no column shows it. When you need
to confirm which role an account points at, read it with `-o json`.

There is no `cloud-account update`. Changing a credential means
registering a second account and deleting the first, and a cluster
already built on the old account keeps pointing at it.

Deleting a cloud account is destructive, so the command prompts
unless you pass `--force`. The API refuses the delete at exit status
1 while a cluster or a backup store still uses the account:

    pgedge starfleet byoc cloud-account delete <cloud-account-id> --force

The prompt reads stdin. A script or a CI job that omits `--force`
fails with a usage error instead of hanging.

## Availability zones

Zone names vary by provider and by region, and `cluster create`
accepts an `availability-zone` key inside `--node`, such as
`--node name=n1,region=us-east-2,availability-zone=us-east-2a`, that
nothing validates client-side. Reading the zones first is how you
avoid a provisioning failure minutes later:

    pgedge starfleet byoc cloud-account availability-zones \
        <cloud-account-id> --region us-east-2

Zones print one per line on stdout. The command needs a non-empty
`--region`, and an empty value is a usage error rather than a request
against a collapsed path.

BYOC has no region check. A region that does not exist answers an
empty list at exit status 0, not an error. An empty result therefore
means "no zones here". That result covers both a real region with
nothing in it and a region you spelled wrong.

## Plan entitlement

A profile has one credential and therefore one tenant, and the
tenant's plan decides which BYOC capabilities answer at all. The
refusal takes two shapes, and only one of them looks like a failure.

The following table shows how the two shapes differ:

| Command | Unentitled tenant | What you see |
|---|---|---|
| `cloud-account list`, `backup-store list` | 400 holding `plan does not allow ...` | A message naming the plan, at exit status 5 |
| `cluster list`, `ssh-key list` | 200 with an empty array | Exit status 0 and no rows |

An empty `cluster list` is identical either way. Either the tenant
has no clusters, or the tenant has no BYOC entitlement at all. Never
read the empty list as proof of either. Read the tenant's plan before
you conclude anything from an empty result:

    pgedge starfleet doctor -o json

The [Exit codes](../exit-codes.md) guide owns the full meaning of
exit status 5. Exit status 5 covers authentication failures and
entitlement refusals alike.

## Register an SSH key

SSH keys are account-level, like cloud accounts, and a registered key
can be attached to clusters for node access. Register one from the
public half of a local key pair:

    pgedge starfleet byoc ssh-key create --name laptop \
        --public-key "$(cat ~/.ssh/id_ed25519.pub)"

The CLI parses the key before it sends anything, using the same
parser sshd uses. The CLI refuses four things that parser tolerates:
a value spanning more than one line, and two keys on one line. The
CLI also refuses a value holding authorized_keys options, and a type token that
disagrees with the key material it labels. OpenSSH cannot parse a key whose
type token disagrees with its material, so that mismatch appears as a node
you cannot reach.

`ssh-key list` and `ssh-key get` print ID, name and creation date. The
public key itself has no column, so read it back in JSON:

    pgedge starfleet byoc ssh-key get <ssh-key-id> -o json

Deleting a key is destructive. The command prompts unless you pass
`--force`:

    pgedge starfleet byoc ssh-key delete <ssh-key-id> --force

## Next steps

- The [Manage BYOC clusters](clusters.md) document covers
  building a cluster on the account you registered.
- The [Provision a BYOC cluster and database](provision.md)
  document walks the whole path from a clean slate to a running
  database.
- The [pgedge starfleet byoc command reference](../reference/starfleet-byoc.md)
  lists every flag on the commands above.
