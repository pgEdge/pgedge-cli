package conn

import (
	"fmt"
	"unicode/utf8"

	"github.com/pgEdge/pgedge-cli/internal/cli"
)

// DisplayNameMaxLen mirrors `maxLength: 25` on display_name, which both
// vendored specs declare on the create input, the update input and the
// database response. Some schemas (managed's Size, byoc's ClusterNode)
// carry display_name with no maxLength, so
// TestDisplayNameMaxLenMatchesBothSpecs skips an absent declaration: it
// catches a changed value or a spec losing the field, not one schema of
// three losing it.
const DisplayNameMaxLen = 25

// ValidateDisplayName refuses a --display-name longer than the API
// accepts, before the request. Every verb taking the flag calls it, so
// create can never store a name that update would refuse. It
// counts characters, as OpenAPI maxLength does; counting bytes would
// refuse a valid non-ASCII name.
func ValidateDisplayName(v string) error {
	if n := utf8.RuneCountInString(v); n > DisplayNameMaxLen {
		return &cli.UsageError{Msg: fmt.Sprintf(
			"--display-name is %d characters; the limit is %d",
			n, DisplayNameMaxLen)}
	}
	return nil
}
