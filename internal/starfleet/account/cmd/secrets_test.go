package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// The secret is shown once; with --secret-file it must reach the file
// and nothing else, in every format.
func TestClientCreateSecretFileKeepsSecretOffStdout(t *testing.T) {
	for _, format := range []string{"text", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", format)
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, apiClientCreatedBody))
			path := filepath.Join(t.TempDir(), "ci.secret")
			if err := runAuthedAccount(t, rt, out, url, "client",
				"create", "--name", "ci", "--description", "CI",
				"--secret-file", path); err != nil {
				t.Fatalf("client create --secret-file: %v", err)
			}
			for name, s := range map[string]string{
				"stdout": out.String(), "stderr": errb.String(),
			} {
				if strings.Contains(s, createdSecret) {
					t.Errorf("%s carries the secret: %q", name, s)
				}
			}
			if !strings.Contains(out.String(), testAPIClientID) {
				t.Errorf("stdout = %q, want the client", out.String())
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != createdSecret+"\n" {
				t.Fatalf("file = %q (%v), want the secret", got, err)
			}
			st, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
				t.Errorf("mode = %v, want 0600", st.Mode().Perm())
			}
		})
	}
}

// An existing path is refused before any request, so no client is
// minted whose secret has nowhere to go; a failed request leaves no
// empty file behind to block the retry.
func TestClientCreateSecretFileFailsSafely(t *testing.T) {
	cases := []struct {
		name     string
		existing bool
		status   int
		wantCode int
		wantReqs bool
	}{
		{"existing path", true, 200, ExitUsage, false},
		{"request fails", false, 500, -1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			var reqs atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					reqs.Add(1)
					if r.URL.Path == testsupport.TokenPath {
						fmt.Fprint(w, testsupport.TokenBody)
						return
					}
					testsupport.JSONHandler(tc.status,
						apiClientCreatedBody)(w, r)
				}))
			defer srv.Close()
			path := filepath.Join(t.TempDir(), "ci.secret")
			if tc.existing {
				if err := os.WriteFile(path, []byte("keep"),
					0o600); err != nil {
					t.Fatal(err)
				}
			}

			err := runAuthedAccount(t, rt, out, srv.URL, "client",
				"create", "--name", "ci", "--description", "CI",
				"--secret-file", path)
			if err == nil {
				t.Fatal("want an error")
			}
			var ee *ExitError
			if tc.wantCode >= 0 && (!errors.As(err, &ee) ||
				ee.Code() != tc.wantCode) {
				t.Errorf("err = %v, want exit %d", err, tc.wantCode)
			}
			if got := reqs.Load() > 0; got != tc.wantReqs {
				t.Errorf("requests sent = %d, want any: %v",
					reqs.Load(), tc.wantReqs)
			}
			got, statErr := os.ReadFile(path)
			if tc.existing && string(got) != "keep" {
				t.Errorf("existing file changed: %q", got)
			}
			if !tc.existing && !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("file left behind after a failed request")
			}
		})
	}
}

// The three secret sources are exclusive, and the stdin and file
// forms drop exactly one trailing newline.
func TestAuthLoginSecretSources(t *testing.T) {
	cases := []struct {
		name       string
		stdin      string
		fileBody   string // written to a temp file named by FILE
		args       []string
		wantSecret string
		wantUsage  bool
	}{
		{name: "stdin drops one newline", stdin: "s3cret\n\n",
			args:       []string{"--client-secret-stdin"},
			wantSecret: "s3cret\n"},
		{name: "stdin drops CRLF", stdin: "s3cret\r\n",
			args:       []string{"--client-secret-stdin"},
			wantSecret: "s3cret"},
		{name: "file", fileBody: "s3cret\n",
			args:       []string{"--client-secret-file", "FILE"},
			wantSecret: "s3cret"},
		{name: "empty stdin", stdin: "\n",
			args: []string{"--client-secret-stdin"}, wantUsage: true},
		{name: "stdin and flag", stdin: "s3cret\n",
			args: []string{"--client-secret-stdin",
				"--client-secret", "x"}, wantUsage: true},
		{name: "file and flag", fileBody: "s3cret\n",
			args: []string{"--client-secret-file", "FILE",
				"--client-secret", "x"}, wantUsage: true},
		{name: "stdin and file", stdin: "s3cret\n", fileBody: "s3cret",
			args: []string{"--client-secret-stdin",
				"--client-secret-file", "FILE"}, wantUsage: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reqs atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					reqs.Add(1)
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, testsupport.TokenBody)
				}))
			defer srv.Close()
			rt, out, _ := testsupport.NewRuntime(t, tc.stdin, "text")
			file := filepath.Join(t.TempDir(), "secret")
			if err := os.WriteFile(file, []byte(tc.fileBody),
				0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"auth", "login", "--client-id", "id",
				"--api-url", srv.URL, "--insecure-storage"}
			for _, a := range tc.args {
				if a == "FILE" {
					a = file
				}
				args = append(args, a)
			}

			err := runAccount(t, rt, out, args...)
			if tc.wantUsage {
				var ee *ExitError
				if !errors.As(err, &ee) || ee.Code() != ExitUsage {
					t.Fatalf("err = %v, want exit %d", err, ExitUsage)
				}
				if reqs.Load() != 0 {
					t.Errorf("a rejected login sent %d requests",
						reqs.Load())
				}
				return
			}
			if err != nil {
				t.Fatalf("auth login: %v", err)
			}
			cp := rt.Config.StarfleetProfile("default")
			if cp.ClientSecret != tc.wantSecret {
				t.Errorf("saved secret = %q, want %q",
					cp.ClientSecret, tc.wantSecret)
			}
		})
	}
}
