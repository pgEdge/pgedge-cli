package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestNewDatabaseCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewDatabaseCmd(rt)

	if cmd.Use != "database" {
		t.Errorf("Use = %q, want \"database\"", cmd.Use)
	}
	found := false
	for _, a := range cmd.Aliases {
		if a == "databases" {
			found = true
		}
	}
	if !found {
		t.Errorf("Aliases = %v, want it to contain \"databases\"", cmd.Aliases)
	}

	want := map[string]bool{
		"list": false, "get": false, "create": false,
		"update": false, "delete": false,
		"service": false, "mcp": false, "rag": false,
	}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("database command missing subcommand %q", name)
		}
	}
}

func TestNewDatabaseServiceCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewDatabaseServiceCmd(rt)

	if cmd.Use != "service" {
		t.Errorf("Use = %q, want \"service\"", cmd.Use)
	}
	found := false
	for _, a := range cmd.Aliases {
		if a == "services" {
			found = true
		}
	}
	if !found {
		t.Errorf("Aliases = %v, want it to contain \"services\"", cmd.Aliases)
	}

	want := map[string]bool{"list": false, "get": false, "remove": false}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("service command missing subcommand %q", name)
		}
	}
}

func TestNewDatabaseMCPCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewDatabaseMCPCmd(rt)

	if cmd.Use != "mcp" {
		t.Errorf("Use = %q, want \"mcp\"", cmd.Use)
	}

	want := map[string]bool{"deploy": false, "update": false}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("mcp command missing subcommand %q", name)
		}
	}
}

func TestDatabaseCreateRequiresFlags(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	var out bytes.Buffer
	err := runByoc(t, rt, &out, "database", "create")
	if err == nil {
		t.Fatal("expected error when required flags missing")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error = %q, want 'required' flag error", err.Error())
	}
}

// TestValidateByocDatabaseName pins the byoc --name rule to saas's
// own, which is NOT the managed rule (#135).
//
// The byoc create path validates
// strings.ToLower(strings.TrimSpace(name)) against
// pgutil.ValidateDatabaseName: <= 63 bytes, first rune a unicode
// letter or underscore, every rune a unicode letter, digit or
// underscore. Because saas normalises before validating, uppercase and
// surrounding whitespace are ACCEPTED and coerced server-side — so the
// CLI must accept them too. A pre-check that refuses input the server
// takes is not a pre-check.
func TestValidateByocDatabaseName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Plainly valid.
		{"simple", "mydb", false},
		{"single letter", "a", false},
		{"digits after a letter", "db2test", false},
		{"underscores are legal here", "my_db", false},
		{"leading underscore is legal here", "_internal", false},
		{"max length", strings.Repeat("a", 63), false},

		// Accepted because saas normalises rather than refuses. Each of
		// these succeeds against the real API today; rejecting any of
		// them would be a regression, not a tightening.
		{"uppercase is downcased by the server", "MyDB", false},
		{"surrounding whitespace is trimmed by the server", "  mydb  ", false},
		{"unicode letters are legal", "café", false},

		// Genuinely refused by saas.
		{"hyphen", "my-db", true},
		{"space inside", "my db", true},
		{"dot", "my.db", true},
		{"leading digit", "1mydb", true},
		{"leading hyphen", "-mydb", true},
		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"too long", strings.Repeat("a", 64), true},
		// Length is counted in BYTES, as saas's len() is: 32 two-byte
		// runes are 64 bytes and over the limit, though only 32 chars.
		{"too long in bytes though not in runes", strings.Repeat("é", 32), true},
		// The one case that proves the ToLower in validateByocDatabaseName
		// is doing work rather than decorating. U+023A LATIN CAPITAL
		// LETTER A WITH STROKE is 2 bytes and lowercases to U+2C65,
		// which is 3. Exactly two runes in Unicode grow this way
		// (U+023A and U+023E); twenty-three shrink. 31 of them are 62
		// bytes as typed and 93 lowercased, so this is under the limit
		// before normalisation and over it after. saas measures the
		// lowercased form, so it rejects this name; dropping the
		// ToLower here would accept it and spend a round trip finding
		// that out.
		{"over the limit only once lowercased", strings.Repeat("Ⱥ", 31), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateByocDatabaseName(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("%q was accepted, want rejected", tc.input)
				}
				var ee *ExitError
				if !errors.As(err, &ee) || ee.Code() != ExitUsage {
					t.Errorf("want exit %d, got %v", ExitUsage, err)
				}
				return
			}
			if err != nil {
				t.Errorf("%q rejected: %v", tc.input, err)
			}
		})
	}
}
