package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const (
	testConnPassword = "p@ss:w/rd?#&'x\\y\u00a0z"
	testPublicHost   = "n1.example.com"
	testPrivateHost  = "n2.internal.example.com"
)

// byocNodeConnJSON is one entry of a database's nodes array as GET
// /byoc/v1/databases/{id} returned it (measured 2026-08-29):
// a public node carries host, a private-cluster node internal_host,
// never both. hostKey names which.
func byocNodeConnJSON(name, logical, hostKey, host string) string {
	pw, _ := json.Marshal(testConnPassword)
	logicalField := ""
	if logical != "" {
		logicalField = fmt.Sprintf(`"logical_name":%q,`, logical)
	}
	return fmt.Sprintf(`{"name":%q,%s"location":{},`+
		`"connection":{%q:%q,"port":5432,"database":"defaultdb",`+
		`"username":"admin","password":%s}}`,
		name, logicalField, hostKey, host, pw)
}

func byocDatabaseWithNodesJSON(nodes ...string) string {
	return `{"id":"` + testDatabaseID + `","name":"mydb",` +
		`"status":"available","pg_version":"16","cluster_id":"` +
		testClusterID + `","created_at":"2024-03-15T10:30:00Z",` +
		`"nodes":[` + strings.Join(nodes, ",") + `]}`
}

var (
	publicNode  = byocNodeConnJSON("n1", "", "host", testPublicHost)
	privateNode = byocNodeConnJSON("n2", "east", "internal_host",
		testPrivateHost)
)

func runConnString(t *testing.T, format, body string, args ...string,
) (stdout, stderr string, err error) {
	t.Helper()
	rt, out, errOut := testsupport.NewRuntime(t, "", format)
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, body))
	full := append([]string{"database", "connection-string",
		testDatabaseID}, args...)
	err = runAuthed(t, rt, out, url, full...)
	return out.String(), errOut.String(), err
}

func requireExitCode(t *testing.T, err error, code int) {
	t.Helper()
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != code {
		t.Fatalf("want exit %d, got %v", code, err)
	}
}

func TestByocConnectionStringSingleNodeNeedsNoFlag(t *testing.T) {
	out, errOut, err := runConnString(t, "text",
		byocDatabaseWithNodesJSON(publicNode))
	if err != nil {
		t.Fatal(err)
	}
	want := "postgresql://admin:p%40ss%3Aw%2Frd%3F%23&%27x%5Cy%C2%A0z@" +
		testPublicHost + ":5432/defaultdb?sslmode=require\n"
	if out != want {
		t.Errorf("uri:\n got %q\nwant %q", out, want)
	}
	if errOut != "" {
		t.Errorf("stderr not empty for a non-terminal stdout: %q", errOut)
	}
}

