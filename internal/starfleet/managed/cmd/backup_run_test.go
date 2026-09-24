package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"gopkg.in/yaml.v3"
)

// backupJSON renders a minimal Backup with the given id and database
// id, matching the wire shape saas's managedBackupModelFromObject
// produces: kind/status from the CNPG tier/phase and the managed-only
// detail in metadata.
func backupJSON(id, dbID string) string {
	return fmt.Sprintf(`{
		"id":%q,"database_id":%q,
		"name":"%s-durable-20260804060500",
		"kind":"durable","status":"completed",
		"created_at":"2026-08-04T06:05:00Z",
		"finished_at":"2026-08-04T06:05:02Z",
		"metadata":{"source":"managed","tier":"durable",
			"method":"plugin"}}`,
		id, dbID, dbID)
}

// backupJSONWithPurpose extends backupJSON with saas #1968's purpose
// field. purpose is spliced in unvalidated, exercising the same wire
// shape for a recognized value, an unrecognized one, or any other
// string the caller passes.
func backupJSONWithPurpose(id, dbID, purpose string) string {
	return strings.Replace(backupJSON(id, dbID),
		`"kind":"durable"`,
		fmt.Sprintf(`"kind":"durable","purpose":%q`, purpose), 1)
}

// --- list ---

// TestBackupListRendersDecidedColumns pins the table design: NAME is
// omitted because it is derived from database_id + kind + created_at
// and nothing reads it back, and metadata is machine output only.
// There is no SIZE column either — the API stopped returning size
// altogether (CNPG reports none).
func TestBackupListRendersDecidedColumns(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, "["+backupJSON(testBackupID, testDatabaseID)+"]"))

	if err := runAuthed(t, rt, out, srv, "backup", "list"); err != nil {
		t.Fatalf("backup list: %v", err)
	}
	s := out.String()
	for _, col := range []string{
		"ID", "DATABASE", "KIND", "PURPOSE", "STATUS", "CREATED",
		"FINISHED"} {
		if !strings.Contains(s, col) {
			t.Errorf("table is missing the %s column:\n%s", col, s)
		}
	}
	if !strings.Contains(s, testBackupID) {
		t.Errorf("table does not show the backup id:\n%s", s)
	}
	if strings.Contains(s, "SIZE") {
		t.Errorf("SIZE column rendered; the API returns no size for "+
			"a managed backup:\n%s", s)
	}
	if strings.Contains(s, "durable-20260804060500") {
		t.Errorf("derived name rendered in the table:\n%s", s)
	}
}

// TestBackupRowFromRendersPurpose is the direct, table-driven check on
// the adapter saas #1968 added purpose to: a known value, an
// unrecognized one (the set is open — render it, don't reject it),
// and absent (nil, blank — the same convention FinishedAt's own
// absence already used).
func TestBackupRowFromRendersPurpose(t *testing.T) {
	cases := []struct {
		name    string
		purpose *string
		want    string
	}{
		{"known value", strPtr("scheduled"), "scheduled"},
		{"unrecognized value", strPtr("quarterly-audit"), "quarterly-audit"},
		{"absent", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := backupRowFrom(api.Backup{
				Id:         testBackupID,
				DatabaseId: testDatabaseID,
				Kind:       "durable",
				Status:     "completed",
				CreatedAt:  time.Now(),
				Purpose:    tc.purpose,
			})
			if row.purpose != tc.want {
				t.Errorf("purpose = %q, want %q", row.purpose, tc.want)
			}
		})
	}
}

// TestBackupGetRendersPurposeAcrossOutputModes exercises purpose
// end-to-end — server response through the generated client's JSON
// decode and into each renderer — for a known value, an unrecognized
// one, and absent. The unrecognized case is the contract: saas #1968
// documents the set as open, so an unfamiliar value must render
// without the command refusing it.
func TestBackupGetRendersPurposeAcrossOutputModes(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantOut string // substring the rendering must contain
	}{
		{
			name: "known value",
			json: backupJSONWithPurpose(
				testBackupID, testDatabaseID, "scheduled"),
			wantOut: "scheduled",
		},
		{
			name: "unrecognized value",
			json: backupJSONWithPurpose(
				testBackupID, testDatabaseID, "quarterly-audit"),
			wantOut: "quarterly-audit",
		},
	}
	for _, tc := range cases {
		for _, format := range []string{"text", "json", "yaml"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				rt, out, _ := testsupport.NewRuntime(t, "", format)
				srv := testsupport.NewAuthedServer(t,
					testsupport.JSONHandler(http.StatusOK, tc.json))

				if err := runAuthed(t, rt, out, srv,
					"backup", "get", testBackupID); err != nil {
					t.Fatalf("backup get: %v", err)
				}
				if !strings.Contains(out.String(), tc.wantOut) {
					t.Errorf("%s output missing purpose %q:\n%s",
						format, tc.wantOut, out.String())
				}
			})
		}
	}
}

