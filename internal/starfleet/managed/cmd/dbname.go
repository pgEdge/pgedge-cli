package cmd

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/connstr"
	"github.com/spf13/cobra"
)

// addDBNameFlag registers --dbname, which names a second Postgres
// database inside the managed database in place of the one its
// connection block names.
func addDBNameFlag(cmd *cobra.Command, dbname *string) {
	cmd.Flags().StringVar(dbname, "dbname", "",
		"Connect to this Postgres database inside the managed database "+
			"instead of its own")
}

// validateDBName refuses a --dbname the URI cannot carry as one path
// segment. Whether the database exists is the server's to say.
func validateDBName(cmd *cobra.Command, name string) error {
	if !cmd.Flags().Changed("dbname") {
		return nil
	}
	switch {
	case name == "":
		return newExitError("--dbname given an empty value: name a "+
			"database, or omit the flag", ExitUsage)
	case strings.ContainsAny(name, "/?#"):
		return newExitError(fmt.Sprintf(
			"--dbname %q cannot hold /, ? or #", name), ExitUsage)
	case strings.ContainsFunc(name, unicode.IsControl):
		return newExitError(fmt.Sprintf(
			"--dbname %q holds a control character", name), ExitUsage)
	}
	return nil
}

// withDBName returns cs pointed at dbname, or cs itself when dbname is
// empty. Only the database changes; Build percent-encodes it.
func withDBName(
	cs *connstr.String, dbname string, withPassword bool,
) *connstr.String {
	if dbname == "" {
		return cs
	}
	return connstr.Build(cs.Host, cs.Port, dbname, cs.Username,
		cs.Password, withPassword)
}
