package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// The password carries every URI-special character so a URI that
// parses back to the same values proves the encoding rather than the
// happy path.
const (
	testConnPassword = "p@ss:w/rd?#&'x\\y\u00a0z"
	testConnHost     = "db.example.pgedge.cloud"
)

// databaseWithConnectionJSON is databaseJSON plus a connection block of
// the shape GET /managed/v1/databases/{id} returns (measured 2026-08-29:
// host, port, database, username, password; no external_ip_address).
func databaseWithConnectionJSON(id string) string {
	pw, _ := json.Marshal(testConnPassword)
	return fmt.Sprintf(`{
		"id":%q,"name":"mydb","status":"available",
		"region":"us-east-1","size":"small","pg_version":"16",
		"created_at":"2026-07-31T10:00:00Z",
		"updated_at":"2026-07-31T10:00:00Z",
		"connection":{"host":%q,"port":5432,"database":"mydb",
			"username":"app","password":%s}}`, id, testConnHost, pw)
}

func TestConnectionStringPrintsAnEncodedURI(t *testing.T) {
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseWithConnectionJSON(testDatabaseID)))

	err := runAuthed(t, rt, out, url, "database", "connection-string",
		testDatabaseID)
	if err != nil {
		t.Fatalf("connection-string: %v", err)
	}
	got := strings.TrimSpace(out.String())
	want := "postgresql://app:p%40ss%3Aw%2Frd%3F%23&%27x%5Cy%C2%A0z@" +
		testConnHost + ":5432/mydb?sslmode=require"
	if got != want {
		t.Errorf("uri:\n got %s\nwant %s", got, want)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Errorf("want exactly one line, got %q", out.String())
	}
	// The stdout in a test is a buffer, never a terminal, so the
	// warning must not fire here; a warning that fires into a pipe
	// would land in every captured .env file.
	if errOut.Len() != 0 {
		t.Errorf("stderr not empty for a non-terminal stdout: %q",
			errOut.String())
	}
}

func TestConnectionStringNoPasswordOmitsIt(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseWithConnectionJSON(testDatabaseID)))

	err := runAuthed(t, rt, out, url, "database", "connection-string",
		testDatabaseID, "--no-password")
	if err != nil {
		t.Fatalf("connection-string: %v", err)
	}
	got := strings.TrimSpace(out.String())
	want := "postgresql://app@" + testConnHost + ":5432/mydb?sslmode=require"
	if got != want {
		t.Errorf("uri:\n got %s\nwant %s", got, want)
	}
	if strings.Contains(out.String(), "p%40ss") ||
		strings.Contains(out.String(), "p@ss") {
		t.Error("--no-password still printed the password")
	}
}

func TestConnectionStringEnvFormat(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseWithConnectionJSON(testDatabaseID)))

	err := runAuthed(t, rt, out, url, "database", "connection-string",
		testDatabaseID, "--format", "env")
	if err != nil {
		t.Fatalf("connection-string: %v", err)
	}
	want := strings.Join([]string{
		"PGHOST='" + testConnHost + "'",
		"PGPORT='5432'",
		"PGDATABASE='mydb'",
		"PGUSER='app'",
		"PGPASSWORD='p@ss:w/rd?#&'\\''x\\y\u00a0z'",
		"PGSSLMODE='require'",
		"",
	}, "\n")
	if out.String() != want {
		t.Errorf("env:\n got %q\nwant %q", out.String(), want)
	}
}

func TestConnectionStringEnvNoPasswordDropsTheLine(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseWithConnectionJSON(testDatabaseID)))

	err := runAuthed(t, rt, out, url, "database", "connection-string",
		testDatabaseID, "--format", "env", "--no-password")
	if err != nil {
		t.Fatalf("connection-string: %v", err)
	}
	if strings.Contains(out.String(), "PGPASSWORD") {
		t.Errorf("PGPASSWORD line present under --no-password:\n%s",
			out.String())
	}
	if !strings.Contains(out.String(), "PGUSER='app'") {
		t.Errorf("PGUSER line missing:\n%s", out.String())
	}
}

func TestConnectionStringJSONCarriesURIAndParts(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseWithConnectionJSON(testDatabaseID)))

	err := runAuthed(t, rt, out, url, "database", "connection-string",
		testDatabaseID)
	if err != nil {
		t.Fatalf("connection-string: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	for _, k := range []string{"uri", "host", "port", "database",
		"username", "password", "sslmode"} {
		if _, ok := got[k]; !ok {
			t.Errorf("json missing %q: %v", k, got)
		}
	}
	if got["password"] != testConnPassword {
		t.Errorf("password = %v, want the raw value", got["password"])
	}
	if !strings.HasPrefix(got["uri"].(string), "postgresql://app:") {
		t.Errorf("uri = %v", got["uri"])
	}
}