func TestByocConnectionStringSeveralNodesListsAndExitsTwo(t *testing.T) {
	out, _, err := runConnString(t, "text",
		byocDatabaseWithNodesJSON(publicNode, privateNode))
	requireExitCode(t, err, ExitUsage)
	if !strings.Contains(err.Error(), "--node") {
		t.Errorf("error does not name the flag: %v", err)
	}
	for _, want := range []string{"NODE", "NAME", "n1", "n2", "east",
		testPublicHost, testPrivateHost, "5432"} {
		if !strings.Contains(out, want) {
			t.Errorf("node table lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "p@ss") || strings.Contains(out, "p%40ss") {
		t.Errorf("node table printed a password:\n%s", out)
	}
	// Each cell under its own header: the private node's label is
	// "east" and its name "n2", so the row must read label then name.
	var row string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, testPrivateHost) {
			row = l
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(row), "east") ||
		strings.Index(row, "east") > strings.Index(row, "n2") {
		t.Errorf("label and name cells out of order: %q", row)
	}
	// The same rows under -o json carry node and name keys.
	jsonOut, _, err := runConnString(t, "json",
		byocDatabaseWithNodesJSON(publicNode, privateNode))
	requireExitCode(t, err, ExitUsage)
	// The buffer also holds cobra's usage text after the value, so
	// decode the first value only.
	var rows []map[string]any
	if err := json.NewDecoder(strings.NewReader(jsonOut)).Decode(
		&rows); err != nil {
		t.Fatalf("listing is not a JSON array: %v\n%s", err, jsonOut)
	}
	if len(rows) != 2 || rows[1]["node"] != "east" || rows[1]["name"] != "n2" {
		t.Errorf("json rows = %v", rows)
	}
}

func TestByocConnectionStringNodeSelects(t *testing.T) {
	db := byocDatabaseWithNodesJSON(publicNode, privateNode)
	t.Run("by name", func(t *testing.T) {
		out, _, err := runConnString(t, "text", db, "--node", "n1",
			"--no-password")
		if err != nil {
			t.Fatal(err)
		}
		if out != "postgresql://admin@"+testPublicHost+
			":5432/defaultdb?sslmode=require\n" {
			t.Errorf("uri = %q", out)
		}
	})
	t.Run("by logical name with --internal", func(t *testing.T) {
		out, _, err := runConnString(t, "text", db, "--node", "east",
			"--internal", "--no-password")
		if err != nil {
			t.Fatal(err)
		}
		if out != "postgresql://admin@"+testPrivateHost+
			":5432/defaultdb?sslmode=require\n" {
			t.Errorf("uri = %q", out)
		}
	})
	t.Run("unknown node is exit 2 and lists the names", func(t *testing.T) {
		_, _, err := runConnString(t, "text", db, "--node", "n9")
		requireExitCode(t, err, ExitUsage)
		if !strings.Contains(err.Error(), "east, n1") {
			t.Errorf("error does not list the nodes: %v", err)
		}
	})
	t.Run("unknown node on a single-node database is still exit 2",
		func(t *testing.T) {
			_, _, err := runConnString(t, "text",
				byocDatabaseWithNodesJSON(publicNode), "--node", "n9")
			requireExitCode(t, err, ExitUsage)
		})
	t.Run("a label two nodes answer to is refused", func(t *testing.T) {
		twin := byocNodeConnJSON("n3", "east", "host", "n3.example")
		_, _, err := runConnString(t, "text",
			byocDatabaseWithNodesJSON(publicNode, privateNode, twin),
			"--node", "east")
		requireExitCode(t, err, ExitUsage)
		if !strings.Contains(err.Error(), "2 nodes answering") {
			t.Errorf("error: %v", err)
		}
		// The unambiguous name of either twin still works.
		out, _, err := runConnString(t, "text",
			byocDatabaseWithNodesJSON(publicNode, privateNode, twin),
			"--node", "n3", "--no-password")
		if err != nil || !strings.Contains(out, "n3.example") {
			t.Errorf("n3 by name: %v %q", err, out)
		}
	})
	t.Run("empty node is exit 2 before the server", func(t *testing.T) {
		called := false
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		err := runAuthed(t, rt, out, url, "database", "connection-string",
			testDatabaseID, "--node", "")
		requireExitCode(t, err, ExitUsage)
		if called {
			t.Error("a refused invocation reached the server")
		}
	})
}

// TestByocConnectionStringCannotForgeALine is the pin for the
// output.Sanitize calls on node labels and hosts in error text: the
// stderr gate reads Fprintf to a governed writer, not Sprintf into an
// error, so reverting a call here leaves the suite green.
func TestByocConnectionStringCannotForgeALine(t *testing.T) {
	forged := byocNodeConnJSON("n1", "east\nFORGED", "internal_host",
		"h.internal\nFORGED2")
	t.Run("host refusal", func(t *testing.T) {
		_, _, err := runConnString(t, "text",
			byocDatabaseWithNodesJSON(forged))
		requireExitCode(t, err, ExitGeneral)
		if strings.Contains(err.Error(), "\n") {
			t.Errorf("error carries a raw newline: %q", err.Error())
		}
		if !strings.Contains(err.Error(), `east\nFORGED`) ||
			!strings.Contains(err.Error(), `h.internal\nFORGED2`) {
			t.Errorf("label or host not escaped: %q", err.Error())
		}
	})
	t.Run("internal refusal and listing", func(t *testing.T) {
		pub := byocNodeConnJSON("n9\nFORGED4", "", "host", "h.pub\nFORGED5")
		_, _, err := runConnString(t, "text",
			byocDatabaseWithNodesJSON(pub), "--internal")
		requireExitCode(t, err, ExitGeneral)
		if strings.Contains(err.Error(), "\n") {
			t.Errorf("error carries a raw newline: %q", err.Error())
		}
		out, _, err := runConnString(t, "text",
			byocDatabaseWithNodesJSON(pub, publicNode))
		requireExitCode(t, err, ExitUsage)
		table := out
		if i := strings.Index(out, "Error:"); i >= 0 {
			table = out[:i]
		}
		if strings.Count(strings.TrimRight(table, "\n"), "\n") != 2 {
			t.Errorf("a cell forged a line, want header + 2 rows:\n%s", table)
		}
	})
	t.Run("unknown node lists labels escaped", func(t *testing.T) {
		_, _, err := runConnString(t, "text",
			byocDatabaseWithNodesJSON(forged, publicNode), "--node",
			"zz\nFORGED3")
		requireExitCode(t, err, ExitUsage)
		if strings.Contains(err.Error(), "\n") {
			t.Errorf("error carries a raw newline: %q", err.Error())
		}
	})
}

// The network is chosen, never switched: a node advertising only the
// other network's host is refused with the flag that would reach it.
func TestByocConnectionStringRefusesTheOtherNetwork(t *testing.T) {
	t.Run("private node without --internal", func(t *testing.T) {
		_, _, err := runConnString(t, "text",
			byocDatabaseWithNodesJSON(privateNode))
		requireExitCode(t, err, ExitGeneral)
		for _, want := range []string{"--internal", testPrivateHost,
			"no host"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error lacks %q: %v", want, err)
			}
		}
	})
	t.Run("public node with --internal", func(t *testing.T) {
		_, _, err := runConnString(t, "text",
			byocDatabaseWithNodesJSON(publicNode), "--internal")
		requireExitCode(t, err, ExitGeneral)
		for _, want := range []string{"no internal_host", testPublicHost} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error lacks %q: %v", want, err)
			}
		}
	})
	t.Run("node with neither host", func(t *testing.T) {
		_, _, err := runConnString(t, "text",
			byocDatabaseWithNodesJSON(strings.Replace(publicNode,
				`"host":"`+testPublicHost+`"`, `"host":""`, 1)))
		requireExitCode(t, err, ExitGeneral)
		if !strings.Contains(err.Error(), "no connection host yet") {
			t.Errorf("error: %v", err)
		}
	})
	t.Run("no nodes", func(t *testing.T) {
		_, _, err := runConnString(t, "text", databaseBody)
		requireExitCode(t, err, ExitGeneral)
		if !strings.Contains(err.Error(), "no nodes yet") {
			t.Errorf("error: %v", err)
		}
	})
}