// TestBackupGetOmitsPurposeWhenAbsent pins the other half of the
// contract: a backup with no provenance recorded renders blank in
// text rather than erroring, and the json/yaml renderers — which
// print the struct straight through — omit the key entirely rather
// than emitting a null, matching FinishedAt's own omitempty tag.
func TestBackupGetOmitsPurposeWhenAbsent(t *testing.T) {
	for _, format := range []string{"text", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", format)
			srv := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
				http.StatusOK,
				backupJSON(testBackupID, testDatabaseID)))

			if err := runAuthed(t, rt, out, srv,
				"backup", "get", testBackupID); err != nil {
				t.Fatalf("backup get: %v", err)
			}
			if strings.Contains(out.String(), "purpose") {
				t.Errorf("%s output names purpose though the backup "+
					"carries none:\n%s", format, out.String())
			}
		})
	}
}

func TestBackupListReportsWhenEmpty(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, "[]"))

	if err := runAuthed(t, rt, out, srv, "backup", "list"); err != nil {
		t.Fatalf("backup list: %v", err)
	}
	if !strings.Contains(errb.String(), "No backups found.") {
		t.Errorf("empty list not reported: %q", errb.String())
	}
}

// TestBackupListAppliesEveryFilter asserts each flag lands on the
// wire as the query parameter the spec names.
func TestBackupListAppliesEveryFilter(t *testing.T) {
	var mu sync.Mutex
	var got url.Values
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			got = r.URL.Query()
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		})

	err := runAuthed(t, rt, out, srv, "backup", "list",
		"--database-id", testDatabaseID,
		"--kind", "durable",
		"--created-after", "2026-08-01T00:00:00Z",
		"--created-before", "2026-08-05T00:00:00Z",
		"--limit", "5", "--offset", "2", "--descending")
	if err != nil {
		t.Fatalf("backup list with filters: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for param, want := range map[string]string{
		"database_id":    testDatabaseID,
		"kind":           "durable",
		"created_after":  "2026-08-01T00:00:00Z",
		"created_before": "2026-08-05T00:00:00Z",
		"limit":          "5",
		"offset":         "2",
		"descending":     "true",
	} {
		if got.Get(param) != want {
			t.Errorf("query[%q] = %q, want %q", param, got.Get(param), want)
		}
	}
}

// TestBackupListDescendingIsSentOnlyWhenGiven pins the flag's wire
// contract against the server-side default of true (newest first):
// an untouched flag sends no descending parameter at all, and
// --descending=false must land as descending=false — sending the
// parameter only when its value is true would make the server's
// default inexpressible to turn off.
func TestBackupListDescendingIsSentOnlyWhenGiven(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"default omits the parameter": {
			args: []string{"backup", "list"},
			want: "",
		},
		"explicit false is sent": {
			args: []string{"backup", "list", "--descending=false"},
			want: "false",
		},
		"explicit true is sent": {
			args: []string{"backup", "list", "--descending"},
			want: "true",
		},
	} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var got url.Values
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			srv := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					got = r.URL.Query()
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte("[]"))
				})

			if err := runAuthed(t, rt, out, srv, tc.args...); err != nil {
				t.Fatalf("backup list: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if got.Get("descending") != tc.want {
				t.Errorf("query[descending] = %q, want %q",
					got.Get("descending"), tc.want)
			}
		})
	}
}