func TestConnectionStringJSONNoPasswordOmitsTheKey(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseWithConnectionJSON(testDatabaseID)))

	err := runAuthed(t, rt, out, url, "database", "connection-string",
		testDatabaseID, "--no-password")
	if err != nil {
		t.Fatalf("connection-string: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["password"]; ok {
		t.Errorf("password key present under --no-password: %v", got)
	}
}

func TestConnectionStringSendsUserType(t *testing.T) {
	var query string
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(databaseWithConnectionJSON(testDatabaseID)))
		})
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "connection-string",
		testDatabaseID, "--user-type", "admin")
	if err != nil {
		t.Fatalf("connection-string: %v", err)
	}
	if !strings.Contains(query, "user_type=admin") {
		t.Errorf("query %q missing user_type=admin", query)
	}
}

func TestConnectionStringClientSideRefusals(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown format",
			[]string{"--format", "dsn"}, "--format"},
		{"empty user-type",
			[]string{"--user-type", ""}, "empty value"},
		{"unknown user-type",
			[]string{"--user-type", "superuser"}, "superuser"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, _ *http.Request) {
					called = true
					w.WriteHeader(http.StatusOK)
				})
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			args := append([]string{"database", "connection-string",
				testDatabaseID}, tc.args...)
			err := runAuthed(t, rt, out, url, args...)
			var ee *ExitError
			if !asExitError(err, &ee) || ee.Code() != ExitUsage {
				t.Fatalf("want exit %d, got %v", ExitUsage, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %q", err, tc.want)
			}
			if called {
				t.Error("a refused invocation reached the server")
			}
		})
	}
}

func TestConnectionStringWithoutHostIsExitOne(t *testing.T) {
	cases := map[string]string{
		"no connection block": databaseJSON(testDatabaseID, ""),
		"empty host": strings.Replace(
			databaseWithConnectionJSON(testDatabaseID),
			`"host":"`+testConnHost+`"`, `"host":""`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusOK, body))
			err := runAuthed(t, rt, out, url, "database",
				"connection-string", testDatabaseID)
			var ee *ExitError
			if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
				t.Fatalf("want exit %d, got %v", ExitGeneral, err)
			}
			if !strings.Contains(err.Error(), "no connection host") {
				t.Errorf("error %q does not explain the missing host", err)
			}
		})
	}
}

// The password is carried byte-for-byte: a backslash or a non-ASCII
// space is a character Postgres accepts, and an escaping pass on it
// would print a credential the server never issued.
func TestConnectionStringCarriesThePasswordVerbatim(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseWithConnectionJSON(testDatabaseID)))
	if err := runAuthed(t, rt, out, url, "database", "connection-string",
		testDatabaseID); err != nil {
		t.Fatal(err)
	}
	u, err := neturl.Parse(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatalf("output is not a URL: %v", err)
	}
	if pw, _ := u.User.Password(); pw != testConnPassword {
		t.Errorf("password round-trips as %q, want %q", pw, testConnPassword)
	}
}

// A control character in a server string must not forge a line.
// url.URL percent-encodes it; the env printer, whose single quotes
// would keep it inside one assignment but not off the reader's
// screen, refuses instead.
func TestConnectionStringControlCharacterInHost(t *testing.T) {
	body := strings.Replace(databaseWithConnectionJSON(testDatabaseID),
		`"host":"`+testConnHost+`"`, `"host":"evil\nPGUSER=x"`, 1)
	t.Run("uri is percent-encoded", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, body))
		if err := runAuthed(t, rt, out, url, "database",
			"connection-string", testDatabaseID, "--no-password"); err != nil {
			t.Fatal(err)
		}
		if strings.Count(out.String(), "\n") != 1 ||
			!strings.Contains(out.String(), "evil%0APGUSER=x") {
			t.Errorf("newline not encoded:\n%s", out.String())
		}
	})
	t.Run("env is refused", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, body))
		err := runAuthed(t, rt, out, url, "database",
			"connection-string", testDatabaseID, "--format", "env",
			"--no-password")
		var ee *ExitError
		if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
			t.Fatalf("want exit %d, got %v", ExitGeneral, err)
		}
		if strings.Contains(out.String(), "PGHOST") {
			t.Errorf("a refused env write reached stdout:\n%s", out.String())
		}
	})
}
