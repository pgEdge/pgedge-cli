package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// testNodeID is the UUID of node n1 in nodeListBody. Node IDs are
// UUIDs and the logs path rejects a node NAME with
// "invalid UUID length: 2", which is what resolveNodeID exists for.
const testNodeID = "a77ed2b6-2130-5780-83fd-e09889a4f587"

// nodeListBody is a trimmed prod ListClusterNodes response. Only the
// fields resolveNodeID and node list read are kept.
const nodeListBody = `[{"availability_zone":"us-east-1a",` +
	`"display_name":"n1","id":"` + testNodeID + `",` +
	`"image_id":"ami-1","instance_id":"i-1","instance_type":"r7g.medium",` +
	`"ip_address":"10.2.1.154","is_active":true,"key_name":"",` +
	`"location":{"code":"IAD","country":"US","latitude":38.9,` +
	`"longitude":-77.4,"name":"N. Virginia","region":"N. Virginia",` +
	`"region_code":"VA"},"logical_name":"n1","name":"n1",` +
	`"region":"us-east-1","region_detail":{"active":true,` +
	`"availability_zones":["a"],"starfleet":"AWS","code":"IAD",` +
	`"name":"us-east-1"},"volume_iops":100,"volume_size":30,` +
	`"volume_type":"gp2"}]`

// nodeLogsBody is a devapi docker-log entry captured 2026-10-06, with
// raw_text cut to a few of the journald record's fields.
const nodeLogsBody = `[` +
	`{"level":"INFO","message":"NetworkDB stats netPeers:1 entries:1",` +
	`"raw_text":"{\"_SYSTEMD_UNIT\":\"docker.service\",\"PRIORITY\":\"6\",` +
	`\"MESSAGE\":\"NetworkDB stats netPeers:1 entries:1\"}",` +
	`"time":"2026-10-06T18:54:23Z"}]`

// nodeLogsRawOnlyBody is the older API's shape: only raw_text filled,
// and a trailing all-empty element.
const nodeLogsRawOnlyBody = `[` +
	`{"level":"","message":"","raw_text":"Aug 04 09:00:00 n1 dockerd[34]: up",` +
	`"time":""},` +
	`{"level":"","message":"","raw_text":"","time":""}]`

// nodeLogsRouter serves the cluster-nodes list and the node-logs read
// from one stub, recording the logs path and query it was asked for.
// testsupport.JSONHandler answers every path with one body, which cannot
// exercise a command that lists nodes and then reads logs.
func nodeLogsRouter(
	logsBody string, gotPath, gotQuery *string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/logs/") {
			*gotPath = r.URL.Path
			*gotQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(logsBody))
			return
		}
		_, _ = w.Write([]byte(nodeListBody))
	}
}