// TestBackupListRefusesADatabaseIDPrefix is the inversion of a test
// that pinned prefix resolution on --database-id (#194). The value now
// takes a full UUID, refused locally at exit 2 with NOTHING sent —
// which is the half worth pinning, since the flag used to cost a
// GET /databases before the list it was filtering.
//
// The stub fails any request, so a check that resolved anyway fails
// here rather than passing on the exit code alone.
func TestBackupListRefusesADatabaseIDPrefix(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("a request reached %s for a malformed --database-id",
				r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		})

	err := runAuthed(t, rt, out, srv, "backup", "list",
		"--database-id", testDatabaseID[:8])
	if err == nil {
		t.Fatal("an ID prefix was accepted on --database-id")
	}
	if got := cli.ExitCode(err); got != ExitUsage {
		t.Errorf("exit %d, want %d", got, ExitUsage)
	}
}

// An explicitly empty --database-id is refused too, rather than
// silently meaning "every database" — ruling of 2026-08-20, which
// #317 applied to the paging flags and this extends to the ID filter.
func TestBackupListRefusesAnEmptyDatabaseID(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("a request reached %s for an empty --database-id",
				r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		})

	err := runAuthed(t, rt, out, srv, "backup", "list",
		"--database-id", "")
	if err == nil {
		t.Fatal("an empty --database-id was accepted")
	}
	if got := cli.ExitCode(err); got != ExitUsage {
		t.Errorf("exit %d, want %d", got, ExitUsage)
	}
}

func TestBackupListRejectsABadTimeFilter(t *testing.T) {
	for _, flag := range []string{"--created-after", "--created-before"} {
		t.Run(flag, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			srv := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusOK, "[]"))

			err := runAuthed(t, rt, out, srv,
				"backup", "list", flag, "yesterday")
			if err == nil {
				t.Fatal("a non-RFC3339 time filter was accepted")
			}
			// The code, not the type: the parser is shared with
			// byoc and cp now, so it returns *cli.UsageError rather
			// than this module's ExitError. cli.ExitCode is what
			// main.go consults, and 2 is the contract.
			if got := cli.ExitCode(err); got != ExitUsage {
				t.Fatalf("exit = %d, want ExitUsage(%d); err=%v",
					got, ExitUsage, err)
			}
		})
	}
}

// --- get ---

func TestBackupGetByFullUUIDWritesJSON(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	srv := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, backupJSON(testBackupID, testDatabaseID)))

	if err := runAuthed(t, rt, out, srv,
		"backup", "get", testBackupID); err != nil {
		t.Fatalf("backup get: %v", err)
	}
	var any1 any
	if err := json.Unmarshal(out.Bytes(), &any1); err != nil {
		t.Fatalf("stdout was not valid JSON: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), testBackupID) {
		t.Errorf("payload does not carry the backup id:\n%s", out.String())
	}
}

// TestBackupGetRefusesAPrefix replaces two tests that pinned
// resolution: one for the list-then-read flow, and one for the
// pagination inside it. Both are gone with prefixes (#194), and the
// paging loop went with them — resolveBackupID walked up to 100 pages
// of 100 to avoid the false unique a single page produces, and a full
// UUID needs no pages at all.
//
// The stub fails any request, so this pins that a `backup get` with a
// short id costs nothing rather than merely answering 2.
func TestBackupGetRefusesAPrefix(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("a request reached %s for a malformed backup ID",
				r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		})

	err := runAuthed(t, rt, out, srv,
		"backup", "get", testBackupID[:8])
	if err == nil {
		t.Fatal("an ID prefix was accepted")
	}
	if got := cli.ExitCode(err); got != ExitUsage {
		t.Errorf("exit %d, want %d", got, ExitUsage)
	}
}

func TestBackupGetNotFoundExitsFour(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusNotFound, `{"code":404,"message":"backup not found"}`))

	err := runAuthed(t, rt, out, srv, "backup", "get", testBackupID)
	if err == nil {
		t.Fatal("a 404 was reported as success")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitNotFound {
		t.Fatalf("want ExitNotFound, got %v", err)
	}
}

func TestBackupGetHandlesANullBody(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, "null"))

	if err := runAuthed(t, rt, out, srv,
		"backup", "get", testBackupID); err != nil {
		t.Fatalf("a null body crashed get: %v", err)
	}
}

