package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/oapi-codegen/nullable"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// Prefixes are withdrawn: every ID input takes a full UUID and a
// value that is not one is a usage error (#194). This replaces
// TestResolveIDPrefix and the two resolveDatabaseID tests, which
// pinned the resolution that is gone.
//
// This covers the VALUES. That nothing is sent for a bad one is a
// property of the command, not of this function, and is pinned where
// it is observable: internal/clitest's directUUIDCommands runs each
// verb with no credentials at all, so an exit 2 there proves the
// parse ran before the client was built.
func TestParseUUIDArgTakesOnlyAFullUUID(t *testing.T) {
	cases := []struct {
		name, input string
	}{
		{"unique prefix", "7c9e"},
		{"ambiguous prefix", "3f"},
		{"uppercase prefix", "7C9E"},
		{"a name", "mydb"},
		{"empty", ""},
		{"nonsense", "zzzz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseUUIDArg(tc.input, "database ID")
			if err == nil {
				t.Fatalf("%q was accepted", tc.input)
			}
			if got := cli.ExitCode(err); got != ExitUsage {
				t.Errorf("exit %d, want %d: a malformed ID is a bad "+
					"invocation, not a lookup failure", got, ExitUsage)
			}
		})
	}

	t.Run("a full UUID passes", func(t *testing.T) {
		got, err := parseUUIDArg(testDatabaseID, "database ID")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got.String() != testDatabaseID {
			t.Errorf("got %s, want %s", got, testDatabaseID)
		}
	})

	// uuid.Parse accepts spellings `database list` never prints. They
	// are canonicalised rather than refused, which is why the wire
	// value is id.String() and not the argument.
	t.Run("a braced UUID is canonicalised", func(t *testing.T) {
		got, err := parseUUIDArg("{"+testDatabaseID+"}", "database ID")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got.String() != testDatabaseID {
			t.Errorf("got %s, want %s", got, testDatabaseID)
		}
	})
}

