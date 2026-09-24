package connstr

import (
	"bytes"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
)

// The password carries every URI-special character so a URI that
// parses back to the same values proves the encoding rather than the
// happy path.
const testPassword = "p@ss:w/rd?#&'x\\y\u00a0z"

func TestBuildRoundTripsThePassword(t *testing.T) {
	cs := Build("db.example", 5432, "mydb", "app", testPassword, true)
	u, err := url.Parse(cs.URI)
	if err != nil {
		t.Fatalf("not a URL: %v", err)
	}
	if pw, _ := u.User.Password(); pw != testPassword {
		t.Errorf("password round-trips as %q", pw)
	}
	if u.Path != "/mydb" || u.Query().Get("sslmode") != SSLModeRequire {
		t.Errorf("path/query wrong: %s", cs.URI)
	}
	if cs.Password != testPassword {
		t.Errorf("parts do not carry the password verbatim")
	}
}

func TestBuildWithoutPassword(t *testing.T) {
	cs := Build("db.example", 5432, "mydb", "app", testPassword, false)
	if cs.URI != "postgresql://app@db.example:5432/mydb?sslmode=require" {
		t.Errorf("uri = %s", cs.URI)
	}
	if cs.Password != "" {
		t.Errorf("password should be empty")
	}
}

func TestPrintEnv(t *testing.T) {
	cs := Build("h", 5432, "d", "u", "it's", true)
	var b bytes.Buffer
	if err := PrintEnv(&b, cs, true); err != nil {
		t.Fatal(err)
	}
	want := "PGHOST='h'\nPGPORT='5432'\nPGDATABASE='d'\nPGUSER='u'\n" +
		"PGPASSWORD='it'\\''s'\nPGSSLMODE='require'\n"
	if b.String() != want {
		t.Errorf("env:\n got %q\nwant %q", b.String(), want)
	}
	b.Reset()
	if err := PrintEnv(&b, cs, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "PGPASSWORD") {
		t.Errorf("PGPASSWORD line present without password: %q", b.String())
	}
}

// Every value the env printer writes is checked, not only the host.
func TestPrintEnvRefusesControlCharacterInEveryValue(t *testing.T) {
	const evil = "evil\nPGUSER=x"
	cases := map[string]*String{
		"host":     Build(evil, 5432, "d", "u", "p", true),
		"database": Build("h", 5432, evil, "u", "p", true),
		"username": Build("h", 5432, "d", evil, "p", true),
		"password": Build("h", 5432, "d", "u", evil, true),
	}
	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			err := PrintEnv(&b, cs, true)
			var ee *conn.ExitError
			if !errors.As(err, &ee) || ee.Code() != conn.ExitGeneral {
				t.Fatalf("want exit %d, got %v", conn.ExitGeneral, err)
			}
			if b.Len() != 0 {
				t.Errorf("refused output still wrote %q", b.String())
			}
		})
	}
	// A control character in the password is not checked when the
	// password is not printed, so --no-password still works.
	var b bytes.Buffer
	if err := PrintEnv(&b, cases["password"], false); err != nil {
		t.Errorf("password not printed yet refused: %v", err)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"plain":   "'plain'",
		"a b":     "'a b'",
		"$HOME":   "'$HOME'",
		"it's":    `'it'\''s'`,
		"":        "''",
		`back\sl`: `'back\sl'`,
	}
	for in, want := range cases {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestValidateFormat(t *testing.T) {
	for _, ok := range FormatCompletion() {
		if err := ValidateFormat(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	err := ValidateFormat("dsn")
	var ee *conn.ExitError
	if !errors.As(err, &ee) || ee.Code() != conn.ExitUsage {
		t.Errorf("want exit %d, got %v", conn.ExitUsage, err)
	}
}
