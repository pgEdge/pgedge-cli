package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const twoRulesJSON = `[{"cidr":"203.0.113.7/32","label":"office"},
	{"cidr":"198.51.100.0/24"}]`

func TestAllowlistGetPrintsStateAndRules(t *testing.T) {
	url := testsupport.NewAuthedServer(t, stubAllowlist(
		&captureRequest{},
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, ""),
		"203.0.113.9", http.StatusOK, ""))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "get",
		testDatabaseID); err != nil {
		t.Fatalf("allowlist get: %v", err)
	}
	for _, want := range []string{"Endpoint: postgres",
		"State: restricted", "CIDR", "LABEL", "203.0.113.7/32", "office",
		"198.51.100.0/24"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestAllowlistGetClosedSaysSoOnStderr(t *testing.T) {
	url := testsupport.NewAuthedServer(t, stubAllowlist(
		&captureRequest{},
		allowlistDatabaseJSON(testDatabaseID, "[]", ""),
		"203.0.113.9", http.StatusOK, ""))
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "get",
		testDatabaseID); err != nil {
		t.Fatalf("allowlist get: %v", err)
	}
	if !strings.Contains(out.String(), "State: closed") {
		t.Errorf("stdout lacks the state line:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "closed") {
		t.Errorf("stderr lacks the closed note: %q", errOut.String())
	}
}

func TestAllowlistGetServiceReadsThatServicesList(t *testing.T) {
	url := testsupport.NewAuthedServer(t, stubAllowlist(
		&captureRequest{},
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON,
			mcpWithAllowlistJSON),
		"203.0.113.9", http.StatusOK, ""))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "get",
		testDatabaseID, "--service", "mcp"); err != nil {
		t.Fatalf("allowlist get --service mcp: %v", err)
	}
	if !strings.Contains(out.String(), "Endpoint: mcp") ||
		!strings.Contains(out.String(), "198.51.100.0/24") {
		t.Errorf("did not print the mcp list:\n%s", out.String())
	}
	if strings.Contains(out.String(), "203.0.113.7") {
		t.Errorf("printed a postgres rule under --service mcp")
	}
}

func TestAllowlistGetServiceWithoutAllowlistReadsClosed(t *testing.T) {
	url := testsupport.NewAuthedServer(t, stubAllowlist(
		&captureRequest{},
		allowlistDatabaseJSON(testDatabaseID, "[]", mcpNoAllowlistJSON),
		"203.0.113.9", http.StatusOK, ""))
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "get",
		testDatabaseID, "--service", "mcp"); err != nil {
		t.Fatalf("allowlist get --service mcp: %v", err)
	}
	if !strings.Contains(out.String(), "State: closed") {
		t.Errorf("stdout lacks the state line:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "closed") {
		t.Errorf("stderr lacks the closed note: %q", errOut.String())
	}

	rtJSON, outJSON, _ := testsupport.NewRuntime(t, "", "json")
	if err := runAuthed(t, rtJSON, outJSON, url, "database", "allowlist",
		"get", testDatabaseID, "--service", "mcp"); err != nil {
		t.Fatalf("allowlist get --service mcp -o json: %v", err)
	}
	if !strings.Contains(outJSON.String(), `"state"`) ||
		!strings.Contains(outJSON.String(), "closed") {
		t.Errorf("json lacks a derived closed state:\n%s", outJSON.String())
	}
}

func TestAllowlistGetServiceNotDeployedIsNotFound(t *testing.T) {
	url := testsupport.NewAuthedServer(t, stubAllowlist(
		&captureRequest{},
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, ""),
		"203.0.113.9", http.StatusOK, ""))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "allowlist", "get",
		testDatabaseID, "--service", "rag")
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitNotFound {
		t.Errorf("want exit 4, got %v", err)
	}
}

func TestAllowlistGetUnknownServiceWordIsUsage(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, "http://127.0.0.1:9", "database",
		"allowlist", "get", testDatabaseID, "--service", "redis")
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("want exit 2 with nothing sent, got %v", err)
	}
}

func TestAllowlistGetJSONPassesObjectThrough(t *testing.T) {
	url := testsupport.NewAuthedServer(t, stubAllowlist(
		&captureRequest{},
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, ""),
		"203.0.113.9", http.StatusOK, ""))
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "get",
		testDatabaseID); err != nil {
		t.Fatalf("allowlist get -o json: %v", err)
	}
	if !strings.Contains(out.String(), `"rules"`) ||
		!strings.Contains(out.String(), `"state"`) {
		t.Errorf("json lacks rules/state:\n%s", out.String())
	}
}

