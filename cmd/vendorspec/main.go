// Command vendorspec captures the published per-product OpenAPI
// contracts into openapi/.
//
// It is the engine behind `make vendor-spec`. Each contract is served
// unauthenticated at {base}/{product}/v1/openapi.json, already
// filtered to the public, enterprise-maximal surface by the API
// itself — so what this tool fetches
// IS the public contract, and nothing here derives or filters.
//
//	go run ./cmd/vendorspec -out openapi
//	go run ./cmd/vendorspec -out openapi -check
//
// -base defaults to production because the vendored contract must be
// the one customers are served (TestVendoredSpecsAreCanonical pins the
// vendored servers URL there). -check fetches and reports drift
// without writing, and compares the contract rather than the host: a
// servers block that differs is printed as a note, not counted as
// drift, so a check against another environment answers for its paths
// and schemas. Both modes hit the network, so neither runs in CI.
//
// The tool is still fail-closed, in three ways, and every product is
// validated before any file is written: an `x-pgedge-*` key anywhere
// in a published contract aborts the run (the upstream filter should
// have removed it, so one appearing means that filter regressed); so
// does `x-go-type-import`, which names a package internal to the API
// server's Go module, which this module cannot import (a bare
// `x-go-type` is expected — it names UUID, which each module's
// api/types.go supplies); and so does a path
// outside the product's own namespace.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// product names one published contract and the path prefix every
// operation in it must sit under. The API asserts the same namespace
// invariant upstream, and TestVendoredSpecsAreCanonical asserts it over the committed
// artefacts; this list is deliberately fixed rather than discovered,
// so a new product is vendored on purpose or not at all.
type product struct{ name, prefix string }

var products = []product{
	{name: "account", prefix: "/account/v1/"},
	{name: "byoc", prefix: "/byoc/v1/"},
	{name: "managed", prefix: "/managed/v1/"},
}

// yamlIndent matches the vendored files already in openapi/, so a
// re-capture shows a content diff rather than a whole-file reformat.
const yamlIndent = 2

// maxSpecBytes bounds a fetched body. The largest contract is ~91 KB
// today; the cap only exists so a misrouted URL answering something
// enormous fails fast instead of being written to disk.
const maxSpecBytes = 32 << 20

func main() {
	base := flag.String("base", "https://api.pgedge.com",
		"API base URL publishing the per-product contracts")
	out := flag.String("out", "openapi",
		"directory to write the per-product specs into")
	check := flag.Bool("check", false,
		"report drift without writing")
	flag.Parse()

	if err := run(*base, *out, *check, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "vendorspec: %v\n", err)
		os.Exit(1)
	}
}

func run(base, outDir string, check bool, stdout io.Writer) error {
	client := &http.Client{Timeout: 60 * time.Second}

	// Fetch and validate every product BEFORE writing anything, so a
	// refused contract cannot leave a partial capture behind.
	type captured struct {
		path  string
		body  []byte
		doc   map[string]any
		paths int
	}
	var files []captured
	for _, p := range products {
		url := base + p.prefix + "openapi.json"
		raw, err := fetch(client, url)
		if err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
		// yaml.v3 parses JSON too, and decodes numbers as int rather
		// than float64 — so a maxLength survives as 25 and not 25.0.
		// Routing the body through encoding/json into `any` would
		// mangle every large integer in the document.
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", url, err)
		}
		if err := validate(doc, p); err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
		body, err := encode(doc)
		if err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
		files = append(files, captured{
			path:  filepath.Join(outDir, p.name+".yaml"),
			body:  body,
			doc:   doc,
			paths: len(doc["paths"].(map[string]any)),
		})
	}

	var drifted []string
	for _, f := range files {
		if check {
			same, note, err := matches(f.path, f.doc)
			if err != nil {
				return err
			}
			if note != "" {
				fmt.Fprintf(stdout, "  %s\n", note)
			}
			if !same {
				drifted = append(drifted, f.path)
			}
			continue
		}
		// 0o600 rather than 0o644 to satisfy gosec G306, matching
		// cmd/gendocs. The mode only applies when the file does not
		// already exist, and these are tracked in git — which records
		// only the executable bit — so the narrower mode costs nothing.
		if err := os.WriteFile(f.path, f.body, 0o600); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "  %s (%d paths, %d bytes)\n", f.path,
			f.paths, len(f.body))
	}

	if len(drifted) > 0 {
		return fmt.Errorf(
			"%d vendored spec(s) differ from the published contract: "+
				"%v; run `make vendor-spec` and record what changed in "+
				"the commit message", len(drifted), drifted)
	}
	return nil
}