// TestMislabelledNotFoundReachesCheckResponse is the end-to-end guard
// for issue #140, and it is deliberately a COMMAND test rather than a
// transport one: the defect was never in the classifier, which was
// already right, but in the fact that a mislabelled body errored
// inside the generated Parse*Response and so never reached it.
//
// JSONHandler is the exact bug shape — it sets a JSON Content-Type
// around whatever body it is given, here nginx's bare "Not Found".
// The assertion that matters is the negative one: the raw
// `invalid character 'N'` must be gone, because a test that only
// checked for the route-miss phrase would also pass if the message
// contained both.
func TestMislabelledNotFoundReachesCheckResponse(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusNotFound, "Not Found"))

	err := runAuthed(t, rt, out, srv, "backup", "get", testBackupID)
	if err == nil {
		t.Fatal("a 404 was reported as success")
	}
	msg := err.Error()
	if strings.Contains(msg, "invalid character") {
		t.Errorf("the raw JSON decode error still reaches the user, so "+
			"the body never got to CheckResponse: %q", msg)
	}
	if !strings.Contains(msg, "does not serve an endpoint") {
		t.Errorf("not classified as a route miss: %q", msg)
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
		t.Fatalf("want ExitGeneral for a route miss, got %v", err)
	}
}

// TestBackupKindsMatchTheSpecEnum fails when saas adds a backup tier
// that backupKinds has not been taught.
//
// The generated ListBackupsParamsKind has a Valid() method but no way
// to enumerate its members, so a Valid()-only check cannot notice a
// THIRD constant appearing — it would keep passing while
// `backup create --kind <new>` refused, client-side with exit 2,
// a value the API accepts. Reading the enum out of the vendored spec
// is the only test that fails in that direction.
func TestBackupKindsMatchTheSpecEnum(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "openapi", "managed.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `yaml:"operationId"`
			Parameters  []struct {
				Name   string `yaml:"name"`
				Schema struct {
					Enum []string `yaml:"enum"`
				} `yaml:"schema"`
			} `yaml:"parameters"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var spec []string
	for _, methods := range doc.Paths {
		for _, op := range methods {
			if op.OperationID != "ListBackups" {
				continue
			}
			for _, p := range op.Parameters {
				if p.Name == "kind" {
					spec = append(spec, p.Schema.Enum...)
				}
			}
		}
	}
	if len(spec) == 0 {
		t.Fatal("no kind enum found on ListBackups; this test has " +
			"lost its truth source and would pass against anything")
	}

	sort.Strings(spec)
	got := append([]string(nil), backupKinds...)
	sort.Strings(got)
	if !slices.Equal(spec, got) {
		t.Errorf("backupKinds = %v, spec enum = %v — teach "+
			"backupKinds the new tier (and check whether the create "+
			"endpoint accepts it)", got, spec)
	}
}

// --- create ---

// createRecorder captures what reached the API, including the body,
// so a test can prove the kind was actually sent rather than merely
// accepted by the flag parser.
type createRecorder struct {
	mu     sync.Mutex
	Method string
	Path   string
	Body   string
	Calls  int
}

func backupCreateHandler(rec *createRecorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			rec.mu.Lock()
			rec.Method, rec.Path = r.Method, r.URL.Path
			rec.Body = string(b)
			rec.Calls++
			rec.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			// The 202 carries the BACKUP, not the database. Answering
			// with a database here would let the pre-fix
			// rendering pass: Backup and ManagedDatabase share id,
			// name, status and created_at, so a stub on the wrong
			// schema cannot tell the two apart.
			_, _ = w.Write([]byte(
				backupJSON(testBackupID, testDatabaseID)))
			return
		}
		// The database list, for prefix resolution.
		_, _ = w.Write([]byte(
			"[" + databaseJSON(testDatabaseID, "") + "]"))
	}
}

// TestBackupCreateRejectsAnUnknownKindBeforeAnyRequest pins the
// client-side vocabulary check. CreateBackupRequest types Kind as a
// bare string, so nothing in the generated client would catch this —
// and a rejected value must cost exit 2, not a round trip.
func TestBackupCreateRejectsAnUnknownKindBeforeAnyRequest(t *testing.T) {
	rec := &createRecorder{}
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t, backupCreateHandler(rec))

	err := runAuthed(t, rt, out, srv, "backup", "create",
		"--database-id", testDatabaseID, "--kind", "warm")
	if err == nil {
		t.Fatal("an unknown kind was accepted")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Fatalf("want ExitUsage, got %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.Calls != 0 {
		t.Errorf("a rejected kind still reached the API (%d calls)",
			rec.Calls)
	}
}

// TestBackupCreatePostsTheKind proves the tier reaches the wire. A
// test that only checked the path would pass while sending an empty
// body, which the API would reject as a 400.
func TestBackupCreatePostsTheKind(t *testing.T) {
	for _, kind := range []string{"hot", "durable"} {
		t.Run(kind, func(t *testing.T) {
			rec := &createRecorder{}
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			srv := testsupport.NewAuthedServer(t,
				backupCreateHandler(rec))

			err := runAuthed(t, rt, out, srv, "backup", "create",
				"--database-id", testDatabaseID, "--kind", kind)
			if err != nil {
				t.Fatalf("backup create --kind %s: %v", kind, err)
			}

			rec.mu.Lock()
			defer rec.mu.Unlock()
			if rec.Method != http.MethodPost ||
				!strings.HasSuffix(rec.Path,
					"/databases/"+testDatabaseID+"/backup") {
				t.Errorf("not POSTed to the backup path; got %s %s",
					rec.Method, rec.Path)
			}
			var body struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal([]byte(rec.Body), &body); err != nil {
				t.Fatalf("body was not JSON: %v (%q)", err, rec.Body)
			}
			if body.Kind != kind {
				t.Errorf("kind = %q, want %q", body.Kind, kind)
			}
			if !strings.Contains(errb.String(), kind) {
				t.Errorf("report does not name the tier: %q",
					errb.String())
			}
		})
	}
}

// TestBackupCreateWritesTheBackupUnderJSON: the 202 carries the
// Backup, and a script needs it on stdout rather than a stderr
// sentence.
//
// The `database_id` assertion is the one that catches the defect.
// Before saas fixed it, the endpoint answered with the ManagedDatabase and
// the CLI declared the response as one — and ManagedDatabase HAS an
// `id` but no `database_id`, so `id == testBackupID` would have
// passed on the old shape while `database_id` was absent entirely.
// The `id` assertion is still worth making: it pins which of the two
// ids lands on the key a script reads.
func TestBackupCreateWritesTheBackupUnderJSON(t *testing.T) {
	rec := &createRecorder{}
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	srv := testsupport.NewAuthedServer(t, backupCreateHandler(rec))

	err := runAuthed(t, rt, out, srv, "backup", "create",
		"--database-id", testDatabaseID, "--kind", "hot")
	if err != nil {
		t.Fatalf("backup create -o json: %v", err)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("create wrote nothing to stdout under -o json")
	}
	var got struct {
		ID         string `json:"id"`
		DatabaseID string `json:"database_id"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout was not valid JSON: %v\n%s", err, out.String())
	}
	if got.ID != testBackupID {
		t.Errorf("id = %q, want the backup's %q\n%s",
			got.ID, testBackupID, out.String())
	}
	if got.DatabaseID != testDatabaseID {
		t.Errorf("database_id = %q, want %q\n%s",
			got.DatabaseID, testDatabaseID, out.String())
	}
}

