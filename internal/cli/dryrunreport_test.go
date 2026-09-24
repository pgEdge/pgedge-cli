package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// recordWrite drives one write through the dry-run transport so the Run
// holds exactly what a real command would have put there. Building the
// Request by hand would let the test disagree with the transport about
// masking, which is the one property most worth pinning.
func recordWrite(t *testing.T, run *dryrun.Run,
	method, url, body string) {
	t.Helper()
	rt := dryrun.Wrap(nil, run)
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer super-secret")
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("precondition: the write should have been aborted")
	}
}

func TestRenderDryRunTextShowsChecksThenRequest(t *testing.T) {
	rt, stdout, _ := testsupport.NewRuntime(t, "", "text")
	rt.DryRun = dryrun.New()
	rt.DryRun.Pass("database mydb resolved (id 7f3a)")
	rt.DryRun.Pass("no %q service already deployed (deploy intent)", "mcp")
	recordWrite(t, rt.DryRun, http.MethodPatch,
		"https://api.test/managed/v1/databases/7f3a",
		`{"services":[{"service_type":"mcp","init_tokens":"s3cr3t"}]}`)

	if err := RenderDryRun(rt); err != nil {
		t.Fatalf("RenderDryRun: %v", err)
	}
	got := stdout.String()

	for _, want := range []string{
		"checks passed:",
		"✓ database mydb resolved (id 7f3a)",
		`✓ no "mcp" service already deployed (deploy intent)`,
		"would send:",
		"PATCH https://api.test/managed/v1/databases/7f3a",
		`"service_type": "mcp"`,
		"nothing was written; the server was not consulted and may " +
			"still reject this request.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "s3cr3t") {
		t.Fatalf("report leaked a secret:\n%s", got)
	}

	// Order matters: the checks are what justify trusting the request.
	if strings.Index(got, "checks passed:") >
		strings.Index(got, "would send:") {
		t.Error("the request was rendered before the checks")
	}
}

func TestRenderDryRunTextIndentsWithoutReserialising(t *testing.T) {
	rt, stdout, _ := testsupport.NewRuntime(t, "", "text")
	rt.DryRun = dryrun.New()
	// An explicit null and a key order that a map round-trip would
	// reorder. json.Indent must preserve both.
	recordWrite(t, rt.DryRun, http.MethodPost,
		"https://api.test/v1/db",
		`{"zebra":1,"plan_expires_at":null,"alpha":2}`)

	if err := RenderDryRun(rt); err != nil {
		t.Fatal(err)
	}
	got := stdout.String()
	if !strings.Contains(got, `"plan_expires_at": null`) {
		t.Errorf("lost the explicit null:\n%s", got)
	}
	if strings.Index(got, `"zebra"`) > strings.Index(got, `"alpha"`) {
		t.Errorf("keys were reordered, so the report is not a "+
			"faithful preview of the bytes:\n%s", got)
	}
}

func TestRenderDryRunHidesHeadersUnlessDebug(t *testing.T) {
	t.Run("without --debug", func(t *testing.T) {
		rt, stdout, _ := testsupport.NewRuntime(t, "", "text")
		rt.DryRun = dryrun.New()
		recordWrite(t, rt.DryRun, http.MethodPost,
			"https://api.test/v1/db", `{"name":"mydb"}`)
		if err := RenderDryRun(rt); err != nil {
			t.Fatal(err)
		}
		if got := stdout.String(); strings.Contains(got,
			"Content-Type") {
			t.Errorf("headers rendered without --debug:\n%s", got)
		}
	})

	t.Run("with --debug", func(t *testing.T) {
		rt, stdout, _ := testsupport.NewRuntime(t, "", "text")
		rt.Debug = true
		rt.DryRun = dryrun.New()
		recordWrite(t, rt.DryRun, http.MethodPost,
			"https://api.test/v1/db", `{"name":"mydb"}`)
		if err := RenderDryRun(rt); err != nil {
			t.Fatal(err)
		}
		got := stdout.String()
		if !strings.Contains(got, "Content-Type: application/json") {
			t.Errorf("--debug did not render headers:\n%s", got)
		}
		if !strings.Contains(got, "Authorization: ") {
			t.Errorf("--debug dropped the Authorization line:\n%s", got)
		}
		if strings.Contains(got, "super-secret") {
			t.Fatalf("--debug leaked the bearer token:\n%s", got)
		}
	})
}

