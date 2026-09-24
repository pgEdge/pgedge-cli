// Package cmd wires the account-level commands of the pgedge starfleet
// tree: authentication, API clients, tenants, invites and memberships
// — everything the accounts half of the Starfleet API owns.
//
// There is no `account` command: these hang directly off `pgedge
// starfleet` (internal/starfleet/cmd), which owns the persistent
// connection flags they read.
package cmd
