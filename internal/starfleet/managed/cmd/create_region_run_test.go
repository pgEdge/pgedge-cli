package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// regionCreateServer answers the regions list with the given names and
// records the create body. Paths, not order: the create verb reads
// regions first, so a handler keyed on call count would pass whichever
// way round the CLI happened to send them.
func regionCreateServer(
	t *testing.T, rec *captureRequest, regions ...string,
) string {
	t.Helper()
	items := make([]string, 0, len(regions))
	for _, r := range regions {
		items = append(items, `{"region":"`+r+`"}`)
	}
	body := "[" + strings.Join(items, ",") + "]"

	return testsupport.NewAuthedServer(t, func(
		w http.ResponseWriter, r *http.Request,
	) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/regions") {
			_, _ = w.Write([]byte(body))
			return
		}
		// Not withCatalog: this server's whole subject is varying the
		// REGION list, so the shared wrapper would shadow the thing
		// under test. The size list is a fixture here either way.
		if strings.HasSuffix(r.URL.Path, "/sizes") {
			_, _ = w.Write([]byte(catalogSizesJSON))
			return
		}
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		rec.Body = string(buf)
		_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
	})
}

// TestCreateResolvesTheSoleRegion covers #195: --region is optional
// when the API publishes exactly one, because there is no choice to get
// wrong, and required as soon as there is.
func TestCreateResolvesTheSoleRegion(t *testing.T) {
	t.Run("one region is supplied for you", func(t *testing.T) {
		rec := &captureRequest{}
		url := regionCreateServer(t, rec, "us-east-2")

		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "create",
			"--name", "mydb", "--size", "small"); err != nil {
			t.Fatalf("create without --region: %v", err)
		}

		var body map[string]any
		if err := json.Unmarshal([]byte(rec.Body), &body); err != nil {
			t.Fatalf("create body was not JSON: %v (%q)", err, rec.Body)
		}
		if body["region"] != "us-east-2" {
			t.Errorf("region = %v, want the sole published region",
				body["region"])
		}
	})

	// An explicit flag must win. Otherwise the resolution would be
	// silently overriding a value the caller chose, and this test would
	// pass on an implementation that ignored --region entirely.
	//
	// Both regions are published, and that is now part of the case
	// rather than incidental to it: an explicit --region is checked
	// against the list before it is sent (#242), so a region the API
	// does not publish is refused rather than forwarded. The next
	// sub-test is the one that covers the refusal.
	t.Run("an explicit region is not overridden", func(t *testing.T) {
		rec := &captureRequest{}
		url := regionCreateServer(t, rec, "us-east-2", "eu-west-1")

		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "create",
			"--name", "mydb", "--size", "small",
			"--region", "eu-west-1"); err != nil {
			t.Fatalf("create with --region: %v", err)
		}

		var body map[string]any
		if err := json.Unmarshal([]byte(rec.Body), &body); err != nil {
			t.Fatalf("create body was not JSON: %v (%q)", err, rec.Body)
		}
		if body["region"] != "eu-west-1" {
			t.Errorf("region = %v, want the flag's value", body["region"])
		}
	})

	// Two regions is the case that must NOT resolve. A region is fixed
	// for the life of the database, so guessing is unrecoverable — and
	// refusing here is what stops behaviour changing silently the day a
	// second region appears.
	t.Run("two regions refuse and name them", func(t *testing.T) {
		rec := &captureRequest{}
		url := regionCreateServer(t, rec, "us-west-1", "us-east-2")

		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		err := runAuthed(t, rt, out, url, "database", "create",
			"--name", "mydb", "--size", "small")
		if err == nil {
			t.Fatal("two regions resolved instead of refusing")
		}
		var ee *ExitError
		if !asExitError(err, &ee) || ee.Code() != ExitUsage {
			t.Fatalf("want ExitUsage, got %v", err)
		}
		for _, want := range []string{"us-east-2", "us-west-1"} {
			if !strings.Contains(ee.Error(), want) {
				t.Errorf("the refusal does not name %q: %s",
					want, ee.Error())
			}
		}
		if rec.Body != "" {
			t.Errorf("a create was sent despite the refusal: %q",
				rec.Body)
		}
	})

	t.Run("no regions refuses too", func(t *testing.T) {
		rec := &captureRequest{}
		url := regionCreateServer(t, rec)

		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		err := runAuthed(t, rt, out, url, "database", "create",
			"--name", "mydb", "--size", "small")
		if err == nil {
			t.Fatal("an empty region list resolved to something")
		}
		var ee *ExitError
		if !asExitError(err, &ee) || ee.Code() != ExitUsage {
			t.Fatalf("want ExitUsage, got %v", err)
		}
		if rec.Body != "" {
			t.Errorf("a create was sent despite the refusal: %q",
				rec.Body)
		}
	})
}