// TestBackupCreateReportsTheBackupIDAndItsDatabase pins the text
// report against the defect the old response shape introduced here.
//
// The endpoint used to answer with the ManagedDatabase, so the report
// read `d.Name` and `d.Id` and called them the database's. When the
// response became a Backup, both fields still existed and still had
// the same Go types — the compiler saw nothing, and the command went
// on printing the BACKUP's name and id under the database's labels,
// exit 0. Only an assertion on which id lands in which role can catch
// that, which is why the two constants are checked separately: a swap
// fires both.
func TestBackupCreateReportsTheBackupIDAndItsDatabase(t *testing.T) {
	rec := &createRecorder{}
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t, backupCreateHandler(rec))

	if err := runAuthed(t, rt, out, srv, "backup", "create",
		"--database-id", testDatabaseID, "--kind", "hot"); err != nil {
		t.Fatalf("backup create: %v", err)
	}

	// The whole line, not a substring of it. A backup's name EMBEDS
	// its database's id — the real one reads
	// 4bc97596-…-hot-od-20260817122659 — so `Contains(report,
	// "database "+testDatabaseID)` is satisfied by printing the
	// backup's NAME under the database's label, which is precisely
	// the defect. Only an exact comparison rules that out.
	want := fmt.Sprintf(
		"hot backup started on database %s "+
			"(backup id: %s, status: completed).\n",
		testDatabaseID, testBackupID)
	if got := errb.String(); got != want {
		t.Errorf("report =\n  %q\nwant\n  %q", got, want)
	}
}