func TestRenderDryRunEmptyLedger(t *testing.T) {
	rt, stdout, _ := testsupport.NewRuntime(t, "", "text")
	rt.DryRun = dryrun.New()
	recordWrite(t, rt.DryRun, http.MethodDelete,
		"https://api.test/v1/db/7f3a", "")

	if err := RenderDryRun(rt); err != nil {
		t.Fatal(err)
	}
	got := stdout.String()
	if !strings.Contains(got, "checks passed: none") {
		t.Errorf("an empty ledger must say so explicitly:\n%s", got)
	}
	if !strings.Contains(got, "DELETE https://api.test/v1/db/7f3a") {
		t.Errorf("missing the request line:\n%s", got)
	}
	if !strings.Contains(got, "nothing was written; the server was "+
		"not consulted and may still reject this request.") {
		t.Errorf("missing the closing statement:\n%s", got)
	}
}

func TestRenderDryRunNothingWouldBeSent(t *testing.T) {
	rt, stdout, _ := testsupport.NewRuntime(t, "", "text")
	rt.DryRun = dryrun.New()
	rt.DryRun.Pass("name accepted")

	if err := RenderDryRun(rt); err != nil {
		t.Fatal(err)
	}
	got := stdout.String()
	if !strings.Contains(got, "nothing would be sent; the server was "+
		"not consulted.") {
		t.Errorf("want the no-request wording:\n%s", got)
	}
	if strings.Contains(got, "nothing was written; the server was "+
		"not consulted and may still reject this request.") {
		t.Errorf("a run that sent nothing must not claim it stopped "+
			"a write:\n%s", got)
	}
}