func TestParseServiceType(t *testing.T) {
	for _, ok := range []string{"mcp", "rag", "postgrest"} {
		if _, err := parseServiceType(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	_, err := parseServiceType("nope")
	if err == nil {
		t.Fatal("an unknown type was accepted")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("want exit %d, got %v", ExitUsage, err)
	}
	// The message must list the valid types; that is the whole value of
	// checking client-side rather than letting the API 400.
	for _, want := range []string{"mcp", "rag", "postgrest"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
}

func TestParseRole(t *testing.T) {
	for _, ok := range []string{
		"admin", "app", "app_read_only",
		"application", "application_read_only",
	} {
		if _, err := parseRole(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	if _, err := parseRole("superuser"); err == nil {
		t.Fatal("an unknown role was accepted")
	}
}

// TestParseUserType pins the --user-type normalisation table: every
// canonical short form and long-form alias maps to the wire value the
// managed database get endpoint expects, and unknown values fail
// client-side with ExitUsage (CLI-1).
func TestParseUserType(t *testing.T) {
	cases := map[string]api.GetManagedDatabaseParamsUserType{
		"admin":                 api.GetManagedDatabaseParamsUserTypeAdmin,
		"app":                   api.GetManagedDatabaseParamsUserTypeApplication,
		"application":           api.GetManagedDatabaseParamsUserTypeApplication,
		"app_read_only":         api.GetManagedDatabaseParamsUserTypeApplicationReadOnly,
		"application_read_only": api.GetManagedDatabaseParamsUserTypeApplicationReadOnly,
	}
	for in, want := range cases {
		got, err := parseUserType(in)
		if err != nil {
			t.Errorf("%q rejected: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseUserType(%q) = %q, want %q", in, got, want)
		}
	}

	_, err := parseUserType("superuser")
	if err == nil {
		t.Fatal("an unknown user-type was accepted")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("want exit %d, got %v", ExitUsage, err)
	}
}

// TestUnknownUserTypeOffersThreeRoles checks the WHOLE message. A
// Contains check cannot see a role leave the list, because "admin"
// and "app" are substrings of a list that also carries app_read_only.
func TestUnknownUserTypeOffersThreeRoles(t *testing.T) {
	want := `unknown user type "superuser" ` +
		`(expected one of: admin, app, app_read_only)`
	if _, err := parseUserType("superuser"); err == nil ||
		err.Error() != want {
		t.Errorf("parseUserType message = %v, want %q", err, want)
	}
	if _, err := parseBranchUserType("superuser"); err == nil ||
		err.Error() != want {
		t.Errorf("parseBranchUserType message = %v, want %q", err, want)
	}
}

func TestParseBranchUserType(t *testing.T) {
	cases := map[string]api.GetBranchParamsUserType{
		"admin":                 api.GetBranchParamsUserTypeAdmin,
		"app":                   api.GetBranchParamsUserTypeApplication,
		"application":           api.GetBranchParamsUserTypeApplication,
		"app_read_only":         api.GetBranchParamsUserTypeApplicationReadOnly,
		"application_read_only": api.GetBranchParamsUserTypeApplicationReadOnly,
	}
	for in, want := range cases {
		got, err := parseBranchUserType(in)
		if err != nil || got != want {
			t.Errorf("parseBranchUserType(%q) = %q, %v; want %q",
				in, got, err, want)
		}
	}
}

// TestValidateManagedDatabaseName pins the client-side --name check
// against skills/pgedge-managed/SKILL.md's documented rules (CLI-17):
// lowercase letters and digits only, starting with a letter, 50
// characters or fewer. The server itself is looser (it accepted a
// unicode name in the field), so this deliberately narrows what the
// CLI will send.
func TestValidateManagedDatabaseName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid short", "mydb", false},
		{"valid single letter", "a", false},
		{"valid with digits", "db2test", false},
		{"valid max length", strings.Repeat("a", 50), false},
		{"uppercase rejected", "MyDB", true},
		{"hyphen rejected", "my-db", true},
		{"underscore rejected", "my_db", true},
		{"unicode rejected", "café", true},
		{"too long rejected", strings.Repeat("a", 51), true},
		{"leading digit rejected", "1mydb", true},
		{"empty rejected", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateManagedDatabaseName(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("%q was accepted, want rejected", tc.input)
				}
				var ee *ExitError
				if !asExitError(err, &ee) || ee.Code() != ExitUsage {
					t.Errorf("want exit %d, got %v", ExitUsage, err)
				}
				return
			}
			if err != nil {
				t.Errorf("%q rejected: %v", tc.input, err)
			}
		})
	}
}

func TestBuildServiceListReplacesByType(t *testing.T) {
	mcpID, ragID := "mcp-stored", "rag-stored"
	db := &api.ManagedDatabase{
		Services: &[]api.ServiceConfig{
			{ServiceType: api.Mcp, ServiceId: &mcpID},
			{ServiceType: api.Rag, ServiceId: &ragID},
		},
	}
	got := buildServiceList(db, api.ServiceConfig{ServiceType: api.Mcp})

	if len(got) != 2 {
		t.Fatalf("expected 2 services, got %d", len(got))
	}
	// rag survives untouched; the incoming mcp replaces the stored one
	// and ADOPTS its id, which is what makes the write an in-place
	// reconfiguration rather than a new service.
	var sawRag, sawMcp int
	for _, s := range got {
		switch s.ServiceType {
		case api.Rag:
			sawRag++
			if s.ServiceId == nil || *s.ServiceId != ragID {
				t.Errorf("rag's service id was not preserved: %v",
					s.ServiceId)
			}
		case api.Mcp:
			sawMcp++
			if s.ServiceId == nil {
				t.Fatal("the replaced mcp did not adopt the stored " +
					"service id; saas would treat the write as a NEW " +
					"service and demand its API keys")
			}
			if *s.ServiceId != mcpID {
				t.Errorf("mcp service id = %q, want %q",
					*s.ServiceId, mcpID)
			}
		}
	}
	if sawRag != 1 || sawMcp != 1 {
		t.Errorf("rag=%d mcp=%d, want 1 and 1", sawRag, sawMcp)
	}
}

// TestBuildServiceListDoesNotOverrideAnExplicitID pins that a caller
// which already set an id keeps it, so the adoption cannot silently
// rewrite a deliberate choice.
func TestBuildServiceListDoesNotOverrideAnExplicitID(t *testing.T) {
	stored, explicit := "stored", "explicit"
	db := &api.ManagedDatabase{
		Services: &[]api.ServiceConfig{
			{ServiceType: api.Mcp, ServiceId: &stored},
		},
	}
	got := buildServiceList(db, api.ServiceConfig{
		ServiceType: api.Mcp, ServiceId: &explicit,
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 service, got %d", len(got))
	}
	if got[0].ServiceId == nil || *got[0].ServiceId != explicit {
		t.Errorf("service id = %v, want %q", got[0].ServiceId, explicit)
	}
}

func TestBuildServiceListOnEmptyDatabase(t *testing.T) {
	got := buildServiceList(&api.ManagedDatabase{},
		api.ServiceConfig{ServiceType: api.Mcp})
	if len(got) != 1 || got[0].ServiceType != api.Mcp {
		t.Errorf("got %v, want a single mcp entry", got)
	}
}

func TestFindService(t *testing.T) {
	db := &api.ManagedDatabase{
		Services: &[]api.ServiceConfig{{ServiceType: api.Rag}},
	}
	if findService(db, api.Rag) == nil {
		t.Error("deployed service not found")
	}
	if findService(db, api.Mcp) != nil {
		t.Error("an undeployed service was found")
	}
	if findService(&api.ManagedDatabase{}, api.Mcp) != nil {
		t.Error("a service was found on a database with none")
	}
}

// TestGuardServiceIntent covers all four intent×presence combinations
// plus the different-type-deployed case (#117): deploy refuses an
// existing service of the SAME type, and must not be fooled by a
// different type being present.
func TestGuardServiceIntent(t *testing.T) {
	group := "pgedge starfleet managed database mcp"
	state := "running"
	present := &api.ManagedDatabase{
		Id: testDatabaseID,
		Services: &[]api.ServiceConfig{
			{ServiceType: api.Mcp, State: &state},
		},
	}
	absent := &api.ManagedDatabase{Id: testDatabaseID}
	otherType := &api.ManagedDatabase{
		Id:       testDatabaseID,
		Services: &[]api.ServiceConfig{{ServiceType: api.Rag}},
	}

	cases := []struct {
		name     string
		db       *api.ManagedDatabase
		intent   serviceIntent
		wantErr  bool
		wantText string
	}{
		{"deploy, absent: allowed", absent, intentDeploy, false, ""},
		{"deploy, present: refused", present, intentDeploy, true,
			"mcp update"},
		{"update, present: allowed", present, intentUpdate, false, ""},
		{"update, absent: refused", absent, intentUpdate, true,
			"mcp deploy"},
		{"deploy, different type deployed: allowed",
			otherType, intentDeploy, false, ""},
		{"update, different type deployed: refused",
			otherType, intentUpdate, true, "mcp deploy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := guardServiceIntent(&module.Runtime{}, tc.db, api.Mcp, tc.intent, group)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			var ee *ExitError
			if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
				t.Errorf("want exit %d, got %v", ExitGeneral, err)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error %q does not mention %q", err, tc.wantText)
			}
		})
	}
}

