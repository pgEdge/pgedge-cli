// Package apicall is the raw request behind `pgedge starfleet api` and
// `pgedge controlplane api`: one HTTP call to a path under the module's
// API base, over the module's own authenticated client, with the
// module's own status-to-exit mapping and the CLI's output formats.
//
// It is an escape hatch for the endpoint no verb covers yet, and keeps
// the CLI's contract on the raw call, as gh api and az rest do.
package apicall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// Request is one call. Path is relative to the base URL and must start
// with "/"; the host always comes from the parsed base, so a path can
// at most move within the API the credential was issued for.
type Request struct {
	Method  string
	Path    string
	Query   []string // key=value, repeatable
	Headers []string // Name: value, repeatable
	Body    []byte
	// Include prints the status line and response headers on stderr.
	Include bool
}

// Methods is the closed vocabulary; anything else is a usage error.
var Methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// Deps is what a module hands over: its client, base URL, request
// editor (nil for a client whose transport already authenticates),
// and its status-to-exit mapping.
type Deps struct {
	Client   *http.Client
	BaseURL  string
	Edit     func(context.Context, *http.Request) error
	Check    func(status int, body string) error
	UsageErr func(msg string) error
	// Transport wraps a failure to reach the server the way the
	// module's own verbs do (controlplane adds its reachability hint);
	// nil keeps the plain error.
	Transport func(err error) error
}

// ReadBody resolves the --data spelling: "@file" reads a file, "-"
// reads stdin, anything else is the literal body.
func ReadBody(spec string, stdin io.Reader) ([]byte, error) {
	switch {
	case spec == "":
		return nil, nil
	case spec == "-":
		return io.ReadAll(stdin)
	case strings.HasPrefix(spec, "@"):
		return os.ReadFile(spec[1:])
	}
	return []byte(spec), nil
}

// Build validates req against d and returns the http.Request, before
// anything is sent, so every refusal is exit 2 with no round trip.
func Build(ctx context.Context, d Deps, req Request) (*http.Request, error) {
	method := strings.ToUpper(req.Method)
	known := false
	for _, m := range Methods {
		if m == method {
			known = true
		}
	}
	if !known {
		return nil, d.UsageErr(fmt.Sprintf(
			"method %q is not one of %s", req.Method,
			strings.Join(Methods, ", ")))
	}
	if !strings.HasPrefix(req.Path, "/") || strings.Contains(req.Path, "://") {
		return nil, d.UsageErr(fmt.Sprintf(
			"path %q must start with / and be relative to the API base; "+
				"an absolute URL is not accepted", req.Path))
	}
	u, err := url.Parse(strings.TrimRight(d.BaseURL, "/") + req.Path)
	if err != nil {
		return nil, d.UsageErr(fmt.Sprintf("path %q: %v", req.Path, err))
	}
	// A query typed into the path travels as typed. One that does not
	// parse is refused: re-encoding it would send a different request.
	if u.RawQuery != "" {
		if _, err := url.ParseQuery(u.RawQuery); err != nil {
			return nil, d.UsageErr(fmt.Sprintf(
				"path %q: query does not parse: %v", req.Path, err))
		}
	}
	extra := url.Values{}
	for _, kv := range req.Query {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, d.UsageErr(fmt.Sprintf(
				"--query %q is not key=value", kv))
		}
		extra.Add(k, v)
	}
	if len(extra) > 0 {
		if u.RawQuery != "" {
			u.RawQuery += "&"
		}
		u.RawQuery += extra.Encode()
	}
	var body io.Reader
	if len(req.Body) > 0 {
		if method == http.MethodGet {
			return nil, d.UsageErr("--data is not sent with GET; use " +
				"--query for parameters")
		}
		body = bytes.NewReader(req.Body)
	}
	hreq, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, d.UsageErr(err.Error())
	}
	if body != nil {
		hreq.Header.Set("Content-Type", "application/json")
	}
	hreq.Header.Set("Accept", "application/json")
	for _, h := range req.Headers {
		name, value, ok := strings.Cut(h, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, d.UsageErr(fmt.Sprintf(
				"--header %q is not Name: value", h))
		}
		if strings.EqualFold(name, "Authorization") {
			return nil, d.UsageErr("--header cannot set Authorization; " +
				"the module's own credential is sent")
		}
		hreq.Header.Set(name, strings.TrimSpace(value))
	}
	if d.Edit != nil {
		if err := d.Edit(ctx, hreq); err != nil {
			return nil, err
		}
	}
	return hreq, nil
}

// Do sends hreq and prints the response through rt. A non-2xx status
// goes through d.Check, so the exit code matches the module's verbs.
func Do(rt *module.Runtime, d Deps, hreq *http.Request, include bool) error {
	// The host is the module's own API base and Build refuses an
	// absolute URL, so the caller chooses only the path.
	resp, err := d.Client.Do(hreq) //nolint:gosec // G704: host fixed above
	if err != nil {
		if d.Transport != nil {
			return d.Transport(err)
		}
		return fmt.Errorf("%s %s: %w", hreq.Method, hreq.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if include {
		fmt.Fprintf(rt.Stderr, "HTTP %d\n", resp.StatusCode)
		for _, k := range sortedKeys(resp.Header) {
			fmt.Fprintf(rt.Stderr, "%s: %s\n", output.Sanitize(k),
				output.Sanitize(strings.Join(resp.Header[k], ", ")))
		}
	}
	if err := d.Check(resp.StatusCode, string(raw)); err != nil {
		return err
	}
	body := bytes.TrimSpace(raw)
	if len(body) == 0 {
		fmt.Fprintf(rt.Stderr, "%s %s: HTTP %d, no body.\n",
			output.Sanitize(hreq.Method), output.Sanitize(hreq.URL.Path),
			resp.StatusCode)
		return nil
	}
	if json.Valid(body) {
		switch rt.Output.Format {
		case "json":
			// The server's bytes, untouched: a re-encode through any
			// would round every integer to float64 and sort the keys.
			_, err = rt.Stdout.Write(append(body, '\n'))
			return err
		case "yaml":
			// UseNumber plus yaml.Node keeps a 20-digit integer's
			// digits and type; the shared renderer's yaml path rounds
			// it through float64.
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.UseNumber()
			var v any
			if err := dec.Decode(&v); err != nil {
				return err
			}
			enc := yaml.NewEncoder(rt.Stdout)
			enc.SetIndent(2)
			if err := enc.Encode(yamlNode(v)); err != nil {
				return err
			}
			return enc.Close()
		default:
			var out bytes.Buffer
			if err := json.Indent(&out, body, "", "  "); err != nil {
				return err
			}
			out.WriteByte('\n')
			_, err = rt.Stdout.Write(out.Bytes())
			return err
		}
	}
	_, err = rt.Stdout.Write(append(body, '\n'))
	return err
}

// yamlNode converts a UseNumber-decoded JSON tree into yaml.Nodes,
// sorting mapping keys and writing every number with the server's own
// digits under an int or float tag.
func yamlNode(v any) *yaml.Node {
	switch t := v.(type) {
	case map[string]any:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, k := range sortedMapKeys(t) {
			n.Content = append(n.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
				yamlNode(t[k]))
		}
		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, e := range t {
			n.Content = append(n.Content, yamlNode(e))
		}
		return n
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(t.String(), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: t.String()}
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool",
			Value: fmt.Sprint(t)}
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeys(h http.Header) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
