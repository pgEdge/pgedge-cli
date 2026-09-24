package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureSpec builds a minimal published contract for one product. The
// maxLength probe is deliberate: a capture routed through
// encoding/json's default decoding would round it through float64 and
// emit `maxLength: 25.0` — the same class of mangling `-o json`
// guards against — so the tests assert the literal `25` survives.
func fixtureSpec(product string) string {
	return `{
		"components": {"schemas": {"Thing": {
			"properties": {"name": {"maxLength": 25, "type": "string"},
				"id": {"type": "string", "x-go-type": "UUID"}},
			"type": "object"}}},
		"info": {"title": "` + product + `", "version": "1"},
		"openapi": "3.0.3",
		"paths": {
			"/` + product + `/v1/openapi.json": {"get": {"responses": {"200": {"description": "the contract"}}}},
			"/` + product + `/v1/things": {"get": {"responses": {"200": {"description": "ok"}}}}
		},
		"servers": [{"url": "https://api.pgedge.com"}]
	}`
}

// serve starts a test server publishing one contract per product,
// with per-product overrides for the sabotage cases.
func serve(t *testing.T, override map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			for _, p := range products {
				if r.URL.Path == p.prefix+"openapi.json" {
					if body, ok := override[p.name]; ok {
						if body == "" {
							http.NotFound(w, r)
							return
						}
						fmt.Fprint(w, body)
						return
					}
					fmt.Fprint(w, fixtureSpec(p.name))
					return
				}
			}
			http.NotFound(w, r)
		}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCaptureWritesEveryProductsContract(t *testing.T) {
	srv := serve(t, nil)
	dir := t.TempDir()

	if err := run(srv.URL, dir, false, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, p := range products {
		raw, err := os.ReadFile(filepath.Join(dir, p.name+".yaml"))
		if err != nil {
			t.Fatalf("read %s: %v", p.name, err)
		}
		body := string(raw)
		if !strings.Contains(body, "/"+p.name+"/v1/things") {
			t.Errorf("%s.yaml is missing its own path", p.name)
		}
		if !strings.Contains(body, "maxLength: 25\n") {
			t.Errorf("%s.yaml mangled the integer: want `maxLength: 25`, "+
				"got %q around it", p.name, body)
		}
	}
}

// TestCaptureIsFailClosed covers the three refusals, and that each one
// leaves the out directory EMPTY — validation of every product happens
// before any file is written, so a sabotaged third contract cannot
// leave the first two silently updated.
func TestCaptureIsFailClosed(t *testing.T) {
	sabotage := map[string]struct {
		body string
		want string
	}{
		"a pgedge visibility marker": {
			body: strings.Replace(fixtureSpec("managed"),
				`"type": "object"`,
				`"type": "object", "x-pgedge-plan": ["enterprise"]`, 1),
			want: "x-pgedge-plan",
		},
		"an unbindable go-type import": {
			body: strings.Replace(fixtureSpec("managed"),
				`"x-go-type": "UUID"`,
				`"x-go-type": "UUID", "x-go-type-import": {"path": "example.com/internal/oapi"}`, 1),
			want: "x-go-type-import",
		},
		"a path outside the product namespace": {
			body: strings.Replace(fixtureSpec("managed"),
				`"/managed/v1/things"`, `"/v1/things"`, 1),
			want: "/v1/things",
		},
		"an empty contract": {
			body: `{"openapi": "3.0.3", "paths": {}}`,
			want: "no paths",
		},
		"a non-200 answer": {
			body: "",
			want: "404",
		},
	}

	for name, tc := range sabotage {
		t.Run(name, func(t *testing.T) {
			srv := serve(t, map[string]string{"managed": tc.body})
			dir := t.TempDir()

			err := run(srv.URL, dir, false, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run = %v, want an error naming %q", err, tc.want)
			}
			left, readErr := os.ReadDir(dir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(left) != 0 {
				t.Errorf("a refused capture wrote %d file(s); the out "+
					"directory must stay untouched", len(left))
			}
		})
	}
}

func TestCheckPassesWhenCurrentAndWritesNothing(t *testing.T) {
	srv := serve(t, nil)
	dir := t.TempDir()
	if err := run(srv.URL, dir, false, io.Discard); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := run(srv.URL, dir, true, &out); err != nil {
		t.Fatalf("check over a fresh capture reported drift: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("check over a fresh capture printed %q, want nothing",
			out.String())
	}
}

// A check compares the contract, not the host. The same paths and
// schemas under a different servers URL are not drift: a check that
// failed on that line alone said nothing about the rest, which is
// what the comparison is run for.
func TestCheckComparesTheContractNotTheHost(t *testing.T) {
	prod := serve(t, nil)
	dir := t.TempDir()
	if err := run(prod.URL, dir, false, io.Discard); err != nil {
		t.Fatal(err)
	}

	rehosted := map[string]string{}
	for _, p := range products {
		rehosted[p.name] = strings.Replace(fixtureSpec(p.name),
			"https://api.pgedge.com", "https://staging.example", 1)
	}
	dev := serve(t, rehosted)
	var out strings.Builder
	if err := run(dev.URL, dir, true, &out); err != nil {
		t.Fatalf("check against a rehosted contract = %v, want none", err)
	}
	notes := strings.Count(out.String(), "servers differ")
	if notes != len(products) {
		t.Fatalf("servers notes = %d, want one per product (%d):\n%s",
			notes, len(products), out.String())
	}
	if !strings.Contains(out.String(), "https://staging.example") {
		t.Errorf("note does not name the published host:\n%s", out.String())
	}

	// Positive controls on two axes of the document, a path and a
	// schema property, each with the host change still in place: the
	// exemption must cover the servers block and nothing beside it.
	rehosted["byoc"] = strings.Replace(rehosted["byoc"],
		"/byoc/v1/things", "/byoc/v1/widgets", 1)
	if !strings.Contains(rehosted["byoc"], "/byoc/v1/widgets") {
		t.Fatal("path mutation did not land")
	}
	changed := serve(t, rehosted)
	err := run(changed.URL, dir, true, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "byoc.yaml") ||
		strings.Contains(err.Error(), "account.yaml") {
		t.Fatalf("check = %v, want drift naming byoc.yaml alone", err)
	}

	rehosted["account"] = strings.Replace(rehosted["account"],
		`"maxLength": 25`, `"maxLength": 26`, 1)
	if !strings.Contains(rehosted["account"], `"maxLength": 26`) {
		t.Fatal("schema mutation did not land")
	}
	changed = serve(t, rehosted)
	err = run(changed.URL, dir, true, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "account.yaml") ||
		strings.Contains(err.Error(), "managed.yaml") {
		t.Fatalf("check = %v, want drift naming account.yaml too", err)
	}
}

// A servers difference the URLs do not show still has to be legible,
// or the note prints one list twice and reads as noise.
func TestServersNoteShowsANonURLDifference(t *testing.T) {
	srv := serve(t, nil)
	dir := t.TempDir()
	if err := run(srv.URL, dir, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	described := map[string]string{"account": strings.Replace(
		fixtureSpec("account"), `{"url": "https://api.pgedge.com"}`,
		`{"url": "https://api.pgedge.com", "description": "prod-edge"}`, 1)}
	if !strings.Contains(described["account"], "prod-edge") {
		t.Fatal("mutation did not land")
	}
	var out strings.Builder
	if err := run(serve(t, described).URL, dir, true, &out); err != nil {
		t.Fatalf("check = %v, want none", err)
	}
	if strings.Count(out.String(), "servers differ") != 1 ||
		!strings.Contains(out.String(), "prod-edge") {
		t.Fatalf("note = %q, want one line naming the description", out.String())
	}
}

func TestCheckReportsDriftWithoutRewriting(t *testing.T) {
	srv := serve(t, nil)
	dir := t.TempDir()
	if err := run(srv.URL, dir, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "byoc.yaml")
	if err := os.WriteFile(stale, []byte("paths: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := run(srv.URL, dir, true, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "byoc.yaml") {
		t.Fatalf("check = %v, want drift naming byoc.yaml", err)
	}
	raw, readErr := os.ReadFile(stale)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != "paths: {}\n" {
		t.Error("check rewrote the drifted file; it must only report")
	}
}

// A missing file is drift, not an error — the state a fresh checkout
// of a new product would be in.
func TestCheckTreatsAMissingFileAsDrift(t *testing.T) {
	srv := serve(t, nil)
	dir := t.TempDir()
	if err := run(srv.URL, dir, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "account.yaml")); err != nil {
		t.Fatal(err)
	}

	err := run(srv.URL, dir, true, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "account.yaml") {
		t.Fatalf("check = %v, want drift naming account.yaml", err)
	}
}

// TestForbiddenExtensionIsCaughtAtAnyDepth pins the guard's walk: a
// marker buried inside a schema property, not only at path-item level
// where the API has placed them.
func TestForbiddenExtensionIsCaughtAtAnyDepth(t *testing.T) {
	deep := strings.Replace(fixtureSpec("byoc"),
		`"maxLength": 25`,
		`"maxLength": 25, "x-pgedge-omit-public": true`, 1)
	srv := serve(t, map[string]string{"byoc": deep})

	err := run(srv.URL, t.TempDir(), false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "x-pgedge-omit-public") {
		t.Fatalf("run = %v, want an error naming the buried marker", err)
	}
}

// TestProductListCoversEveryVendoredSpec pins the removal direction
// the products doc comment does not: dropping a product from the
// list silently ends drift coverage for its committed spec, so
// `vendor-spec-check` passes over a sabotaged byoc.yaml with byoc
// removed. Both directions are
// asserted — a spec with no product entry, and a product entry with
// no committed spec.
func TestProductListCoversEveryVendoredSpec(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "openapi"))
	if err != nil {
		t.Fatal(err)
	}
	specs := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") ||
			strings.HasPrefix(name, "generate-") {
			continue
		}
		specs[strings.TrimSuffix(name, ".yaml")] = true
	}
	if len(specs) == 0 {
		t.Fatal("no vendored specs found — the walk is broken and " +
			"this gate is checking nothing")
	}

	listed := map[string]bool{}
	for _, p := range products {
		listed[p.name] = true
		if !specs[p.name] {
			t.Errorf("products lists %q but openapi/%s.yaml is not "+
				"committed", p.name, p.name)
		}
	}
	for name := range specs {
		if !listed[name] {
			t.Errorf("openapi/%s.yaml is committed but %q is not in "+
				"products — vendor-spec-check no longer covers it",
				name, name)
		}
	}
}

// TestForbiddenExtensionIsCaughtInsideAnArray covers the []any branch
// of the walk, which the buried-marker test does not reach: the
// API's servers block is a sequence, and a marker planted
// on one of its elements must still abort the capture.
func TestForbiddenExtensionIsCaughtInsideAnArray(t *testing.T) {
	inArray := strings.Replace(fixtureSpec("byoc"),
		`{"url": "https://api.pgedge.com"}`,
		`{"url": "https://api.pgedge.com", "x-pgedge-internal": true}`, 1)
	if inArray == fixtureSpec("byoc") {
		t.Fatal("the fixture edit did not land")
	}
	srv := serve(t, map[string]string{"byoc": inArray})

	err := run(srv.URL, t.TempDir(), false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "/servers/0/") {
		t.Fatalf("run = %v, want the marker located inside the "+
			"servers array", err)
	}
}