func TestBackupCreateRefusesADatabaseIDPrefix(t *testing.T) {
	rec := &createRecorder{}
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t, backupCreateHandler(rec))

	err := runAuthed(t, rt, out, srv, "backup", "create",
		"--database-id", testDatabaseID[:8], "--kind", "hot")
	if err == nil {
		t.Fatal("an ID prefix was accepted on --database-id")
	}
	if got := cli.ExitCode(err); got != ExitUsage {
		t.Errorf("exit %d, want %d", got, ExitUsage)
	}
	// The recorder is the point: a create must not have been sent.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.Path != "" {
		t.Errorf("a backup was created at %q for a malformed "+
			"--database-id", rec.Path)
	}
}

// --- restore ---

// backupRestoreHandler routes the restore flow: reading the backup,
// POSTing the restore, and the task endpoints --wait polls. A
// single-body stub cannot drive a command that reads then writes.
// Body is recorded because restore's contract is split across the
// request: the path names the DATABASE and the body names the BACKUP.
// A path-only assertion cannot tell a correct call from one that sent
// the wrong id in the body, or no body at all.
type restoreRecorder struct {
	mu     sync.Mutex
	Method string
	Path   string
	Body   string
	Calls  int
}

func backupRestoreHandler(rec *restoreRecorder,
	terminalStatus, taskError string,
) http.HandlerFunc {
	var mu sync.Mutex
	mutated := false

	errField := ""
	if taskError != "" {
		errField = fmt.Sprintf(`,"error":%q`, taskError)
	}
	newTask := fmt.Sprintf(`{"id":"e9562e00-c8f8-438b-860a-b5eb427f88d8","name":"restore-managed",
		"status":%q,"subject_id":%q,"subject_kind":"database",
		"messages":[]%s,"created_at":"2026-08-05T00:00:00Z",
		"updated_at":"2026-08-05T00:00:01Z"}`,
		terminalStatus, testDatabaseID, errField)

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/tasks"):
			if r.URL.Query().Get("id") != "" {
				_, _ = w.Write([]byte("[" + newTask + "]"))
				return
			}
			mu.Lock()
			done := mutated
			mu.Unlock()
			if done {
				_, _ = w.Write([]byte("[" + newTask + "]"))
			} else {
				_, _ = w.Write([]byte(`[]`))
			}

		case r.Method == http.MethodPost:
			mu.Lock()
			mutated = true
			mu.Unlock()
			body, _ := io.ReadAll(r.Body)
			rec.mu.Lock()
			rec.Method = r.Method
			rec.Path = r.URL.Path
			rec.Body = string(body)
			rec.Calls++
			rec.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))

		case strings.HasSuffix(r.URL.Path, "/backups"):
			_, _ = w.Write([]byte(
				"[" + backupJSON(testBackupID, testDatabaseID) + "]"))

		default:
			_, _ = w.Write([]byte(
				backupJSON(testBackupID, testDatabaseID)))
		}
	}
}

// TestBackupRestoreRequiresConfirmation pins the destructive-verb
// contract: without --force and without a TTY, restore must refuse
// before anything reaches the API.
func TestBackupRestoreRequiresConfirmation(t *testing.T) {
	rec := &restoreRecorder{}
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t,
		backupRestoreHandler(rec, "succeeded", ""))

	err := runAuthed(t, rt, out, srv, "backup", "restore", testBackupID)
	if err == nil {
		t.Fatal("restore ran without confirmation")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("refusal does not name --force: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.Calls != 0 {
		t.Errorf("a request reached the API before confirmation")
	}
}

