package cmd

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// databaseLogsBody is a verbatim prod response, trimmed to two lines.
// Note the shape: an OBJECT whose "logs" holds one block per node, each
// block carrying its node name and its lines as raw strings.
const databaseLogsBody = `{"logs":[{"logs":[` +
	`"2026-07-29 23:15:24.245 UTC [34] LOG:  checkpoint starting: time",` +
	`"2026-07-29 23:15:24.263 UTC [34] LOG:  checkpoint complete"` +
	`],"node":"n1"}]}`

// databaseLogsTwoNodeBody carries a block per node, which is what
// --nodes n1,n2 returns.
const databaseLogsTwoNodeBody = `{"logs":[` +
	`{"logs":["n1 line one"],"node":"n1"},` +
	`{"logs":["n2 line one"],"node":"n2"}]}`

// TestDatabaseLogsTypedShapeMatchesAPI pins the reason `database logs`
// calls the TYPED generated operation, and is the inverse of the check
// that used to live here.
//
// The spec once declared this 200 as an ARRAY of DatabaseLogsResponse
// while the handler returned a single one, so the generated parser
// rejected a real success and the command decoded the body itself. saas
// fixed the wrapper, the vendored spec carries the fix, and the typed
// path is now the one the command uses.
//
// The predecessor could never have caught that fix. It unmarshalled into
// a hardcoded `[]api.DatabaseLogsResponse` rather than into the generated
// response field's own type, so it asserted its own premise and passed
// whatever the spec said — on a re-vendor AND on a regeneration. This
// reads the generated field type, so a regression back to an array fails
// here.
func TestDatabaseLogsTypedShapeMatchesAPI(t *testing.T) {
	field, ok := reflect.TypeOf(api.GetDatabaseLogsResponse{}).
		FieldByName("JSON200")
	if !ok {
		t.Fatal("GetDatabaseLogsResponse has no JSON200 field")
	}
	if want := reflect.TypeOf(&api.DatabaseLogsResponse{}); field.Type != want {
		t.Errorf("JSON200 is %s, want %s; if the spec regressed to an "+
			"array, the command needs its untyped decode back",
			field.Type, want)
	}

	// The typed path must actually parse a real body, since the command
	// now depends on it rather than decoding the body itself.
	var typed api.DatabaseLogsResponse
	if err := json.Unmarshal([]byte(databaseLogsBody), &typed); err != nil {
		t.Fatalf("generated type does not parse a real response: %v", err)
	}
	if len(typed.Logs) != 1 {
		t.Fatalf("fixture parsed to %d blocks, want 1", len(typed.Logs))
	}

	// The blocks themselves are still bare `type: object`, which is why
	// databaseLogBlocksFrom exists. If the spec ever describes them, this
	// conversion becomes redundant and can go.
	blocks := databaseLogBlocksFrom(typed.Logs)
	if len(blocks) != 1 || blocks[0].Node != "n1" ||
		len(blocks[0].Logs) != 2 {
		t.Errorf("converted to %+v, want one n1 block of 2 lines", blocks)
	}
}

// TestDatabaseLogBlocksFromToleratesPartialBlocks covers the branch the
// spec makes reachable: nothing constrains a block, so a missing or
// wrongly typed key must yield an empty section rather than a panic.
func TestDatabaseLogBlocksFromToleratesPartialBlocks(t *testing.T) {
	blocks := databaseLogBlocksFrom([]map[string]interface{}{
		{"node": "n1", "logs": []interface{}{"one", 2, "three"}},
		{"node": 7},
		{"logs": "not-a-list"},
		{},
	})
	if len(blocks) != 4 {
		t.Fatalf("got %d blocks, want 4", len(blocks))
	}
	// Non-string lines are skipped, not coerced.
	if blocks[0].Node != "n1" || len(blocks[0].Logs) != 2 {
		t.Errorf("block 0 = %+v, want n1 with 2 string lines", blocks[0])
	}
	for i, b := range blocks[1:] {
		if b.Node != "" || len(b.Logs) != 0 {
			t.Errorf("block %d = %+v, want empty", i+1, b)
		}
	}
}