func fetch(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url) //nolint:noctx // one-shot build tool
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSpecBytes))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	return raw, nil
}

func validate(doc map[string]any, p product) error {
	paths, _ := doc["paths"].(map[string]any)
	if len(paths) == 0 {
		return fmt.Errorf("the published contract carries no paths")
	}
	for _, path := range sortedKeys(paths) {
		if !strings.HasPrefix(path, p.prefix) {
			return fmt.Errorf("path %s sits outside the %s namespace %s",
				path, p.name, p.prefix)
		}
	}
	return findForbiddenExtension(doc, "")
}

// findForbiddenExtension walks the whole document, not only the
// path-item level where the API has placed its markers — a
// marker anywhere means the upstream public filter regressed, and
// vendoring the document would copy the regression. A mapping with a
// non-string key would decode as map[interface{}]interface{} and be
// skipped, but JSON object keys are always strings, so no served
// contract can produce one.
func findForbiddenExtension(v any, at string) error {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range sortedKeys(t) {
			if strings.HasPrefix(k, "x-pgedge-") || k == "x-go-type-import" {
				return fmt.Errorf("published contract carries %s at %s — "+
					"refusing to vendor it", k, at+"/"+k)
			}
			if err := findForbiddenExtension(t[k], at+"/"+k); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range t {
			if err := findForbiddenExtension(item,
				fmt.Sprintf("%s/%d", at, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// encode renders a captured document as YAML. yaml.v3 sorts mapping
// keys, which is what makes two captures of the same contract
// byte-identical regardless of served key order.
func encode(doc map[string]any) ([]byte, error) {
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// matches reports whether path already holds the contract in doc. The
// servers block is compared on its own and returned as a note rather
// than counted as drift: it names the host that served the document,
// so a check against another environment would otherwise fail on that
// line alone and say nothing about the paths and schemas it was run
// for. A missing file is drift, not an error — that is the state a
// fresh checkout of a new product would be in.
func matches(path string, doc map[string]any) (same bool, note string, err error) {
	//nolint:gosec // G304: path is built from -out and a fixed
	// product name, both under the operator's control.
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", err
	}
	var have map[string]any
	if err := yaml.Unmarshal(raw, &have); err != nil {
		return false, "", fmt.Errorf("parse %s: %w", path, err)
	}
	haveBody, err := encode(withoutServers(have))
	if err != nil {
		return false, "", err
	}
	wantBody, err := encode(withoutServers(doc))
	if err != nil {
		return false, "", err
	}
	if !reflect.DeepEqual(have["servers"], doc["servers"]) {
		var vendored, published any = serverURLs(have), serverURLs(doc)
		// A difference the URLs do not show (a description, a
		// variables map) would otherwise print the same list twice.
		if reflect.DeepEqual(vendored, published) {
			vendored, published = have["servers"], doc["servers"]
		}
		note = fmt.Sprintf("%s: servers differ, vendored %v, published %v",
			path, vendored, published)
	}
	return string(haveBody) == string(wantBody), note, nil
}

func withoutServers(doc map[string]any) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		if k != "servers" {
			out[k] = v
		}
	}
	return out
}

// serverURLs flattens a document's servers block to its url values.
func serverURLs(doc map[string]any) []string {
	var urls []string
	servers, _ := doc["servers"].([]any)
	for _, s := range servers {
		if m, ok := s.(map[string]any); ok {
			if u, ok := m["url"].(string); ok {
				urls = append(urls, u)
			}
		}
	}
	return urls
}
