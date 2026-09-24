package apicall

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

type usage struct{ msg string }

func (u *usage) Error() string { return u.msg }

func deps(base string) Deps {
	return Deps{
		Client:  http.DefaultClient,
		BaseURL: base,
		Check: func(status int, body string) error {
			if status >= 400 {
				return errors.New("status " + http.StatusText(status))
			}
			return nil
		},
		UsageErr: func(m string) error { return &usage{m} },
	}
}

func TestBuildRefusalsAreUsageErrors(t *testing.T) {
	d := deps("https://api.example/")
	for _, tc := range []struct {
		name string
		req  Request
		want string
	}{
		{"method", Request{Method: "FETCH", Path: "/x"}, "not one of GET"},
		{"absolute url", Request{Method: "GET", Path: "https://evil/x"}, "absolute URL"},
		{"relative path", Request{Method: "GET", Path: "x"}, "must start with /"},
		{"bad query", Request{Method: "GET", Path: "/x", Query: []string{"novalue"}}, "key=value"},
		{"body on GET", Request{Method: "GET", Path: "/x", Body: []byte("{}")}, "not sent with GET"},
		{"bad header", Request{Method: "GET", Path: "/x", Headers: []string{"nocolon"}}, "Name: value"},
		{"authorization", Request{Method: "GET", Path: "/x", Headers: []string{"Authorization: Bearer x"}}, "cannot set Authorization"},
		{"authorization lowercase", Request{Method: "GET", Path: "/x", Headers: []string{"authorization: Bearer x"}}, "cannot set Authorization"},
		{"unparseable query in path", Request{Method: "GET", Path: "/x?a=%zz"}, "query does not parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(context.Background(), d, tc.req)
			var u *usage
			if !errors.As(err, &u) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestBuildComposesTheRequest(t *testing.T) {
	d := deps("https://api.example/")
	d.Edit = func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer t")
		return nil
	}
	hreq, err := Build(context.Background(), d, Request{
		Method: "post", Path: "/byoc/v1/clusters", Query: []string{"a=1", "b=x y"},
		Headers: []string{"X-Test: yes"}, Body: []byte(`{"k":1}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if hreq.Method != "POST" || hreq.URL.String() != "https://api.example/byoc/v1/clusters?a=1&b=x+y" {
		t.Errorf("%s %s", hreq.Method, hreq.URL)
	}
	// A query typed into the path travels as typed, with --query
	// appended, never re-encoded or reordered.
	q, err := Build(context.Background(), d, Request{
		Method: "GET", Path: "/x?z=9&y=8", Query: []string{"a=1"}})
	if err != nil || q.URL.RawQuery != "z=9&y=8&a=1" {
		t.Errorf("query = %q err=%v", q.URL.RawQuery, err)
	}
	if hreq.Header.Get("Content-Type") != "application/json" ||
		hreq.Header.Get("X-Test") != "yes" || hreq.Header.Get("Authorization") != "Bearer t" {
		t.Errorf("headers = %v", hreq.Header)
	}
}

func TestDoPrintsBodiesByFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"b":2,"a":[1,2],"big":12345678901234567890}`))
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		case "/text":
			_, _ = w.Write([]byte("plain"))
		case "/bad":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"nope"}`))
		}
	}))
	t.Cleanup(srv.Close)
	d := deps(srv.URL)
	run := func(format, path string, include bool) (string, string, error) {
		rt, out, errOut := testsupport.NewRuntime(t, "", format)
		hreq, err := Build(context.Background(), d, Request{Method: "GET", Path: path})
		if err != nil {
			t.Fatal(err)
		}
		err = Do(rt, d, hreq, include)
		return out.String(), errOut.String(), err
	}
	out, _, err := run("text", "/json", false)
	if err != nil || !strings.HasPrefix(out, "{\n  \"b\": 2,\n  \"a\": [\n    1,\n    2\n  ],\n  \"big\": 12345678901234567890\n}") {
		t.Errorf("text json: %q %v", out, err)
	}
	// -o json is the server's bytes: key order and the 20-digit
	// integer survive, which a decode through any would not keep.
	out, _, err = run("json", "/json", false)
	if err != nil || strings.TrimSpace(out) != `{"b":2,"a":[1,2],"big":12345678901234567890}` {
		t.Errorf("json: %q %v", out, err)
	}
	out, _, err = run("yaml", "/json", false)
	if err != nil || !strings.Contains(out, "big: 12345678901234567890") {
		t.Errorf("yaml: %q %v", out, err)
	}
	out, errOut, err := run("text", "/empty", true)
	if err != nil || out != "" || !strings.Contains(errOut, "HTTP 204") || !strings.Contains(errOut, "no body") {
		t.Errorf("empty: out=%q err=%q %v", out, errOut, err)
	}
	out, _, err = run("text", "/text", false)
	if err != nil || out != "plain\n" {
		t.Errorf("text: %q %v", out, err)
	}
	out, _, err = run("text", "/bad", false)
	if err == nil || out != "" || !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("bad: out=%q err=%v", out, err)
	}
}

func TestReadBody(t *testing.T) {
	b, err := ReadBody("-", strings.NewReader("from stdin"))
	if err != nil || string(b) != "from stdin" {
		t.Errorf("stdin: %q %v", b, err)
	}
	b, _ = ReadBody(`{"x":1}`, nil)
	if string(b) != `{"x":1}` {
		t.Errorf("literal: %q", b)
	}
	if b, _ := ReadBody("", nil); b != nil {
		t.Errorf("empty spec gave %q", b)
	}
	if _, err := ReadBody("@/nonexistent/file", nil); err == nil {
		t.Error("missing file accepted")
	}
}
