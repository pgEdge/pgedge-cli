// Package cmd wires the account-level commands of the pgedge starfleet
// tree: authentication, API clients, tenants, invites and memberships
// — everything the accounts half of the Starfleet API owns.
//
// There is no root command here any more. These commands hang directly
// off `pgedge starfleet` (internal/starfleet/cmd), which owns the persistent
// connection flags they read; the word "account" survives as this
// package's name and as the API surface it covers, not as a command.
package cmd
