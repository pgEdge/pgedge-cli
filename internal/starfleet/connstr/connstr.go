// Package connstr assembles a libpq connection string from the parts a
// database GET returns, for the managed and byoc connection-string
// verbs. The server bytes are carried as they arrived: url.URL
// percent-encodes host, userinfo and path, ShellQuote single-quotes
// every env value, and encoding/json escapes control characters, so
// nothing a server string can carry forges a line in any format, and
// no escaping pass rewrites a legal password.
package connstr

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/term"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
)

// String is what -o json and -o yaml print. The URI is the same one
// text mode prints, and the parts beside it are the ones it was
// assembled from, so a caller can take either without a second call.
// Password is omitted, not blanked, without one. Node is set only by
// byoc, whose string belongs to one node of the database.
type String struct {
	URI      string `json:"uri"`
	Node     string `json:"node,omitempty"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	SSLMode  string `json:"sslmode"`
}

// SSLModeRequire is carried on every string. Managed hosts serve TLS
// 1.3 with a certificate that verifies (measured 2026-08-29), so
// require is safe on every client and stricter is the caller's to add.
const SSLModeRequire = "require"

// Build assembles the URI. url.URL does the percent-encoding of the
// userinfo, which is the part a hand-built string gets wrong; the
// database name goes through the path for the same reason. The caller
// has already established that host is non-empty.
func Build(host string, port int, database, username, password string,
	withPassword bool,
) *String {
	cs := &String{
		Host:     host,
		Port:     port,
		Database: database,
		Username: username,
		SSLMode:  SSLModeRequire,
	}
	u := url.URL{
		Scheme:   "postgresql",
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		Path:     "/" + database,
		RawQuery: "sslmode=" + SSLModeRequire,
	}
	if withPassword {
		cs.Password = password
		u.User = url.UserPassword(username, password)
	} else {
		u.User = url.User(username)
	}
	cs.URI = u.String()
	return cs
}

// PrintEnv writes one PG* assignment per line, single-quoted so a
// value holding a space, a $ or a quote survives `source` and
// `export $(cat)` alike. PGPASSWORD is absent, not empty, without a
// password, so a shell that already exports one keeps it.
//
// A value holding a control character is refused, not rewritten: a
// newline inside single quotes is still one assignment to a shell,
// but to a reader it is a forged line, and rewriting the value would
// print a credential the server never issued. No real host, role or
// password carries one.
func PrintEnv(w io.Writer, cs *String, withPassword bool) error {
	values := []string{cs.Host, cs.Database, cs.Username}
	if withPassword {
		values = append(values, cs.Password)
	}
	for _, v := range values {
		if strings.ContainsFunc(v, unicode.IsControl) {
			return conn.NewExitError("--format env refused: the "+
				"connection block carries a control character, which "+
				"cannot be written as a shell assignment", conn.ExitGeneral)
		}
	}
	lines := []string{
		"PGHOST=" + ShellQuote(cs.Host),
		"PGPORT=" + ShellQuote(strconv.Itoa(cs.Port)),
		"PGDATABASE=" + ShellQuote(cs.Database),
		"PGUSER=" + ShellQuote(cs.Username),
	}
	if withPassword {
		lines = append(lines, "PGPASSWORD="+ShellQuote(cs.Password))
	}
	lines = append(lines, "PGSSLMODE="+ShellQuote(cs.SSLMode))
	_, err := fmt.Fprintln(w, strings.Join(lines, "\n"))
	return err
}

// ShellQuote wraps s in single quotes, the one POSIX quoting form in
// which nothing but a single quote is special.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// StdoutIsTerminal is false for every buffer a test hands the Runtime,
// so the warning is exercised only where a person would see it.
func StdoutIsTerminal(rt *module.Runtime) bool {
	f, ok := rt.Stdout.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// ValidateFormat is the shared --format vocabulary check.
func ValidateFormat(format string) error {
	switch format {
	case "uri", "env":
		return nil
	}
	return conn.NewExitError(fmt.Sprintf(
		"--format %q is not one of uri or env", format), conn.ExitUsage)
}

// FormatCompletion completes --format.
func FormatCompletion() []string { return []string{"uri", "env"} }