func TestNodeLogsRun(t *testing.T) {
	t.Run("resolves a node name to its UUID", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var path, query string
		url := testsupport.NewAuthedServer(t,
			nodeLogsRouter(nodeLogsBody, &path, &query))
		if err := runAuthed(t, rt, out, url, "node", "logs",
			testClusterID, "n1", "docker"); err != nil {
			t.Fatalf("node logs: %v", err)
		}
		want := "/byoc/v1/clusters/" + testClusterID + "/nodes/" +
			testNodeID + "/logs/docker"
		if path != want {
			t.Errorf("requested %q, want %q", path, want)
		}
		if !strings.Contains(out.String(), "NetworkDB stats") {
			t.Errorf("missing log line: %q", out.String())
		}
	})

	t.Run("text prints time, level and message", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var path, query string
		url := testsupport.NewAuthedServer(t,
			nodeLogsRouter(nodeLogsBody, &path, &query))
		if err := runAuthed(t, rt, out, url, "node", "logs",
			testClusterID, "n1", "docker"); err != nil {
			t.Fatalf("node logs: %v", err)
		}
		want := "2026-10-06T18:54:23Z  INFO     " +
			"NetworkDB stats netPeers:1 entries:1\n"
		if out.String() != want {
			t.Errorf("output = %q, want exactly %q", out.String(), want)
		}
	})

	t.Run("text escapes a newline in the message", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var path, query string
		body := `[{"level":"INFO","message":"a\nERR forged",` +
			`"raw_text":"{}","time":"2026-10-06T18:54:23Z"}]`
		url := testsupport.NewAuthedServer(t,
			nodeLogsRouter(body, &path, &query))
		if err := runAuthed(t, rt, out, url, "node", "logs",
			testClusterID, "n1", "docker"); err != nil {
			t.Fatalf("node logs: %v", err)
		}
		if n := strings.Count(out.String(), "\n"); n != 1 {
			t.Errorf("one entry printed %d lines: %q", n, out.String())
		}
	})

	// Exact equality, not a trimmed line count: TrimRight("\n") removes
	// the blank line this test exists to catch.
	t.Run("text falls back to raw_text and drops blanks", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var path, query string
		url := testsupport.NewAuthedServer(t,
			nodeLogsRouter(nodeLogsRawOnlyBody, &path, &query))
		if err := runAuthed(t, rt, out, url, "node", "logs",
			testClusterID, "n1", "docker"); err != nil {
			t.Fatalf("node logs: %v", err)
		}
		want := "Aug 04 09:00:00 n1 dockerd[34]: up\n"
		if out.String() != want {
			t.Errorf("output = %q, want exactly %q", out.String(), want)
		}
	})

	// A full UUID must be used as-is: no nodes list, so the command
	// works even on a cluster whose node list is unavailable.
	t.Run("accepts a full node UUID without listing", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var listed bool
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/nodes") {
					listed = true
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(nodeLogsBody))
			})
		if err := runAuthed(t, rt, out, url, "node", "logs",
			testClusterID, testNodeID, "docker"); err != nil {
			t.Fatalf("node logs by UUID: %v", err)
		}
		if listed {
			t.Error("listed cluster nodes for an argument already a UUID")
		}
	})

	t.Run("unknown node name lists the valid ones", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var path, query string
		url := testsupport.NewAuthedServer(t,
			nodeLogsRouter(nodeLogsBody, &path, &query))
		err := runAuthed(t, rt, out, url, "node", "logs",
			testClusterID, "nope", "docker")
		if err == nil {
			t.Fatal("expected an error for an unknown node name")
		}
		if !strings.Contains(err.Error(), "n1") {
			t.Errorf("error should list valid node names: %v", err)
		}
	})

	t.Run("json carries every field", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		var path, query string
		url := testsupport.NewAuthedServer(t,
			nodeLogsRouter(nodeLogsBody, &path, &query))
		if err := runAuthed(t, rt, out, url, "node", "logs",
			testClusterID, "n1", "docker"); err != nil {
			t.Fatalf("node logs json: %v", err)
		}
		var got []api.ClusterNodeLogMessage
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("output is not a message array: %v (%q)",
				err, out.String())
		}
		if len(got) != 1 {
			t.Fatalf("json dropped entries: got %d, want 1", len(got))
		}
		if got[0].Level != "INFO" || got[0].Time != "2026-10-06T18:54:23Z" ||
			!strings.Contains(got[0].RawText, "docker.service") {
			t.Errorf("json lost fields: %+v", got[0])
		}
	})

	t.Run("empty array reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		var path, query string
		url := testsupport.NewAuthedServer(t,
			nodeLogsRouter(`[]`, &path, &query))
		if err := runAuthed(t, rt, out, url, "node", "logs",
			testClusterID, "n1", "docker"); err != nil {
			t.Fatalf("node logs empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No log entries found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	// Optional filters must appear only when asked for: an empty --grep
	// or a zero --lines sent anyway would change what the API returns,
	// and on a real node an unwanted grep answers 500.
	t.Run("filters reach the API only when set", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var path, query string
		url := testsupport.NewAuthedServer(t,
			nodeLogsRouter(nodeLogsBody, &path, &query))
		if err := runAuthed(t, rt, out, url, "node", "logs",
			testClusterID, "n1", "docker",
			"--lines", "50", "--since", "2026-08-01T00:00:00Z",
			"--until", "2026-08-04T00:00:00Z", "--priority", "err",
			"--grep", "checkpoint", "--case-sensitive",
			"--reverse", "--dmesg"); err != nil {
			t.Fatalf("node logs with filters: %v", err)
		}
		for _, want := range []string{
			"lines=50", "since=2026-08-01T00%3A00%3A00Z",
			"until=2026-08-04T00%3A00%3A00Z", "priority=err",
			"grep=checkpoint", "case_sensitive=true", "reverse=true",
			"dmesg=true",
		} {
			if !strings.Contains(query, want) {
				t.Errorf("query %q missing %q", query, want)
			}
		}
	})

	t.Run("no filters means no query string", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var path, query string
		url := testsupport.NewAuthedServer(t,
			nodeLogsRouter(nodeLogsBody, &path, &query))
		if err := runAuthed(t, rt, out, url, "node", "logs",
			testClusterID, "n1", "docker"); err != nil {
			t.Fatalf("node logs bare: %v", err)
		}
		if query != "" {
			t.Errorf("bare call sent query %q, want none", query)
		}
	})

	// A parseable --since still answers 500 on some nodes, so the error
	// path is the one users will actually meet.
	t.Run("api error surfaces", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "/logs/") {
					w.WriteHeader(500)
					_, _ = w.Write(
						[]byte(`{"code":500,"message":"failed to read log"}`))
					return
				}
				_, _ = w.Write([]byte(nodeListBody))
			})
		if err := runAuthed(t, rt, out, url, "node", "logs",
			testClusterID, "n1", "docker"); err == nil {
			t.Fatal("expected an error on 500")
		}
	})
}