func TestDatabaseLogsRun(t *testing.T) {
	t.Run("text prints raw lines under a node header", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, databaseLogsBody))
		if err := runAuthed(t, rt, out, url, "database", "logs",
			testDatabaseID, "--component-name", "postgres",
			"--nodes", "n1"); err != nil {
			t.Fatalf("database logs: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "==> n1 <==") {
			t.Errorf("missing node header: %q", got)
		}
		if !strings.Contains(got, "checkpoint starting: time") {
			t.Errorf("missing log line: %q", got)
		}
		if !strings.Contains(got, "checkpoint complete") {
			t.Errorf("missing second log line: %q", got)
		}
	})

	t.Run("text prints one header per node block", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, databaseLogsTwoNodeBody))
		if err := runAuthed(t, rt, out, url, "database", "logs",
			testDatabaseID, "--component-name", "postgres",
			"--nodes", "n1,n2"); err != nil {
			t.Fatalf("database logs two nodes: %v", err)
		}
		got := out.String()
		for _, want := range []string{
			"==> n1 <==", "n1 line one", "==> n2 <==", "n2 line one",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q: %q", want, got)
			}
		}
	})

	t.Run("json carries the blocks", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, databaseLogsBody))
		if err := runAuthed(t, rt, out, url, "database", "logs",
			testDatabaseID, "--component-name", "postgres",
			"--nodes", "n1"); err != nil {
			t.Fatalf("database logs json: %v", err)
		}
		var got api.DatabaseLogsResponse
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("output is not the generated body: %v (%q)",
				err, out.String())
		}
		if len(got.Logs) != 1 || got.Logs[0]["node"] != "n1" {
			t.Errorf("json output = %+v, want one n1 block", got)
		}
	})

	// The API answers an unknown component_name, and an unknown node
	// name, with exactly this — a 200 carrying no blocks. It is
	// indistinguishable from a real component that has logged nothing,
	// so the notice must not claim the name was wrong.
	t.Run("empty reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `{"logs":[]}`))
		if err := runAuthed(t, rt, out, url, "database", "logs",
			testDatabaseID, "--component-name", "zzz",
			"--nodes", "n1"); err != nil {
			t.Fatalf("database logs empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No logs found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	// Both query parameters are required by the API — a call missing
	// either answers 400 — so the CLI requires them rather than
	// spending the round trip. No server is started: a required-flag
	// error must be reported before any request is made.
	t.Run("component-name is required", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		err := runByoc(t, rt, out, "database", "logs", testDatabaseID,
			"--nodes", "n1")
		if err == nil {
			t.Fatal("expected an error without --component-name")
		}
		if !strings.Contains(err.Error(), "component-name") {
			t.Errorf("error should name the flag: %v", err)
		}
	})

	t.Run("nodes is required", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		err := runByoc(t, rt, out, "database", "logs", testDatabaseID,
			"--component-name", "postgres")
		if err == nil {
			t.Fatal("expected an error without --nodes")
		}
		if !strings.Contains(err.Error(), "nodes") {
			t.Errorf("error should name the flag: %v", err)
		}
	})

	t.Run("api error surfaces", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(400,
			`{"message":"Invalid format for parameter component_name"}`))
		if err := runAuthed(t, rt, out, url, "database", "logs",
			testDatabaseID, "--component-name", "postgres",
			"--nodes", "n1"); err == nil {
			t.Fatal("expected an error on 400")
		}
	})

	t.Run("unparseable body surfaces", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `not json`))
		if err := runAuthed(t, rt, out, url, "database", "logs",
			testDatabaseID, "--component-name", "postgres",
			"--nodes", "n1"); err == nil {
			t.Fatal("expected an error on an unparseable 200")
		}
	})

	// max_lines is optional; the request must carry it only when asked
	// for, and carry the two required filters every time.
	t.Run("query parameters reach the API", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var gotQuery string
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(databaseLogsBody))
			})
		if err := runAuthed(t, rt, out, url, "database", "logs",
			testDatabaseID, "--component-name", "postgres",
			"--nodes", "n1,n2", "--max-lines", "7"); err != nil {
			t.Fatalf("database logs with filters: %v", err)
		}
		for _, want := range []string{
			"component_name=postgres", "nodes=n1%2Cn2", "max_lines=7",
		} {
			if !strings.Contains(gotQuery, want) {
				t.Errorf("query %q missing %q", gotQuery, want)
			}
		}
	})
}
