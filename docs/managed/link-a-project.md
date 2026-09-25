# Linking a Project Folder to a pgEdge Starfleet Managed Database

You can tie a project folder to one pgEdge Starfleet Managed database,
then write that database's connection URI into the project's `.env`
file with one command. Nobody on the project copies a password by hand.

Four terms recur throughout:

- A link is the file `.pgedge/link.yaml` in a project folder. It
  names one Managed database, or one branch of it, by ID.
- A linked folder is the folder holding `.pgedge`, and every folder
  below it.
- `.env` is the file in which frameworks and ORMs read environment
  variables for a project.
- `DATABASE_URL` is the variable the CLI sets in `.env` to a
  connection URI for the linked database.

## Before You Start

The database must have finished being created before `env pull` can
write its connection. The address your application connects from also
needs an allowlist rule, as the
[Connecting an Application to a pgEdge Starfleet Managed
Database](connect-an-application.md) guide describes.

## Linking a Folder to a Database

In a terminal, run `database link` with no ID from the project's top
folder:

    cd <project-folder>
    pgedge starfleet managed database link

The command lists your databases by number, and you enter the number
of the one to link. When that database has branches, the command lists
them next. Press Enter to link the database itself, or enter a
branch's number. Only a branch with the status `available` can be
chosen. The command then asks whether to write `DATABASE_URL` into
`.env`, and Enter writes it.

A script must pass the database's full UUID, written `<db-id>` below,
because without a terminal and an ID the command exits with status 2.
Run `database list` to read the ID, then link with it:

    pgedge starfleet managed database list
    pgedge starfleet managed database link <db-id>

The command reads the database first, so it writes no file for an ID
your active profile cannot see. On success, it reports the folder, the
database and the file it wrote on stderr.

The link holds the database ID and nothing secret, with no credential
and no profile name. `env pull` and the read commands below also work
in any folder under the linked one.

## Creating a Database and Linking It in One Step

`database create --link` links the current folder to the new database
when the database is available. Run it from the project's top folder,
with a name of your choice:

    pgedge starfleet managed database create --name <db-name> \
        --size small --my-ip --wait --link

`--link` needs `--wait` or `--follow`, because a database still being
created has no connection to write. Without either one, the command
exits with status 2. The command checks for an existing link before it
sends the create, so a folder that is already linked costs you no
database.

The [Provision a managed database](provision.md) guide describes the
other create flags.

## Writing DATABASE_URL into .env

Run `pgedge env pull` anywhere inside the linked folder:

    pgedge env pull

The command finds the link in the current folder or a folder above
it. It stops looking at the first folder that holds `.git`. It writes
`.env` beside the `.pgedge` folder, whichever subfolder you ran it
from.

`pgedge env pull` runs the Managed module's own command, which you can
also type in full:

    pgedge starfleet managed database env pull

Both commands write the same file:

- An existing `DATABASE_URL` line is replaced, and every other line of
  `.env` stays as it was.
- Without a `DATABASE_URL` line, the command appends one.
- A missing `.env` is created readable by you alone.
- A `.env` that is a symbolic link is refused with status 1. Pass
  `--file` with the file the link points to.
- The value is percent-encoded, so every `.env` loader reads the same
  password, with no quoting.

The command writes nothing to stdout. On stderr, it names the file,
the database or branch, and the role, and ends like this:

    Set DATABASE_URL in <project-folder>/.env to database <db-id>'s connection, as app.

Four flags change what is written:

- `--file <path>` writes another file, such as `.env.local`.
- `--var <name>` sets another variable instead of `DATABASE_URL`.
- `--user-type <role>` takes the credentials of `admin`, `app` or
  app_read_only. The default is `app`.
- `--branch <branch-id>` writes a branch's connection this once, and
  leaves the link as it is. Run `database branch list` to read the ID.

To write a database's connection without a link, give its ID to the
Managed command. This form writes `.env` in the current folder:

    pgedge starfleet managed database env pull <db-id>

The [ORM and framework integration](../orm-and-frameworks.md) guide
shows how frameworks read `DATABASE_URL`.

## Keeping .env out of Git

The `.env` file holds the role's live password. Inside a Git work
tree, `env pull` warns on stderr when Git does not ignore the file:

    Warning: git does not ignore <project-folder>/.env, which now holds a password. Add .env to .gitignore.