// TestBackupRestorePostsAndReports drives the accepted path: the POST
// goes to /databases/{database_id}/restore with the backup in the body,
// the human report names the database, and the monitor hint lands on
// stderr.
//
// The two ids are asserted separately and against different constants,
// because restore is keyed on the database while the argument the user
// types is a BACKUP id. Swapping them still produces a well-formed
// request that a single-id assertion would accept.
func TestBackupRestorePostsAndReports(t *testing.T) {
	rec := &restoreRecorder{}
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t,
		backupRestoreHandler(rec, "succeeded", ""))

	err := runAuthed(t, rt, out, srv,
		"backup", "restore", testBackupID, "--force")
	if err != nil {
		t.Fatalf("backup restore --force: %v", err)
	}

	rec.mu.Lock()
	if rec.Method != http.MethodPost ||
		!strings.HasSuffix(rec.Path,
			"/databases/"+testDatabaseID+"/restore") {
		t.Errorf("restore was not POSTed to the database's restore "+
			"path; got %s %s", rec.Method, rec.Path)
	}
	var body struct {
		BackupID string `json:"backup_id"`
	}
	if err := json.Unmarshal([]byte(rec.Body), &body); err != nil {
		t.Fatalf("restore body was not valid JSON: %v\n%s",
			err, rec.Body)
	}
	if body.BackupID != testBackupID {
		t.Errorf("restore body names backup %q, want %q",
			body.BackupID, testBackupID)
	}
	rec.mu.Unlock()

	s := errb.String()
	if !strings.Contains(s, testDatabaseID) {
		t.Errorf("report does not name the database: %q", s)
	}
	if !strings.Contains(s, "managed task list") {
		t.Errorf("no hint at how to monitor: %q", s)
	}
	if strings.Contains(out.String(), "managed task list") {
		t.Errorf("hint leaked onto stdout: %q", out.String())
	}
}

// TestBackupRestoreWritesTheDatabaseUnderJSON: the 202 carries the
// ManagedDatabase being restored, and a script needs it on stdout.
func TestBackupRestoreWritesTheDatabaseUnderJSON(t *testing.T) {
	rec := &restoreRecorder{}
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	srv := testsupport.NewAuthedServer(t,
		backupRestoreHandler(rec, "succeeded", ""))

	err := runAuthed(t, rt, out, srv,
		"backup", "restore", testBackupID, "--force")
	if err != nil {
		t.Fatalf("backup restore -o json: %v", err)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("restore wrote nothing to stdout under -o json")
	}
	var any1 any
	if err := json.Unmarshal(out.Bytes(), &any1); err != nil {
		t.Fatalf("stdout was not valid JSON: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), testDatabaseID) {
		t.Errorf("payload does not carry the database:\n%s", out.String())
	}
}

// TestBackupRestoreWaits covers --wait on the one managed verb whose
// subject is discovered from the backup rather than named in the
// arguments. An async verb without --wait reports success it cannot
// know.
func TestBackupRestoreWaits(t *testing.T) {
	t.Run("succeeds", func(t *testing.T) {
		rec := &restoreRecorder{}
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		srv := testsupport.NewAuthedServer(t,
			backupRestoreHandler(rec, "succeeded", ""))

		err := runAuthed(t, rt, out, srv,
			"backup", "restore", testBackupID, "--force",
			"--wait", "--wait-interval", "1", "--wait-timeout", "30")
		if err != nil {
			t.Fatalf("backup restore --wait: %v", err)
		}
		s := errb.String()
		if !strings.Contains(s, "Tracking task e9562e00-c8f8-438b-860a-b5eb427f88d8") {
			t.Errorf("did not report tracking a task: %q", s)
		}
		if !strings.Contains(s, "succeeded") {
			t.Errorf("did not report success: %q", s)
		}
	})

	t.Run("reports a failed task", func(t *testing.T) {
		rec := &restoreRecorder{}
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		srv := testsupport.NewAuthedServer(t,
			backupRestoreHandler(rec, "failed", "recovery failed"))

		err := runAuthed(t, rt, out, srv,
			"backup", "restore", testBackupID, "--force",
			"--wait", "--wait-interval", "1", "--wait-timeout", "30")
		if err == nil {
			t.Fatal("a failed restore task was reported as success")
		}
		var ee *ExitError
		if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
			t.Fatalf("want ExitGeneral, got %v", err)
		}
		if !strings.Contains(err.Error(), "recovery failed") {
			t.Errorf("error does not carry the reason: %v", err)
		}
	})
}

// TestBackupRestoreFailsWhenTheBackupReadFails: the restore reads the
// backup first to learn its database, and that read can fail.
func TestBackupRestoreFailsWhenTheBackupReadFails(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusNotFound, `{"code":404,"message":"backup not found"}`))

	err := runAuthed(t, rt, out, srv,
		"backup", "restore", testBackupID, "--force")
	if err == nil {
		t.Fatal("a missing backup was restored")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitNotFound {
		t.Fatalf("want ExitNotFound, got %v", err)
	}
}
