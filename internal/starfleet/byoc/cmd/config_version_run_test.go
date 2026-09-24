package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// configVersionBody mirrors a real dev response: managed_extensions is
// explicitly null on the current 15.x line, so the renderer must cope
// with a nil slice rather than only with an absent field.
const configVersionBody = `{"name":"15.6.0",` +
	`"images":{"postgres":"pgedge/postgres:3.5-pg16"},` +
	`"managed_extensions":null,` +
	`"supported_pg_versions":["16","17","18"]}`

const configVersionWithExtBody = `{"name":"14.1.8",` +
	`"images":{"postgres":"pgedge/postgres:3.5-pg16"},` +
	`"managed_extensions":["postgis","vector"],` +
	`"supported_pg_versions":["16","17"]}`

func TestConfigVersionListRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
			200, `[`+configVersionBody+`,`+configVersionWithExtBody+`]`))
		if err := runAuthed(t, rt, out, url,
			"config-version", "list"); err != nil {
			t.Fatalf("config-version list: %v", err)
		}
		if !strings.Contains(out.String(), "15.6.0") {
			t.Errorf("missing version name: %q", out.String())
		}
		if !strings.Contains(out.String(), "16, 17, 18") {
			t.Errorf("missing supported pg versions: %q", out.String())
		}
		if !strings.Contains(out.String(), "postgis, vector") {
			t.Errorf("missing managed extensions: %q", out.String())
		}
	})

	// -o table is an alias for -o text, and the guard that honours it
	// is shared, so this pins the alias on the list path rather than
	// the guard (#475).
	t.Run("table matches text", func(t *testing.T) {
		body := `[` + configVersionBody + `,` + configVersionWithExtBody + `]`
		var got [2]string
		for i, format := range []string{"text", "table"} {
			rt, out, _ := testsupport.NewRuntime(t, "", format)
			url := testsupport.NewAuthedServer(t,
				testsupport.PathHandler(t, "/byoc/v1/config-versions", 200, body))
			if err := runAuthed(t, rt, out, url,
				"config-version", "list"); err != nil {
				t.Fatalf("config-version list -o %s: %v", format, err)
			}
			got[i] = out.String()
		}
		if got[1] == "" || got[0] != got[1] {
			t.Errorf("-o table printed %q, -o text printed %q", got[1], got[0])
		}
	})

	t.Run("empty json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url,
			"config-version", "list"); err != nil {
			t.Fatalf("config-version list json: %v", err)
		}
	})

	t.Run("empty text reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url,
			"config-version", "list"); err != nil {
			t.Fatalf("config-version list empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No config versions found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url,
			"config-version", "list"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestConfigVersionGetRun(t *testing.T) {
	// The stub answers only the version asked for, so a get that fetched
	// some other version would fail here rather than pass on a body it
	// was never entitled to (#475).
	const path = "/byoc/v1/config-versions/15.6.0"

	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.PathHandler(t, path, 200, configVersionBody))
		if err := runAuthed(t, rt, out, url,
			"config-version", "get", "15.6.0"); err != nil {
			t.Fatalf("config-version get: %v", err)
		}
		if !strings.Contains(out.String(), "15.6.0") {
			t.Errorf("missing version name: %q", out.String())
		}
	})

	t.Run("table matches text", func(t *testing.T) {
		var got [2]string
		for i, format := range []string{"text", "table"} {
			rt, out, _ := testsupport.NewRuntime(t, "", format)
			url := testsupport.NewAuthedServer(t,
				testsupport.PathHandler(t, path, 200, configVersionBody))
			if err := runAuthed(t, rt, out, url,
				"config-version", "get", "15.6.0"); err != nil {
				t.Fatalf("config-version get -o %s: %v", format, err)
			}
			got[i] = out.String()
		}
		if got[1] == "" || got[0] != got[1] {
			t.Errorf("-o table printed %q, -o text printed %q", got[1], got[0])
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.PathHandler(t, path, 200, configVersionBody))
		if err := runAuthed(t, rt, out, url,
			"config-version", "get", "15.6.0"); err != nil {
			t.Fatalf("config-version get json: %v", err)
		}
		if !strings.Contains(out.String(), "pgedge/postgres") {
			t.Errorf("json output should carry images: %q", out.String())
		}
	})

	// Dev answers an unknown version with 404 and a JSON message, not
	// with an empty 200, so the command must surface it as an error.
	t.Run("not found", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.PathHandler(t,
			"/byoc/v1/config-versions/nope", 404,
			`{"code":404,"message":"failed to find config version"}`))
		if err := runAuthed(t, rt, out, url,
			"config-version", "get", "nope"); err == nil {
			t.Fatal("expected error on 404")
		}
	})
}