Add the file to `.gitignore` before your next commit. A password
pushed to a shared repository stays in its history after you delete
the file.

When Git already tracks `.env`, the warning names
`git rm --cached .env` as well. Git keeps committing a tracked file
even after `.gitignore` lists it.

## Sharing the Link with Your Team

Commit `.pgedge/link.yaml` with the project. A teammate who clones the
project then runs one command in it:

    pgedge env pull

The link holds no profile, so each teammate's own active profile must
be able to see the database. Each teammate gets a `.env` of their own,
which stays out of the repository.

## Linking a Branch

To point the project at a branch, run `database link` with no ID in a
terminal and choose the branch. From a script, add `--branch` with the
branch's ID. Run `database branch list <db-id>` to read it:

    pgedge starfleet managed database branch list <db-id>
    pgedge starfleet managed database link <db-id> --branch <branch-id>

A folder already linked to the database itself needs `--force` to
switch to the branch:

    pgedge starfleet managed database link <db-id> \
        --branch <branch-id> --force

With a branch link, `env pull` and `connection-string` use the
branch's connection. The other read commands act on the source
database. Run `pgedge env pull` again after switching, and
the command replaces the `DATABASE_URL` line with the branch's URI.

## Using Read Commands Without an ID

Inside a linked folder, these read commands take the linked database
when you leave out its ID:

- `database get`
- `database connection-string`
- `database inspect`
- `database logs`
- `database metrics`
- `database allowlist get`
- `database branch list`

Each one prints `Using database <db-id> from <path>` on stderr, so you
can see which database answered. Run this in a linked folder to print
the linked database's connection URI without its password:

    pgedge starfleet managed database connection-string --no-password

Every command that changes a database still needs its ID, in a linked
folder too. A script whose ID variable is unset fails, instead of
changing whatever database the folder happens to be linked to. For
example, `allowlist add <address>` with no ID reads the address as the
ID and exits with status 2.

## Refreshing .env After a Password Rotation

`env pull` writes the role's current password. After you rotate that
role's password, run the command again to replace the old value:

    pgedge env pull

The [Rotating a Password on a pgEdge Starfleet Managed
Database](rotate-credentials.md) guide describes the rotation itself.
The [Branching a pgEdge Starfleet Managed Database](branching.md)
guide describes creating and removing branches.

## Removing a Link

Run `database unlink` from the folder that holds `.pgedge`:

    pgedge starfleet managed database unlink

The command removes `.pgedge/link.yaml`, and the `.pgedge` folder when
nothing else is left in it. It changes nothing in the database and
leaves `.env` as it is. Run from a subfolder, the command exits with
status 1, because it removes only a link in the current folder.

## Troubleshooting

Each entry below names the error a link or `env pull` command prints,
its cause and the fix.

### No Link Found

`pgedge env pull` exits with status 2, and the message begins
`no .pgedge/link.yaml found here or above`. A read command run with no
ID exits with status 2 for the same cause. Either no link exists in
this folder or above it, or a folder holding `.git` sits between you
and the link. Link the folder with
`pgedge starfleet managed database link <db-id>`, or pass the ID.

### Home Folder Refused

`database link`, `database unlink` and `database create --link` exit
with status 2 in your home folder. The message begins
`the home folder cannot be linked`. Your home folder's `.pgedge` holds
shared pgEdge settings, so it never holds a link. Run the command from
a project folder instead.

### Folder Already Linked

`database link` exits with status 1, and the message ends
`already links database <db-id>; pass --force to replace it`. The
folder links another database or branch. Add `--force` to replace the
link.

`database create --link` refuses a linked folder with status 1 before
it sends the create. Run `pgedge starfleet managed database unlink`
first, or create the database without `--link`.

### Database Has No Connection Host Yet

`env pull` exits with status 1, and the message says the database
`has no connection host yet`. The database is still being created, or
its status is not available. Run `pgedge env pull` again when the
database has finished being created.

### Database ID Not Found

`database link` or `env pull` exits with status 4, and the message
begins `resource not found`. Your active profile cannot see that
database or branch, so no link file is written. Check the ID with
`pgedge starfleet managed database list` under the profile that owns
the database.