func TestByocConnectionStringEnvAndJSON(t *testing.T) {
	db := byocDatabaseWithNodesJSON(privateNode)
	t.Run("env", func(t *testing.T) {
		out, _, err := runConnString(t, "text", db, "--internal",
			"--format", "env", "--no-password")
		if err != nil {
			t.Fatal(err)
		}
		want := "PGHOST='" + testPrivateHost + "'\nPGPORT='5432'\n" +
			"PGDATABASE='defaultdb'\nPGUSER='admin'\nPGSSLMODE='require'\n"
		if out != want {
			t.Errorf("env:\n got %q\nwant %q", out, want)
		}
	})
	t.Run("json carries the node and the parts", func(t *testing.T) {
		out, _, err := runConnString(t, "json", db, "--internal")
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if got["node"] != "east" || got["host"] != testPrivateHost ||
			got["password"] != testConnPassword ||
			got["sslmode"] != "require" {
			t.Errorf("json = %v", got)
		}
		if !strings.HasPrefix(got["uri"].(string), "postgresql://admin:") {
			t.Errorf("uri = %v", got["uri"])
		}
	})
	t.Run("unknown format is exit 2", func(t *testing.T) {
		_, _, err := runConnString(t, "text", db, "--format", "dsn")
		requireExitCode(t, err, ExitUsage)
	})
}
