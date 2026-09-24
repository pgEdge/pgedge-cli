package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	api "github.com/pgEdge/pgedge-cli/internal/starfleet/account/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const testAPIClientID = "11111111-2222-3333-4444-555566667777"

const apiClientBody = `{"id":"` + testAPIClientID + `",` +
	`"name":"ci","description":"CI runner","auth0_id":"auth0|abc",` +
	`"created_at":"2026-07-01T00:00:00Z",` +
	`"updated_at":"2026-07-02T00:00:00Z"}`

// createdSecret is the fixture secret VALUE. Every suppression check
// below asserts on this value rather than on the key name
// "auth0_secret", because the key name is a far weaker signal than it
// looks: the yaml renderer spells the same field "auth0secret" (no
// underscore, see TestClientYAMLKeySpelling), and a table column that
// printed the secret under any other header would carry the value
// without the key ever appearing. A key-name check sleeps through both.
const createdSecret = "SUPERSECRET"

// apiClientCreatedBody is the response body POST /account/v1/clients is
// specified to return: a CreateApiClientResponse, which carries every
// ApiClient field plus auth0_secret. The reads are deliberately fed this
// same body even though they answer the narrower ApiClient, so their
// suppression checks are exercised against a body that DOES contain a
// secret. It is spec-derived, not observed — the dev tenant is shared,
// so no client was created to watch the real response.
const apiClientCreatedBody = `{"id":"` + testAPIClientID + `",` +
	`"name":"ci","description":"CI","auth0_id":"auth0|abc",` +
	`"auth0_secret":"` + createdSecret + `",` +
	`"created_at":"2026-07-01T00:00:00Z",` +
	`"updated_at":"2026-07-01T00:00:00Z"}`

func TestClientListRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var gotPath string
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			testsupport.JSONHandler(200,
				`[`+apiClientCreatedBody+`]`)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url,
			"client", "list"); err != nil {
			t.Fatalf("client list: %v", err)
		}
		if !strings.Contains(out.String(), "ci") {
			t.Errorf("missing client name: %q", out.String())
		}
		if gotPath != "/account/v1/clients" {
			t.Errorf("path = %q, want /account/v1/clients", gotPath)
		}
		// ApiClient has no secret field, so a body that carries one
		// cannot put the VALUE on stdout.
		if strings.Contains(out.String(), createdSecret) {
			t.Errorf("list leaked the secret value: %q", out.String())
		}
		// Second belt, not the primary: the json key spelling.
		if strings.Contains(out.String(), "auth0_secret") {
			t.Error("list rendered a secret column")
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[`+apiClientCreatedBody+`]`))
		if err := runAuthedAccount(t, rt, out, url,
			"client", "list"); err != nil {
			t.Fatalf("client list json: %v", err)
		}
		if !strings.Contains(out.String(), testAPIClientID) {
			t.Errorf("missing id: %q", out.String())
		}
		if strings.Contains(out.String(), createdSecret) {
			t.Errorf("list -o json leaked the secret value: %q",
				out.String())
		}
	})

	t.Run("empty list notices on stderr", func(t *testing.T) {
		rt, out, errBuf := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthedAccount(t, rt, out, url,
			"client", "list"); err != nil {
			t.Fatalf("client list empty: %v", err)
		}
		if !strings.Contains(errBuf.String(), "No API clients found") {
			t.Errorf("stderr = %q, want the empty notice",
				errBuf.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthedAccount(t, rt, out, url,
			"client", "list"); err == nil {
			t.Fatal("expected an error on 500")
		}
	})
}

func TestClientGetRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, apiClientCreatedBody))
		if err := runAuthedAccount(t, rt, out, url,
			"client", "get", testAPIClientID); err != nil {
			t.Fatalf("client get: %v", err)
		}
		if !strings.Contains(out.String(), "ci") {
			t.Errorf("missing client name: %q", out.String())
		}
		if strings.Contains(out.String(), createdSecret) {
			t.Errorf("get leaked the secret value: %q", out.String())
		}
		if strings.Contains(out.String(), "auth0_secret") {
			t.Error("get rendered a secret column")
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, apiClientCreatedBody))
		if err := runAuthedAccount(t, rt, out, url,
			"client", "get", testAPIClientID); err != nil {
			t.Fatalf("client get json: %v", err)
		}
		if !strings.Contains(out.String(), testAPIClientID) {
			t.Errorf("missing id: %q", out.String())
		}
		if strings.Contains(out.String(), createdSecret) {
			t.Errorf("get -o json leaked the secret value: %q",
				out.String())
		}
	})

	t.Run("invalid id fails before any request", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		called := false
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/account/v1/clients") {
				called = true
			}
			testsupport.JSONHandler(200, apiClientBody)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url,
			"client", "get", "not-a-uuid"); err == nil {
			t.Fatal("expected an error for a non-UUID id")
		}
		if called {
			t.Error("made an HTTP call despite an invalid id")
		}
	})

	// nil body covers the "no client data returned" branch: a 2xx
	// response that carries no parseable JSON200 body (the generated
	// parser only fills it for 200; 202 leaves it nil).
	t.Run("nil body", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(202, `{}`))
		if err := runAuthedAccount(t, rt, out, url,
			"client", "get", testAPIClientID); err != nil {
			t.Fatalf("client get nil body: %v", err)
		}
		if !strings.Contains(errb.String(), "No client data returned") {
			t.Errorf("missing nil-body message: %q", errb.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthedAccount(t, rt, out, url,
			"client", "get", testAPIClientID); err == nil {
			t.Fatal("expected an error on 500")
		}
	})
}

// TestReadResponseTypeCannotCarryASecret is the structural half of the
// property, and the reason apiclient.go no longer strips anything.
//
// Reads decode into api.ApiClient, which declares no secret field, so
// an auth0_secret in the body is discarded at unmarshal and no renderer
// can reach it. Three hand-written helpers used to clear the field
// instead, because the old spec shared one response type across
// POST/PATCH/GET; the API splits them, so the type system now enforces what
// those helpers did.
//
// This fails if a re-vendor puts a secret-shaped field back on the read
// type — which would silently restore the leak the helpers prevented,
// with nothing left in the code to stop it.
func TestReadResponseTypeCannotCarryASecret(t *testing.T) {
	readType := reflect.TypeOf(api.ApiClient{})
	for i := range readType.NumField() {
		name := strings.ToLower(readType.Field(i).Name)
		if strings.Contains(name, "secret") ||
			strings.Contains(name, "password") {
			t.Errorf("%s has field %s; reads render this type directly, "+
				"so a secret field on it leaks. Either restore a strip "+
				"helper or keep the field off the read schema",
				readType, readType.Field(i).Name)
		}
	}

	// Control: the search must find a secret where one really exists,
	// or the loop above proves nothing about ApiClient.
	ct := reflect.TypeOf(api.CreateApiClientResponse{})
	var found bool
	for i := range ct.NumField() {
		if strings.Contains(
			strings.ToLower(ct.Field(i).Name), "secret") {
			found = true
		}
	}
	if !found {
		t.Fatal("CreateApiClientResponse carries no secret field, so " +
			"the check above cannot detect one; create must still " +
			"return the minted secret")
	}
}

// TestClientSecretNeverRenderedByReads is the value-based half. Every
// case feeds a body that DOES carry auth0_secret and asserts the VALUE
// never reaches either stream, in all three output formats — so the
// property is covered end to end and not only at the type level.
//
// Asserting on the value rather than on the key name is the whole
// point. The predecessor of this check looked for the literal string
// "auth0_secret" and was proved toothless twice over: it can never
// match yaml's key spelling ("auth0secret"), and it passes unchanged
// when a table column prints the secret's value under some other
// header.
func TestClientSecretNeverRenderedByReads(t *testing.T) {
	verbs := []struct {
		name string
		args []string
		body string
	}{
		{"list", []string{"client", "list"},
			`[` + apiClientCreatedBody + `]`},
		{"get", []string{"client", "get", testAPIClientID},
			apiClientCreatedBody},
		{"update", []string{"client", "update", testAPIClientID,
			"--name", "renamed"}, apiClientCreatedBody},
	}
	formats := []string{"text", "json", "yaml"}
	for _, v := range verbs {
		for _, format := range formats {
			t.Run(v.name+" "+format, func(t *testing.T) {
				rt, out, errBuf := testsupport.NewRuntime(t, "", format)
				url := testsupport.NewAuthedServer(t,
					testsupport.JSONHandler(200, v.body))
				if err := runAuthedAccount(t, rt, out, url,
					v.args...); err != nil {
					t.Fatalf("client %s -o %s: %v", v.name, format, err)
				}
				if strings.Contains(out.String(), createdSecret) {
					t.Errorf("stdout leaked the secret value: %q",
						out.String())
				}
				if strings.Contains(errBuf.String(), createdSecret) {
					t.Errorf("stderr leaked the secret value: %q",
						errBuf.String())
				}
				// Second belt only: the json key spelling. It cannot
				// match yaml and is never a substitute for the value
				// assertions above.
				if strings.Contains(out.String(), "auth0_secret") {
					t.Errorf("stdout carried the auth0_secret key: %q",
						out.String())
				}
			})
		}
	}
}

// TestClientReadsPreserveNilShape pins the rendered shape of an empty
// result: a 2xx that leaves the generated JSON200 nil must still render
// as `null` under -o json. Returning an empty array or an empty object
// instead would be a silent shape change for every scripted caller.
//
// This outlived the strip helpers it was originally written to guard —
// the nil-vs-empty distinction is the renderer's, not theirs.
func TestClientReadsPreserveNilShape(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"list", []string{"client", "list"}},
		{"get", []string{"client", "get", testAPIClientID}},
		{"update", []string{"client", "update", testAPIClientID,
			"--name", "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			// 202: the generated parser fills JSON200 only for 200, so
			// this is the nil-body path.
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(202, `{}`))
			if err := runAuthedAccount(t, rt, out, url,
				tc.args...); err != nil {
				t.Fatalf("client %s -o json: %v", tc.name, err)
			}
			if strings.TrimSpace(out.String()) != "null" {
				t.Errorf("stdout = %q, want \"null\"", out.String())
			}
		})
	}
}

// TestClientYAMLKeySpelling gates the yaml key names the account
// reference documents. ApiClient carries json tags only, and the
// renderer normalizes through JSON before encoding yaml, so the keys
// are the API's own: auth0_id, auth0_secret, created_at, updated_at.
// They match -o json exactly, so `yq '.auth0_secret'` and
// `jq '.auth0_secret'` agree.
//
// Both halves matter. Asserting only that auth0_secret appears would
// pass on output that also carried the old auth0secret spelling, and
// asserting only the spelling would not notice the secret leaking from
// get.
func TestClientYAMLKeySpelling(t *testing.T) {
	t.Run("get uses API key names and omits the secret",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "yaml")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, apiClientCreatedBody))
			if err := runAuthedAccount(t, rt, out, url,
				"client", "get", testAPIClientID); err != nil {
				t.Fatalf("client get -o yaml: %v", err)
			}
			for _, key := range []string{
				"auth0_id:", "created_at:", "updated_at:",
				"description:", "name:",
			} {
				if !strings.Contains(out.String(), key) {
					t.Errorf("yaml output missing key %q: %q",
						key, out.String())
				}
			}
			// The old lowercased-Go-field-name spellings must be gone.
			for _, stale := range []string{
				"auth0id", "auth0secret", "createdat", "updatedat",
			} {
				if strings.Contains(out.String(), stale) {
					t.Errorf("yaml used the Go field name %q: %q",
						stale, out.String())
				}
			}
			// get never returns the minted secret. It is an omitempty
			// field, so with the secret absent the key is absent too —
			// which is exactly what -o json does.
			if strings.Contains(out.String(), createdSecret) {
				t.Error("get -o yaml leaked the minted secret")
			}
		})

	t.Run("create renders the secret under auth0_secret",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "yaml")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, apiClientCreatedBody))
			if err := runAuthedAccount(t, rt, out, url, "client",
				"create", "--name", "ci",
				"--description", "CI"); err != nil {
				t.Fatalf("client create -o yaml: %v", err)
			}
			if !strings.Contains(out.String(),
				"auth0_secret: "+createdSecret) {
				t.Errorf("yaml must carry `auth0_secret: <secret>`: %q",
					out.String())
			}
			if strings.Contains(out.String(), "auth0secret") {
				t.Errorf("yaml used the Go field name: %q", out.String())
			}
		})
}

// The minted secret is returned once and cannot be re-fetched. If the
// command fails to render it, the caller has lost it permanently and
// the client is useless.
func TestClientCreateRun(t *testing.T) {
	t.Run("text renders the secret once", func(t *testing.T) {
		rt, out, errBuf := testsupport.NewRuntime(t, "", "text")
		var gotBody, gotMethod string
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/account/v1/clients" &&
				r.Method == http.MethodPost {
				b, _ := io.ReadAll(r.Body)
				gotBody, gotMethod = string(b), r.Method
			}
			testsupport.JSONHandler(200, apiClientCreatedBody)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url, "client", "create",
			"--name", "ci", "--description", "CI"); err != nil {
			t.Fatalf("client create: %v", err)
		}
		if gotMethod != http.MethodPost {
			t.Errorf("method = %q, want POST", gotMethod)
		}
		if !strings.Contains(gotBody, `"name":"ci"`) ||
			!strings.Contains(gotBody, `"description":"CI"`) {
			t.Errorf("body = %q, want name and description", gotBody)
		}
		// The secret goes to STDOUT so it can be piped or redirected.
		if !strings.Contains(out.String(), createdSecret) {
			t.Errorf("stdout = %q, MUST carry the minted secret — it "+
				"cannot be retrieved again", out.String())
		}
		// The prose warning goes to stderr.
		if !strings.Contains(errBuf.String(), "shown once") {
			t.Errorf("stderr = %q, want the shown-once warning",
				errBuf.String())
		}
		// stdout must carry the secret and nothing that would corrupt
		// it in a `$(...)` capture: the surrounding prose is stderr's.
		if strings.TrimSpace(out.String()) != createdSecret {
			t.Errorf("stdout = %q, want exactly the secret so a "+
				"scripted caller can capture it", out.String())
		}
	})

	// -o json must emit the whole body, secret included, or a scripted
	// caller cannot capture what it just minted. This is the defect
	// class found in the three service deploys, which wrote nothing at
	// all under -o json.
	t.Run("json carries the secret", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, apiClientCreatedBody))
		if err := runAuthedAccount(t, rt, out, url, "client", "create",
			"--name", "ci", "--description", "CI"); err != nil {
			t.Fatalf("client create json: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("stdout is not JSON (%v): %q", err, out.String())
		}
		if got["auth0_secret"] != createdSecret {
			t.Errorf("json auth0_secret = %v, want %s",
				got["auth0_secret"], createdSecret)
		}
	})

	// An empty-body 200 means the secret is gone. Say so loudly
	// rather than reporting success.
	t.Run("missing secret warns", func(t *testing.T) {
		for _, format := range []string{"text", "json"} {
			t.Run(format, func(t *testing.T) {
				rt, out, errBuf := testsupport.NewRuntime(t, "", format)
				url := testsupport.NewAuthedServer(t,
					testsupport.JSONHandler(200, apiClientBody))
				if err := runAuthedAccount(t, rt, out, url,
					"client", "create", "--name", "ci",
					"--description", "CI"); err != nil {
					t.Fatalf("client create: %v", err)
				}
				if !strings.Contains(errBuf.String(),
					"no client secret") {
					t.Errorf("stderr = %q, want a missing-secret "+
						"warning", errBuf.String())
				}
			})
		}
	})

	// A 2xx with no parseable body leaves JSON200 nil, which means the
	// secret was minted server-side and is already unreachable. That is
	// an error in EVERY format: rendering `null` under -o json and
	// exiting 0 would tell a script it succeeded.
	t.Run("no body is an error in every format", func(t *testing.T) {
		for _, format := range []string{"text", "json", "yaml"} {
			t.Run(format, func(t *testing.T) {
				rt, out, _ := testsupport.NewRuntime(t, "", format)
				url := testsupport.NewAuthedServer(t,
					testsupport.JSONHandler(202, `{}`))
				err := runAuthedAccount(t, rt, out, url, "client",
					"create", "--name", "ci", "--description", "CI")
				if err == nil {
					t.Fatal("expected an error when no body came back")
				}
				if !strings.Contains(err.Error(), "unrecoverable") {
					t.Errorf("error = %q, want it to say the secret "+
						"is unrecoverable", err)
				}
				if strings.Contains(out.String(), "null") {
					t.Errorf("stdout = %q, must not render a body",
						out.String())
				}
			})
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(500, `boom`))
		if err := runAuthedAccount(t, rt, out, url, "client", "create",
			"--name", "ci", "--description", "CI"); err == nil {
			t.Fatal("expected an error on 500")
		}
	})
}

func TestClientUpdateRun(t *testing.T) {
	// UpdateApiClientInput has pointer fields precisely so an omitted
	// flag is omitted from the request — not sent as "".
	t.Run("sends only the changed field", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var gotBody, gotMethod string
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/account/v1/clients/") {
				b, _ := io.ReadAll(r.Body)
				gotBody, gotMethod = string(b), r.Method
			}
			testsupport.JSONHandler(200, apiClientBody)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url, "client", "update",
			testAPIClientID, "--name", "renamed"); err != nil {
			t.Fatalf("client update: %v", err)
		}
		if gotMethod != http.MethodPatch {
			t.Errorf("method = %q, want PATCH", gotMethod)
		}
		if !strings.Contains(gotBody, `"name":"renamed"`) {
			t.Errorf("body = %q, want the new name", gotBody)
		}
		if strings.Contains(gotBody, "description") {
			t.Errorf("body = %q, must omit description when "+
				"--description was not passed", gotBody)
		}
		if !strings.Contains(out.String(), "ci") {
			t.Errorf("stdout = %q, want the updated client row",
				out.String())
		}
	})

	t.Run("sends only description", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var gotBody string
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/account/v1/clients/") {
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
			}
			testsupport.JSONHandler(200, apiClientBody)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url, "client", "update",
			testAPIClientID, "--description", "new"); err != nil {
			t.Fatalf("client update: %v", err)
		}
		if !strings.Contains(gotBody, `"description":"new"`) {
			t.Errorf("body = %q, want the new description", gotBody)
		}
		if strings.Contains(gotBody, `"name"`) {
			t.Errorf("body = %q, must omit name when --name was not "+
				"passed", gotBody)
		}
	})

	t.Run("no flags is a usage error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		called := false
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/account/v1/clients/") {
				called = true
			}
			testsupport.JSONHandler(200, apiClientBody)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url, "client", "update",
			testAPIClientID); err == nil {
			t.Fatal("expected an error with no flags")
		}
		if called {
			t.Error("sent a PATCH with nothing to change")
		}
	})

	t.Run("invalid id fails before any request", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		called := false
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/account/v1/clients") {
				called = true
			}
			testsupport.JSONHandler(200, apiClientBody)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url, "client", "update",
			"not-a-uuid", "--name", "x"); err == nil {
			t.Fatal("expected an error for a non-UUID id")
		}
		if called {
			t.Error("made an HTTP call despite an invalid id")
		}
	})

	t.Run("nil body", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(202, `{}`))
		if err := runAuthedAccount(t, rt, out, url, "client", "update",
			testAPIClientID, "--name", "x"); err != nil {
			t.Fatalf("client update nil body: %v", err)
		}
		if !strings.Contains(errb.String(), "No client data returned") {
			t.Errorf("missing nil-body message: %q", errb.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(500, `boom`))
		if err := runAuthedAccount(t, rt, out, url, "client", "update",
			testAPIClientID, "--name", "x"); err == nil {
			t.Fatal("expected an error on 500")
		}
	})
}

// testsupport.JSONHandler(204, "") below is the FAITHFUL fixture for
// this endpoint, and using it is essential. Do not "simplify" it to
// a bare w.WriteHeader(http.StatusNoContent).
//
// The API answers this DELETE with a 204 that still carries the json
// Content-Type: net/http suppresses only Content-Length and
// Transfer-Encoding on a 204 — not Content-Type. So the wire shape is 204 +
// `Content-Type: application/json` + a 0-byte body, which is exactly
// what JSONHandler(204, "") produces.
//
// That shape lands on the `Content-Type contains "json" && true`
// catch-all every generated Parse*Delete*Response ends with, which
// unmarshals the body into an Error for ANY status, 2xx included.
// json.Unmarshal of 0 bytes fails, so DeleteClientWithResponse returns
// "unexpected end of JSON input" and a delete that SUCCEEDED is
// reported as a failure. newAPIClientDeleteCmd therefore bypasses that
// parser (see its comment); this fixture is what gates the bypass.
//
// This endpoint is the only one that needs it: every other no-content
// handler in starfleet goes through RespondNoContent -> ctx.NoContent,
// which sets no Content-Type, which is why invite delete, membership
// delete and every byoc delete have never tripped it.
func TestClientDeleteRun(t *testing.T) {
	// cli.Confirm returns a *cli.UsageError on non-TTY stdin without
	// --force, so a script fails loudly instead of hanging.
	t.Run("refuses without force on non-tty", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		called := false
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			called = true
			testsupport.JSONHandler(204, "")(w, r)
		})
		err := runAuthedAccount(t, rt, out, url,
			"client", "delete", testAPIClientID)
		if err == nil {
			t.Fatal("expected a refusal without --force")
		}
		if cli.ExitCode(err) != cli.ExitUsage {
			t.Errorf("exit code = %d, want ExitUsage",
				cli.ExitCode(err))
		}
		if called {
			t.Error("reached the API without confirmation")
		}
	})

	// Both 204 wire shapes must work. The json-content-type one is what
	// the API actually sends and is what the bypass exists for; the bare
	// one is what a spec-literal 204 looks like, and checkResponse
	// accepting any 2xx is what makes the bypass correct for both. The
	// Authorization assertion is the guard on the bypass itself: the
	// untyped DeleteClient still has to run the client-level request
	// editors, so dropping the bearer token here would be silent.
	// Read the expected token out of the shared fixture rather than
	// duplicating its literal, so this cannot drift from TokenBody.
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(
		[]byte(testsupport.TokenBody), &tok); err != nil {
		t.Fatalf("decode testsupport.TokenBody: %v", err)
	}
	shapes := []struct {
		name  string
		write http.HandlerFunc
	}{
		{"204 with a json content-type (what the API sends)",
			testsupport.JSONHandler(204, "")},
		{"204 with no content-type", func(
			w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}},
	}
	for _, shape := range shapes {
		t.Run("force deletes and renders nothing: "+shape.name,
			func(t *testing.T) {
				rt, out, errb := testsupport.NewRuntime(t, "", "text")
				var gotMethod, gotAuth string
				url := testsupport.NewAuthedServer(t, func(
					w http.ResponseWriter, r *http.Request) {
					if strings.Contains(r.URL.Path, "/account/v1/clients/") {
						gotMethod = r.Method
						gotAuth = r.Header.Get("Authorization")
					}
					shape.write(w, r)
				})
				if err := runAuthedAccount(t, rt, out, url, "client",
					"delete", testAPIClientID, "--force"); err != nil {
					t.Fatalf("client delete --force: %v", err)
				}
				if gotMethod != http.MethodDelete {
					t.Errorf("method = %q, want DELETE", gotMethod)
				}
				if gotAuth != "Bearer "+tok.AccessToken {
					t.Errorf("Authorization = %q, want the bearer "+
						"token — the untyped call must still run the "+
						"client's request editors", gotAuth)
				}
				// 204 => nothing on stdout.
				if strings.TrimSpace(out.String()) != "" {
					t.Errorf("stdout = %q, want empty for a 204",
						out.String())
				}
				if !strings.Contains(errb.String(), "deleted") {
					t.Errorf("stderr = %q, want a deletion confirmation",
						errb.String())
				}
			})
	}

	t.Run("invalid id fails before any request", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		called := false
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/account/v1/clients") {
				called = true
			}
			testsupport.JSONHandler(204, "")(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url, "client", "delete",
			"not-a-uuid", "--force"); err == nil {
			t.Fatal("expected an error for a non-UUID id")
		}
		if called {
			t.Error("made an HTTP call despite an invalid id")
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(500, `boom`))
		if err := runAuthedAccount(t, rt, out, url, "client", "delete",
			testAPIClientID, "--force"); err == nil {
			t.Fatal("expected an error on 500")
		}
	})
}