// TestGuardServiceIntentDeployMessageNamesTheState pins that the
// deploy-on-existing refusal names the deployed service's state — free
// information off the same GET, and the first thing a user asks.
func TestGuardServiceIntentDeployMessageNamesTheState(t *testing.T) {
	state := "running"
	db := &api.ManagedDatabase{
		Id:       testDatabaseID,
		Services: &[]api.ServiceConfig{{ServiceType: api.Mcp, State: &state}},
	}
	err := guardServiceIntent(&module.Runtime{}, db, api.Mcp, intentDeploy,
		"pgedge starfleet managed database mcp")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "running") {
		t.Errorf("error does not name the deployed state: %v", err)
	}
}

func TestJoinFlagsAndPluralIs(t *testing.T) {
	cases := map[string][]string{
		"a":          {"a"},
		"a and b":    {"a", "b"},
		"a, b and c": {"a", "b", "c"},
	}
	for want, in := range cases {
		if got := joinFlags(in); got != want {
			t.Errorf("joinFlags(%v) = %q, want %q", in, got, want)
		}
	}
	if joinFlags([]string{"x"}) != "x" {
		t.Error("single flag should not be decorated")
	}
	if pluralIs(1) != "is" || pluralIs(2) != "are" {
		t.Error("pluralIs disagrees with its name")
	}
}

