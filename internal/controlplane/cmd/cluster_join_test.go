package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
)

const secretJoinToken = "PGEDGE-s3cr3t-join-token"

func hostListBody(hosts ...[2]string) string {
	rows := make([]string, 0, len(hosts))
	for _, h := range hosts {
		rows = append(rows, `{"id":"`+h[0]+`","orchestrator":"swarm",`+
			`"data_dir":"/d","peer_addresses":[],"client_addresses":[],`+
			`"status":{"state":"`+h[1]+`",`+
			`"updated_at":"2026-06-18T00:00:00Z","components":{}}}`)
	}
	return `{"hosts":[` + strings.Join(rows, ",") + `]}`
}

// joinTarget records the join body the new host receives.
func joinTarget(
	t *testing.T, status int, reply string,
) (url string, body *string) {
	t.Helper()
	var mu sync.Mutex
	body = new(string)
	url = newServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		*body = string(b)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	})
	return url, body
}

func writeTokenFile(t *testing.T, contents string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestClusterJoinTokenSourcesAreExclusive(t *testing.T) {
	file := writeTokenFile(t, "tok\n")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"none", []string{"--server-url", "http://m:3000"}},
		{"from and token", []string{"--from", "http://m:3000",
			"--token", "x"}},
		{"from and token-file", []string{"--from", "http://m:3000",
			"--token-file", file}},
		{"token and token-file", []string{"--token", "x",
			"--token-file", file, "--server-url", "http://m:3000"}},
		{"token-file without server-url", []string{"--token-file", file}},
		{"empty token file", []string{"--token-file",
			writeTokenFile(t, "\n"), "--server-url", "http://m:3000"}},
		{"two-line token file", []string{"--token-file",
			writeTokenFile(t, "a\nb\n"), "--server-url", "http://m:3000"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "text")
			url, body := joinTarget(t, 200, "")
			err := runControlplane(t, rt, out, url,
				append([]string{"cluster", "join"}, tc.args...)...)
			requireUsageError(t, err)
			if *body != "" {
				t.Errorf("a refused join reached the server: %q", *body)
			}
		})
	}
}

func TestClusterJoinTokenFileDropsOneNewline(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url, body := joinTarget(t, 200, "")
	err := runControlplane(t, rt, out, url, "cluster", "join",
		"--token-file", writeTokenFile(t, secretJoinToken+"\n"),
		"--server-url", "http://m:3000")
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	var sent struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(*body), &sent); err != nil {
		t.Fatalf("join body: %v (%q)", err, *body)
	}
	if sent.Token != secretJoinToken {
		t.Errorf("token sent = %q, want %q", sent.Token, secretJoinToken)
	}
}

// TestClusterJoinFromNeverPrintsTheToken is the reason --from exists:
// the token goes from member to new host without reaching either
// stream, including when the join fails and the server echoes it.
func TestClusterJoinFromNeverPrintsTheToken(t *testing.T) {
	member := newServer(t, jsonHandler(200, `{"token":"`+secretJoinToken+
		`","server_urls":["http://member-peer:3000"]}`))
	for _, tc := range []struct {
		name    string
		status  int
		reply   string
		wantErr bool
	}{
		{"accepted", 200, "", false},
		{"rejected, token echoed", 401, `{"name":"invalid_join_token",` +
			`"message":"bad token ` + secretJoinToken + `"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, errb := newTestRuntime(t, "", "json")
			url, body := joinTarget(t, tc.status, tc.reply)
			err := runControlplane(t, rt, out, url, "cluster", "join",
				"--from", member)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && cli.ExitCode(err) != ExitAuth {
				t.Errorf("exit = %d, want %d", cli.ExitCode(err), ExitAuth)
			}
			streams := out.String() + errb.String()
			if err != nil {
				streams += err.Error()
			}
			if strings.Contains(streams, secretJoinToken) {
				t.Errorf("token reached output: %q", streams)
			}
			var sent struct {
				Token      string   `json:"token"`
				ServerURLs []string `json:"server_urls"`
			}
			if err := json.Unmarshal([]byte(*body), &sent); err != nil {
				t.Fatalf("join body: %v (%q)", err, *body)
			}
			if sent.Token != secretJoinToken ||
				len(sent.ServerURLs) != 1 ||
				sent.ServerURLs[0] != "http://member-peer:3000" {
				t.Errorf("join body = %+v, want the member's token "+
					"and server URLs", sent)
			}
		})
	}
}

func TestClusterJoinWaitsForTheNewHost(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	member := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/cluster/join-token" {
			_, _ = io.WriteString(w, joinTokenBody)
			return
		}
		mu.Lock()
		n := polls
		polls++
		mu.Unlock()
		switch n {
		case 0: // the snapshot, before the join
			_, _ = io.WriteString(w, hostListBody([2]string{"host-1", "healthy"}))
		case 1:
			_, _ = io.WriteString(w, hostListBody(
				[2]string{"host-1", "healthy"},
				[2]string{"host-2", "degraded"}))
		default:
			_, _ = io.WriteString(w, hostListBody(
				[2]string{"host-1", "healthy"},
				[2]string{"host-2", "healthy"}))
		}
	})
	rt, out, errb := newTestRuntime(t, "", "text")
	url, _ := joinTarget(t, 200, "")
	if err := runControlplane(t, rt, out, url, "cluster", "join",
		"--from", member, "--wait", "--wait-interval", "1"); err != nil {
		t.Fatalf("join --wait: %v", err)
	}
	if !strings.Contains(errb.String(), "Host host-2: healthy.") {
		t.Errorf("want the new host reported healthy: %q", errb.String())
	}
	if strings.Contains(errb.String(), "Host host-1") {
		t.Errorf("an existing member was reported as joining: %q",
			errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestClusterJoinWaitTimesOut(t *testing.T) {
	member := newServer(t, jsonHandler(200,
		hostListBody([2]string{"host-1", "healthy"})))
	rt, out, _ := newTestRuntime(t, "", "text")
	url, _ := joinTarget(t, 200, "")
	err := runControlplane(t, rt, out, url, "cluster", "join",
		"--token", "x", "--server-url", member,
		"--wait", "--wait-timeout", "0")
	if cli.ExitCode(err) != ExitTimeout {
		t.Fatalf("exit = %d (%v), want %d", cli.ExitCode(err), err,
			ExitTimeout)
	}
}