// allowlistBody decodes the ip_allowlist block of a captured PATCH.
func allowlistBody(t *testing.T, body string) (rules []map[string]any,
	present bool, servicesPresent bool) {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("PATCH body not JSON: %v (%q)", err, body)
	}
	_, servicesPresent = payload["services"]
	raw, present := payload["ip_allowlist"]
	if !present {
		return nil, false, servicesPresent
	}
	var list struct {
		Rules []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("ip_allowlist not an object: %v", err)
	}
	return list.Rules, true, servicesPresent
}

func TestAllowlistAddAppendsAndSendsOnlyTheField(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, ""),
		"203.0.113.9", http.StatusOK,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, "")))
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "add",
		testDatabaseID, "192.0.2.1", "--label", "home"); err != nil {
		t.Fatalf("allowlist add: %v", err)
	}
	if rec.Method != http.MethodPatch {
		t.Fatalf("method = %s, want PATCH", rec.Method)
	}
	rules, present, services := allowlistBody(t, rec.Body)
	if !present || services {
		t.Fatalf("body must carry ip_allowlist and not services: %s",
			rec.Body)
	}
	if len(rules) != 3 || rules[2]["cidr"] != "192.0.2.1" ||
		rules[2]["label"] != "home" {
		t.Errorf("rules = %v", rules)
	}
	if !strings.Contains(errOut.String(), "Monitor with:") {
		t.Errorf("no monitor hint on stderr: %q", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout must stay empty in text mode: %q", out.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rec.Body), &payload); err != nil {
		t.Fatalf("PATCH body not JSON: %v (%q)", err, rec.Body)
	}
	if len(payload) != 1 {
		t.Errorf("body carries %d top-level fields, want 1 "+
			"(ip_allowlist only): %s", len(payload), rec.Body)
	}
}

func TestAllowlistAddSkipsPresentAndSendsNothingWhenAllPresent(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, ""),
		"203.0.113.9", http.StatusOK, ""))
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "add",
		testDatabaseID, "203.0.113.7"); err != nil {
		t.Fatalf("allowlist add: %v", err)
	}
	if rec.Calls != 0 {
		t.Errorf("a write was sent for an already-present rule: %s",
			rec.Body)
	}
	if !strings.Contains(errOut.String(), "Already allowed: 203.0.113.7") ||
		!strings.Contains(errOut.String(), "Nothing to add.") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestAllowlistAddMyIPFetchesAndAppendsSlash32(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, "[]", ""),
		"203.0.113.9", http.StatusOK,
		allowlistDatabaseJSON(testDatabaseID, "[]", "")))
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "add",
		testDatabaseID, "--my-ip"); err != nil {
		t.Fatalf("allowlist add --my-ip: %v", err)
	}
	rules, _, _ := allowlistBody(t, rec.Body)
	if len(rules) != 1 || rules[0]["cidr"] != "203.0.113.9/32" {
		t.Errorf("rules = %v", rules)
	}
	if !strings.Contains(errOut.String(), "not necessarily") {
		t.Errorf("caveat missing: %q", errOut.String())
	}
}

func TestAllowlistAddMyIPRefusesIPv6WithNothingSent(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, "[]", ""),
		"2001:db8::1", http.StatusOK, ""))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "allowlist", "add",
		testDatabaseID, "--my-ip")
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
		t.Errorf("want exit 1, got %v", err)
	}
	if rec.Calls != 0 {
		t.Errorf("a write was sent: %s", rec.Body)
	}
}

func TestAllowlistAddServiceRidesTheServicesArray(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON,
			mcpWithAllowlistJSON),
		"203.0.113.9", http.StatusOK,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, "")))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "add",
		testDatabaseID, "192.0.2.1", "--service", "mcp"); err != nil {
		t.Fatalf("allowlist add --service mcp: %v", err)
	}
	_, present, services := allowlistBody(t, rec.Body)
	if present || !services {
		t.Fatalf("a service write must carry services, not a top-level "+
			"ip_allowlist: %s", rec.Body)
	}
	if !strings.Contains(rec.Body, `"192.0.2.1"`) ||
		!strings.Contains(rec.Body, `"198.51.100.0/24"`) {
		t.Errorf("mcp list not appended in place: %s", rec.Body)
	}
	if !strings.Contains(rec.Body, `"service_id":"mcp00001"`) {
		t.Errorf("service identity not carried: %s", rec.Body)
	}
}