func TestParsePipelineConfig(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantLen int
		wantErr bool
	}{
		{"bare array", validPipelines, 1, false},
		{"object form", `{"pipelines":` + validPipelines + `}`, 1, false},
		{"object with empty list", `{"pipelines":[]}`, 0, false},
		{"object missing the key", `{"pipeline":[]}`, 0, true},
		{"not json", `nonsense`, 0, true},
		{"object, bad pipelines", `{"pipelines":3}`, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePipelineConfig([]byte(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tc.wantLen {
				t.Errorf("got %d pipelines, want %d", len(got), tc.wantLen)
			}
		})
	}
}

func TestValidatePipelines(t *testing.T) {
	table := func(t api.RAGEmbeddingTable) []api.RAGEmbeddingTable {
		return []api.RAGEmbeddingTable{t}
	}
	good := api.RAGEmbeddingTable{
		Table: "t", TextColumn: "c", VectorColumn: "v"}

	cases := []struct {
		name string
		in   []api.RAGPipelineConfig
		ok   bool
	}{
		{"valid", []api.RAGPipelineConfig{
			{Name: "docs", Tables: table(good)}}, true},
		{"empty list", nil, false},
		{"missing name", []api.RAGPipelineConfig{
			{Tables: table(good)}}, false},
		{"reserved name", []api.RAGPipelineConfig{
			{Name: ragReservedPipelineName, Tables: table(good)}}, false},
		{"duplicate name", []api.RAGPipelineConfig{
			{Name: "d", Tables: table(good)},
			{Name: "d", Tables: table(good)}}, false},
		{"no tables", []api.RAGPipelineConfig{{Name: "d"}}, false},
		{"table missing name", []api.RAGPipelineConfig{{Name: "d",
			Tables: table(api.RAGEmbeddingTable{
				TextColumn: "c", VectorColumn: "v"})}}, false},
		{"table missing text column", []api.RAGPipelineConfig{{Name: "d",
			Tables: table(api.RAGEmbeddingTable{
				Table: "t", VectorColumn: "v"})}}, false},
		{"table missing vector column", []api.RAGPipelineConfig{{Name: "d",
			Tables: table(api.RAGEmbeddingTable{
				Table: "t", TextColumn: "c"})}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePipelines("p.json", tc.in)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestServiceRowHandlesAbsentFields(t *testing.T) {
	row := serviceRow(api.ServiceConfig{ServiceType: api.Mcp})
	cols := row.Columns()
	if len(cols) != len(serviceColumns) {
		t.Fatalf("got %d columns, want %d", len(cols), len(serviceColumns))
	}
	if cols[1] != "mcp" {
		t.Errorf("type column = %q, want mcp", cols[1])
	}
	// State and endpoint are both absent here, and must render as
	// empty cells rather than "<nil>" or a bare "https:///mcp".
	if cols[2] != "" {
		t.Errorf("state column = %q, want empty", cols[2])
	}
	if cols[3] != "" {
		t.Errorf("endpoint column = %q, want empty", cols[3])
	}
}

func TestServiceEndpoint(t *testing.T) {
	const domain = "demo-db.use2.example.com"

	cases := []struct {
		name string
		svc  api.ServiceConfig
		want string
	}{{
		// The server's own locator wins over anything derived here.
		// Observed live 2026-08-17 on the sanctioned test database.
		name: "reported uri is used verbatim",
		svc: api.ServiceConfig{
			ServiceType:  api.Mcp,
			PublicDomain: nullable.NewNullableWithValue(domain),
			Uri: nullable.NewNullableWithValue(
				"https://" + domain + "/mcp"),
		},
		want: "https://" + domain + "/mcp",
	}, {
		// The whole point of preferring uri: if saas ever moves a
		// service's path, the CLI follows without a release. A derived
		// value that disagreed would be the stale one.
		name: "reported uri wins over the derived segment",
		svc: api.ServiceConfig{
			ServiceType:  api.Postgrest,
			PublicDomain: nullable.NewNullableWithValue(domain),
			Uri: nullable.NewNullableWithValue(
				"https://" + domain + "/somewhere-else"),
		},
		want: "https://" + domain + "/somewhere-else",
	}, {
		// An API that predates saas #1868 omits the field entirely.
		name: "mcp appends its segment",
		svc: api.ServiceConfig{
			ServiceType:  api.Mcp,
			PublicDomain: nullable.NewNullableWithValue(domain),
		},
		want: "https://" + domain + "/mcp",
	}, {
		name: "rag appends its segment",
		svc: api.ServiceConfig{
			ServiceType:  api.Rag,
			PublicDomain: nullable.NewNullableWithValue(domain),
		},
		want: "https://" + domain + "/rag",
	}, {
		// The mapping is not the identity. saas routes postgrest
		// under "rest", so deriving the segment from the type name
		// would build a URL that 404s. Live check 2026-08-08: the
		// real /mcp answered 401 while a wrong segment answered 404.
		name: "postgrest routes under rest, not its type name",
		svc: api.ServiceConfig{
			ServiceType:  api.Postgrest,
			PublicDomain: nullable.NewNullableWithValue(domain),
		},
		want: "https://" + domain + "/rest",
	}, {
		name: "absent domain yields no endpoint",
		svc:  api.ServiceConfig{ServiceType: api.Mcp},
		want: "",
	}, {
		name: "explicit null domain yields no endpoint",
		svc: api.ServiceConfig{
			ServiceType:  api.Mcp,
			PublicDomain: nullable.NewNullNullable[string](),
		},
		want: "",
	}, {
		// Guards against "https:///mcp", which looks dialable.
		name: "empty domain yields no endpoint",
		svc: api.ServiceConfig{
			ServiceType:  api.Mcp,
			PublicDomain: nullable.NewNullableWithValue(""),
		},
		want: "",
	}, {
		name: "unknown service type yields no endpoint",
		svc: api.ServiceConfig{
			ServiceType:  api.ServiceConfigServiceType("wat"),
			PublicDomain: nullable.NewNullableWithValue(domain),
		},
		want: "",
	}, {
		// A type the CLI has no segment for is exactly where the
		// server's uri is the only locator available, so the
		// unknown-type case above must not swallow it.
		name: "unknown service type still renders a reported uri",
		svc: api.ServiceConfig{
			ServiceType:  api.ServiceConfigServiceType("wat"),
			PublicDomain: nullable.NewNullableWithValue(domain),
			Uri: nullable.NewNullableWithValue(
				"https://" + domain + "/wat"),
		},
		want: "https://" + domain + "/wat",
	}, {
		// The spec says uri is omitted until the domain is assigned,
		// so null uri with a domain present means an older API, not a
		// service with no locator — fall back rather than blank out.
		name: "explicit null uri falls back to the derived segment",
		svc: api.ServiceConfig{
			ServiceType:  api.Rag,
			PublicDomain: nullable.NewNullableWithValue(domain),
			Uri:          nullable.NewNullNullable[string](),
		},
		want: "https://" + domain + "/rag",
	}, {
		name: "empty uri falls back to the derived segment",
		svc: api.ServiceConfig{
			ServiceType:  api.Mcp,
			PublicDomain: nullable.NewNullableWithValue(domain),
			Uri:          nullable.NewNullableWithValue(""),
		},
		want: "https://" + domain + "/mcp",
	}, {
		// Nothing to report and nothing to derive.
		name: "no uri and no domain yields no endpoint",
		svc: api.ServiceConfig{
			ServiceType: api.Mcp,
			Uri:         nullable.NewNullNullable[string](),
		},
		want: "",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serviceEndpoint(tc.svc); got != tc.want {
				t.Errorf("serviceEndpoint() = %q, want %q",
					got, tc.want)
			}
		})
	}
}

// TestServicePathSegmentsMatchAPIEnum fails when the API grows a
// service type that servicePathSegments has no entry for. Without it a
// new type silently renders an empty ENDPOINT, which reads as "not
// provisioned yet" rather than "the CLI does not know this type".
func TestServicePathSegmentsMatchAPIEnum(t *testing.T) {
	for _, st := range []api.ServiceConfigServiceType{
		api.Mcp, api.Rag, api.Postgrest,
	} {
		if !st.Valid() {
			t.Fatalf("%q is not a valid enum member; this test's "+
				"list has drifted from the generated client", st)
		}
		if _, ok := servicePathSegments[st]; !ok {
			t.Errorf("no path segment for service type %q", st)
		}
	}
}

func TestDatabaseRowHandlesAbsentPgVersion(t *testing.T) {
	row := databaseRowFrom(api.ManagedDatabase{
		Id: "i", Name: "n", Status: "s", Region: "r", Size: "z"})
	cols := row.Columns()
	if len(cols) != len(databaseColumns) {
		t.Fatalf("got %d columns, want %d", len(cols), len(databaseColumns))
	}
}

// TestPureRoutersPrintHelp walks every group command and requires it to
// print help rather than silently doing nothing.
func TestPureRoutersPrintHelp(t *testing.T) {
	testsupport.WalkPureRouters(t, NewManagedCmd(nil))
}

// TestTokenExchangeFailureSurfaces drives a command against a server
// that fails the token exchange itself.
func TestTokenExchangeFailureSurfaces(t *testing.T) {
	url := newRawServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "list")
	if err == nil {
		t.Fatal("a failed token exchange was reported as success")
	}
}

func TestBuildServiceListCarriesAllowlistForward(t *testing.T) {
	rules := []api.IPAllowlistRule{{Cidr: "198.51.100.0/24"}}
	sid := "mcp00001"
	db := &api.ManagedDatabase{Services: &[]api.ServiceConfig{{
		ServiceId:   &sid,
		ServiceType: api.ServiceConfigServiceType("mcp"),
		IpAllowlist: &api.IPAllowlist{Rules: rules},
	}}}
	got := buildServiceList(db, api.ServiceConfig{
		ServiceType: api.ServiceConfigServiceType("mcp")})
	if len(got) != 1 || got[0].IpAllowlist == nil ||
		len(got[0].IpAllowlist.Rules) != 1 {
		t.Fatalf("reconfigured service lost its allowlist: %+v", got)
	}
	explicit := buildServiceList(db, api.ServiceConfig{
		ServiceType: api.ServiceConfigServiceType("mcp"),
		IpAllowlist: &api.IPAllowlist{Rules: []api.IPAllowlistRule{}},
	})
	if len(explicit[0].IpAllowlist.Rules) != 0 {
		t.Errorf("an explicit allowlist on the new config must win")
	}
}