func TestRenderDryRunJSONShape(t *testing.T) {
	rt, stdout, _ := testsupport.NewRuntime(t, "", "json")
	rt.DryRun = dryrun.New()
	rt.DryRun.Pass("database mydb resolved (id 7f3a)")
	recordWrite(t, rt.DryRun, http.MethodPatch,
		"https://api.test/managed/v1/databases/7f3a",
		`{"services":[{"service_type":"mcp","init_tokens":"s3cr3t"}]}`)

	if err := RenderDryRun(rt); err != nil {
		t.Fatal(err)
	}
	raw := stdout.String()
	if strings.Contains(raw, "s3cr3t") {
		t.Fatalf("json report leaked a secret:\n%s", raw)
	}

	var got struct {
		DryRun       bool     `json:"dry_run"`
		ChecksPassed []string `json:"checks_passed"`
		Request      *struct {
			Method   string          `json:"method"`
			URL      string          `json:"url"`
			Headers  map[string]any  `json:"headers"`
			Body     json.RawMessage `json:"body"`
			BodyText string          `json:"body_text"`
		} `json:"request"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, raw)
	}
	if !got.DryRun {
		t.Error("dry_run = false")
	}
	if len(got.ChecksPassed) != 1 {
		t.Errorf("checks_passed = %v, want one entry", got.ChecksPassed)
	}
	if got.Request == nil {
		t.Fatal("request is null")
	}
	if got.Request.Method != http.MethodPatch {
		t.Errorf("method = %q", got.Request.Method)
	}
	if got.Request.BodyText != "" {
		t.Errorf("body_text set for a JSON body: %q",
			got.Request.BodyText)
	}
	// body must be a JSON object, not a string: a consumer walks into it.
	var obj map[string]any
	if err := json.Unmarshal(got.Request.Body, &obj); err != nil {
		t.Fatalf("body is not a JSON object: %v (%s)",
			err, got.Request.Body)
	}
	if _, ok := obj["services"]; !ok {
		t.Errorf("body lost its content: %s", got.Request.Body)
	}
	if got.Request.Headers != nil {
		t.Errorf("headers present without --debug: %v",
			got.Request.Headers)
	}
}

func TestRenderDryRunJSONUsesBodyTextForNonJSON(t *testing.T) {
	rt, stdout, _ := testsupport.NewRuntime(t, "", "json")
	rt.DryRun = dryrun.New()
	recordWrite(t, rt.DryRun, http.MethodPost,
		"https://api.test/v1/db", "not json at all")

	if err := RenderDryRun(rt); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Request struct {
			Body     json.RawMessage `json:"body"`
			BodyText string          `json:"body_text"`
		} `json:"request"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Request.BodyText == "" {
		t.Error("body_text empty for a non-JSON payload")
	}
	if len(got.Request.Body) != 0 {
		t.Errorf("body set alongside body_text: %s", got.Request.Body)
	}
}

func TestRenderDryRunJSONHeadersWithDebug(t *testing.T) {
	rt, stdout, _ := testsupport.NewRuntime(t, "", "json")
	rt.Debug = true
	rt.DryRun = dryrun.New()
	recordWrite(t, rt.DryRun, http.MethodPost,
		"https://api.test/v1/db", `{"name":"mydb"}`)

	if err := RenderDryRun(rt); err != nil {
		t.Fatal(err)
	}
	raw := stdout.String()
	if strings.Contains(raw, "super-secret") {
		t.Fatalf("json headers leaked the bearer token:\n%s", raw)
	}
	if !strings.Contains(raw, "Authorization") {
		t.Errorf("--debug dropped the Authorization header:\n%s", raw)
	}
}

// TestRenderDryRunYAMLKeysMatchJSON pins the standing decision that a
// yaml key equals its json key.
func TestRenderDryRunYAMLKeysMatchJSON(t *testing.T) {
	rt, stdout, _ := testsupport.NewRuntime(t, "", "yaml")
	rt.DryRun = dryrun.New()
	rt.DryRun.Pass("name accepted")
	recordWrite(t, rt.DryRun, http.MethodPost,
		"https://api.test/v1/db", `{"name":"mydb"}`)

	if err := RenderDryRun(rt); err != nil {
		t.Fatal(err)
	}
	got := stdout.String()
	for _, want := range []string{
		"dry_run:", "server_validated:", "checks_passed:", "request:",
		"method:", "url:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("yaml missing %q:\n%s", want, got)
		}
	}
}

// tail returns the last part of s, for a readable failure message when
// pinning a suffix.
func tail(s string) string {
	const n = 100
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func TestDryRunTextNamesTheServerCaveat(t *testing.T) {
	rt, stdout, _ := testsupport.NewRuntime(t, "", "text")
	rt.DryRun = dryrun.New()
	recordWrite(t, rt.DryRun, http.MethodDelete,
		"https://api.test/v1/db/7f3a", "")

	if err := RenderDryRun(rt); err != nil {
		t.Fatal(err)
	}
	got := stdout.String()
	want := "nothing was written; the server was not consulted " +
		"and may still reject this request.\n"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("text report ends %q, want %q", tail(got), want)
	}
}

func TestDryRunJSONCarriesServerValidatedFalse(t *testing.T) {
	rt, stdout, _ := testsupport.NewRuntime(t, "", "json")
	rt.DryRun = dryrun.New()
	recordWrite(t, rt.DryRun, http.MethodPost,
		"https://api.test/v1/db", `{"name":"mydb"}`)

	if err := RenderDryRun(rt); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	v, ok := got["server_validated"]
	if !ok {
		t.Fatal("server_validated key missing")
	}
	if v != false {
		t.Fatalf("server_validated = %v, want false", v)
	}
}

func TestRenderDryRunWithoutARunIsAnError(t *testing.T) {
	rt := &module.Runtime{}
	if err := RenderDryRun(rt); err == nil {
		t.Error("want an error when no dry run is in progress")
	}
}