func TestAllowlistAddServicePreservesOtherServicesLists(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON,
			mcpAndRagAllowlistJSON),
		"203.0.113.9", http.StatusOK,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, "")))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "add",
		testDatabaseID, "203.0.113.7", "--service", "mcp"); err != nil {
		t.Fatalf("allowlist add --service mcp: %v", err)
	}
	var payload struct {
		Services []struct {
			ServiceType string `json:"service_type"`
			IPAllowlist struct {
				Rules []map[string]any `json:"rules"`
			} `json:"ip_allowlist"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(rec.Body), &payload); err != nil {
		t.Fatalf("PATCH body not JSON: %v (%q)", err, rec.Body)
	}
	var mcpRules, ragRules []map[string]any
	for _, s := range payload.Services {
		switch s.ServiceType {
		case "mcp":
			mcpRules = s.IPAllowlist.Rules
		case "rag":
			ragRules = s.IPAllowlist.Rules
		}
	}
	if len(mcpRules) != 2 {
		t.Errorf("mcp rules = %v, want 2", mcpRules)
	}
	want := []map[string]any{{"cidr": "192.0.2.0/24"}}
	if len(ragRules) != 1 || ragRules[0]["cidr"] != want[0]["cidr"] ||
		len(ragRules[0]) != 1 {
		t.Errorf("rag rules = %v, want untouched %v", ragRules, want)
	}
}

func TestAllowlistAddOverCapIsUsageWithNothingSent(t *testing.T) {
	rules := "["
	for i := range 50 {
		if i > 0 {
			rules += ","
		}
		rules += fmt.Sprintf(`{"cidr":"10.0.%d.0/24"}`, i)
	}
	rules += "]"
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, rules, ""),
		"203.0.113.9", http.StatusOK, ""))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "allowlist", "add",
		testDatabaseID, "192.0.2.1")
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("want exit 2, got %v", err)
	}
	if rec.Calls != 0 {
		t.Errorf("a write was sent: %s", rec.Body)
	}
}

func TestAllowlistAddLongLabelIsUsageBeforeAnyRequest(t *testing.T) {
	long := strings.Repeat("x", 65)
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, "http://127.0.0.1:9", "database",
		"allowlist", "add", testDatabaseID, "192.0.2.1", "--label", long)
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("want exit 2 with nothing sent, got %v", err)
	}
}

func TestAllowlistRemoveMatchesBareAgainstStoredSlash32(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, ""),
		"203.0.113.9", http.StatusOK,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, "")))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist",
		"remove", testDatabaseID, "203.0.113.7"); err != nil {
		t.Fatalf("allowlist remove: %v", err)
	}
	rules, present, _ := allowlistBody(t, rec.Body)
	if !present || len(rules) != 1 || rules[0]["cidr"] != "198.51.100.0/24" {
		t.Errorf("rules = %v", rules)
	}
}

func TestAllowlistRemoveMissingRuleIsNotFoundWithNothingSent(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, ""),
		"203.0.113.9", http.StatusOK, ""))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "allowlist", "remove",
		testDatabaseID, "192.0.2.1")
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitNotFound {
		t.Errorf("want exit 4, got %v", err)
	}
	if rec.Calls != 0 {
		t.Errorf("a write was sent: %s", rec.Body)
	}
}

func TestAllowlistRemoveLastRuleConfirmsAndSendsEmptyArray(t *testing.T) {
	one := `[{"cidr":"203.0.113.7/32"}]`
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, one, ""),
		"203.0.113.9", http.StatusOK,
		allowlistDatabaseJSON(testDatabaseID, "[]", "")))

	// Without --force and without a TTY the prompt is a usage error
	// and nothing is sent.
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "allowlist", "remove",
		testDatabaseID, "203.0.113.7")
	if err == nil || rec.Calls != 0 {
		t.Fatalf("emptying the list without --force must refuse: err=%v "+
			"calls=%d", err, rec.Calls)
	}

	rt, out, _ = testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist",
		"remove", testDatabaseID, "203.0.113.7", "--force"); err != nil {
		t.Fatalf("remove --force: %v", err)
	}
	if !strings.Contains(rec.Body, `"rules":[]`) {
		t.Errorf("closing must send an empty array, not omit the "+
			"field: %s", rec.Body)
	}
}

func TestAllowlistRemoveServiceLastRuleClosesOnlyThatService(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON,
			mcpWithAllowlistJSON),
		"203.0.113.9", http.StatusOK,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, "")))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "remove",
		testDatabaseID, "198.51.100.0/24", "--service", "mcp",
		"--force"); err != nil {
		t.Fatalf("allowlist remove --service mcp --force: %v", err)
	}
	_, present, services := allowlistBody(t, rec.Body)
	if present || !services {
		t.Fatalf("service remove must ride services: %s", rec.Body)
	}
	if !strings.Contains(rec.Body, `"ip_allowlist":{"rules":[]`) {
		t.Errorf("mcp list not emptied: %s", rec.Body)
	}
}

func TestAllowlistAddOpenCIDRWarns(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, "[]", ""),
		"203.0.113.9", http.StatusOK,
		allowlistDatabaseJSON(testDatabaseID, "[]", "")))
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "add",
		testDatabaseID, "0.0.0.0/0"); err != nil {
		t.Fatalf("allowlist add 0.0.0.0/0: %v", err)
	}
	if !strings.Contains(errOut.String(), "open to every address") {
		t.Errorf("no open warning: %q", errOut.String())
	}
}

func TestAllowlistSetReplacesWholeList(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, ""),
		"203.0.113.9", http.StatusOK,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, "")))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "set",
		testDatabaseID, "192.0.2.1", "192.0.2.2", "--label", "ci"); err != nil {
		t.Fatalf("allowlist set: %v", err)
	}
	rules, _, _ := allowlistBody(t, rec.Body)
	if len(rules) != 2 || rules[0]["cidr"] != "192.0.2.1" ||
		rules[1]["label"] != "ci" {
		t.Errorf("rules = %v", rules)
	}
	if strings.Contains(rec.Body, "203.0.113.7") {
		t.Errorf("set kept an old rule: %s", rec.Body)
	}
}

func TestAllowlistSetWithNoCIDRIsUsage(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, "http://127.0.0.1:9", "database",
		"allowlist", "set", testDatabaseID)
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("want exit 2, got %v", err)
	}
}

func TestAllowlistOpenSendsOneRuleAndWarns(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, ""),
		"203.0.113.9", http.StatusOK,
		allowlistDatabaseJSON(testDatabaseID, `[{"cidr":"0.0.0.0/0"}]`, "")))
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist", "open",
		testDatabaseID); err != nil {
		t.Fatalf("allowlist open: %v", err)
	}
	rules, _, _ := allowlistBody(t, rec.Body)
	if len(rules) != 1 || rules[0]["cidr"] != "0.0.0.0/0" {
		t.Errorf("rules = %v", rules)
	}
	if !strings.Contains(errOut.String(), "open to every address") {
		t.Errorf("no warning: %q", errOut.String())
	}
}

func TestAllowlistClearConfirmsAndSendsEmptyArray(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, ""),
		"203.0.113.9", http.StatusOK,
		allowlistDatabaseJSON(testDatabaseID, "[]", "")))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist",
		"clear", testDatabaseID); err == nil || rec.Calls != 0 {
		t.Fatalf("clear without --force must refuse; err=%v calls=%d",
			err, rec.Calls)
	}
	rt, out, _ = testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist",
		"clear", testDatabaseID, "--force"); err != nil {
		t.Fatalf("clear --force: %v", err)
	}
	if !strings.Contains(rec.Body, `"rules":[]`) {
		t.Errorf("clear must send an empty array: %s", rec.Body)
	}
}

func TestAllowlistClearServiceClosesOnlyThatService(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON,
			mcpWithAllowlistJSON),
		"203.0.113.9", http.StatusOK,
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON, "")))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "allowlist",
		"clear", testDatabaseID, "--service", "mcp", "--force"); err != nil {
		t.Fatalf("clear --service mcp: %v", err)
	}
	_, present, services := allowlistBody(t, rec.Body)
	if present || !services {
		t.Fatalf("service clear must ride services: %s", rec.Body)
	}
	if !strings.Contains(rec.Body, `"ip_allowlist":{"rules":[]`) {
		t.Errorf("mcp list not emptied: %s", rec.Body)
	}
}
