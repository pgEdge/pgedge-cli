package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/selfupdate"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// doctorDepsWithRelease builds DoctorDeps whose Source reports tag as
// its only (and therefore newest) release. It reuses fixtureSource
// (self_test.go), the same hermetic selfupdate.Source double self
// update's own tests script -- the Source-seam replacement for the
// old stubGitHub(status, body) helper, which pointed checkLatestVersion
// at a local httptest server instead.
func doctorDepsWithRelease(tag string) *DoctorDeps {
	return &DoctorDeps{Source: &fixtureSource{
		releases: []selfupdate.Release{{TagName: tag}},
	}}
}

// doctorDepsWithError builds DoctorDeps whose Source fails Releases
// with err -- the Source-seam replacement for the old
// stubGitHubUnreachable helper, generalized to carry any error
// (including one wrapping selfupdate.ErrGHUnauthenticated) rather than
// only a transport failure.
func doctorDepsWithError(err error) *DoctorDeps {
	return &DoctorDeps{Source: &fixtureSource{releasesErr: err}}
}

// writeConfigFile writes a minimal config.yaml under the isolated HOME
// so checkConfig's "config exists" branch is reachable.
func writeConfigFile(t *testing.T) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".pgedge", "cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"),
		[]byte("profiles: {}\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// writeTokenCache writes a cached Starfleet token for profile. The
// install doctor must report nothing about it — the token cache is the
// starfleet module's object — so this exists only to prove that silence
// under the most tempting conditions.
func writeTokenCache(t *testing.T, profile string, expiresAt time.Time) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".pgedge", "cli", "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	body := `{"access_token":"tok","expires_at":"` +
		expiresAt.Format(time.RFC3339) + `"}`
	if err := os.WriteFile(
		filepath.Join(dir, profile+"-starfleet.json"),
		[]byte(body), 0o600); err != nil {
		t.Fatalf("write token cache: %v", err)
	}
}

// doctorRuntime returns an env-isolated Runtime on profile "dev" with
// an account and a cp connection configured. testsupport.NewRuntime
// supplies the HOME isolation and clears the host variables (NO_COLOR,
// SHELL, XDG_CONFIG_HOME) that production code reads; there are no
// PGEDGE_* config variables left to clear, since the CLI reads none.
func doctorRuntime(t *testing.T, format string) (
	*module.Runtime, *bytes.Buffer,
) {
	t.Helper()
	rt, out, _ := testsupport.NewRuntime(t, "", format)
	rt.Profile = "dev"
	rt.Config.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL: "https://staging.example",
	})
	rt.Config.SetControlplaneProfile("dev", &config.ControlplaneProfile{
		BaseURL: "https://cp.example",
	})
	return rt, out
}

func TestRootDoctorReportsInstallAndConnections(t *testing.T) {
	rt, out := doctorRuntime(t, "json")
	deps := doctorDepsWithError(errors.New("network unreachable"))

	if err := runDoctor(rt, deps); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"install_method", "staging.example", "cp.example",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	// The install doctor must not claim anything about auth, and the
	// token cache is the starfleet module's object: `cloud doctor`
	// reports it as authInfo.TokenValid, which is strictly more useful
	// (valid, not merely present).
	for _, forbidden := range []string{
		"token_valid", "authenticated", "token_exists", "token",
	} {
		if strings.Contains(got, forbidden) {
			t.Errorf("root doctor reported %q; token and auth state "+
				"belong to cloud doctor:\n%s", forbidden, got)
		}
	}
}

// TestRootDoctorIgnoresTokenCache is the mutation guard for the field
// that moved: a valid cached token exists on disk, and the install
// doctor must still not mention it in either format. Restore
// configInfo.TokenExists and this fails.
func TestRootDoctorIgnoresTokenCache(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			rt, out := doctorRuntime(t, format)
			deps := doctorDepsWithError(errors.New("network unreachable"))
			writeConfigFile(t)
			writeTokenCache(t, "dev", time.Now().Add(time.Hour))

			if err := runDoctor(rt, deps); err != nil {
				t.Fatalf("doctor: %v", err)
			}
			if strings.Contains(out.String(), "token") {
				t.Errorf("install doctor mentioned the token cache:\n%s",
					out.String())
			}
		})
	}
}

// TestRootDoctorMakesNoAuthenticatedCall pins the ownership rule from
// the other side: the install doctor must not resolve credentials or
// dial the Starfleet API. Credentials are exported and the profile's
// api_url points at a server that fails the test if it is contacted.
func TestRootDoctorMakesNoAuthenticatedCall(t *testing.T) {
	rt, out := doctorRuntime(t, "json")
	deps := doctorDepsWithError(errors.New("network unreachable"))

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("root doctor contacted the Starfleet API: %s %s",
				r.Method, r.URL.Path)
			w.WriteHeader(http.StatusOK)
		}))
	t.Cleanup(srv.Close)
	rt.Config.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL:       srv.URL,
		ClientID:     "id",
		ClientSecret: "secret",
	})

	if err := runDoctor(rt, deps); err != nil {
		t.Fatal(err)
	}
	// The connection line still reports credentials are available; it
	// just never proves it by authenticating.
	if !strings.Contains(out.String(), `"has_credentials":true`) {
		t.Errorf("expected has_credentials true:\n%s", out.String())
	}
}

func TestRootDoctorTextFullyHealthy(t *testing.T) {
	// "dev", the default build-time Version, is not valid semver, so
	// selfupdate.Resolve would filter a release naming it out of
	// ranking before UpToDate ever saw it -- see the matching note in
	// TestRootDoctorLatestVersionCases.
	prevVersion := Version
	Version = "v0.6.0"
	t.Cleanup(func() { Version = prevVersion })

	rt, out := doctorRuntime(t, "text")
	deps := doctorDepsWithRelease(Version)
	writeConfigFile(t)
	rt.Config.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL:       "https://staging.example",
		ClientID:     "id",
		ClientSecret: "secret",
	})

	if err := runDoctor(rt, deps); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"Version", "Latest version", "up to date", "Config",
		"config.yaml: yes", "Shell",
		"Install method", "Connection", "starfleet (dev)",
		"https://staging.example", "controlplane (dev)", "https://cp.example",
		// Credentials exist, but this command did not use them.
		"credentials found, not verified",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("doctor output missing %q:\n%s", want, s)
		}
	}
	// No auth verdict anywhere in the table.
	if strings.Contains(s, "authenticated") {
		t.Errorf("root doctor rendered an auth verdict:\n%s", s)
	}
}

func TestRootDoctorTextDegraded(t *testing.T) {
	rt, out := doctorRuntime(t, "text")
	deps := doctorDepsWithError(errors.New("network unreachable"))

	if err := runDoctor(rt, deps); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"could not check", "config.yaml: no",
		"no credentials — run 'pgedge starfleet auth login'",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("degraded doctor output missing %q:\n%s", want, s)
		}
	}
}

// TestRootDoctorConnectionStatusIsHonest is the guard for finding 2: a
// connection the install can see is unconfigured must never render as
// healthy, and a connection that does have credentials must not imply
// they were checked. Both halves are asserted on the status field, not
// on prose, so hardcoding status back to "ok" fails the first case.
func TestRootDoctorConnectionStatusIsHonest(t *testing.T) {
	tests := []struct {
		name        string
		creds       bool
		wantStatus  string
		wantDetail  string
		wantNoMatch string
	}{
		{
			name:        "no credentials is never ok",
			creds:       false,
			wantStatus:  "warning",
			wantDetail:  "no credentials",
			wantNoMatch: `"status":"ok"`,
		},
		{
			name:       "credentials present are not claimed verified",
			creds:      true,
			wantStatus: "ok",
			wantDetail: "not verified",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, out := doctorRuntime(t, "json")
			deps := doctorDepsWithError(errors.New("network unreachable"))
			// Only the account connection, so a controlplane row (always "ok",
			// since cp needs no login) cannot mask the assertion.
			rt.Config.SetControlplaneProfile("dev", &config.ControlplaneProfile{})
			if tt.creds {
				rt.Config.SetStarfleetProfile("dev", &config.StarfleetProfile{
					APIURL:       "https://staging.example",
					ClientID:     "id",
					ClientSecret: "secret",
				})
			}

			if err := runDoctor(rt, deps); err != nil {
				t.Fatalf("doctor: %v", err)
			}
			got := out.String()
			want := `"status":"` + tt.wantStatus + `"`
			if !strings.Contains(got, want) {
				t.Errorf("connection status: want %s in\n%s", want, got)
			}
			if !strings.Contains(got, tt.wantDetail) {
				t.Errorf("detail missing %q:\n%s", tt.wantDetail, got)
			}
			if tt.wantNoMatch != "" &&
				strings.Contains(got, tt.wantNoMatch) {
				t.Errorf("unconfigured connection reported %s:\n%s",
					tt.wantNoMatch, got)
			}
		})
	}
}

// doctorRowLine returns the text-table line naming check, so a case
// can assert its status and detail together rather than anywhere in
// the whole report (which would let a "warning" or "could not check"
// belonging to a different row pass the assertion).
func doctorRowLine(t *testing.T, report, check string) string {
	t.Helper()
	for _, line := range strings.Split(report, "\n") {
		if strings.Contains(line, check) {
			return line
		}
	}
	t.Fatalf("no %q row in:\n%s", check, report)
	return ""
}

// TestRootDoctorLatestVersionCases pins the four outcomes
// checkLatestVersion's Source seam can produce, wording per the task
// spec: a newer release, the running release, a Source error wrapping
// selfupdate.ErrGHUnauthenticated, and any other Source error. Each
// case builds its own fixtureSource directly (the Source-seam
// replacement for the old stubGitHub(status, body) helper, which drove
// the same four outcomes by scripting an httptest server's status and
// JSON body instead).
func TestRootDoctorLatestVersionCases(t *testing.T) {
	// The default build-time Version ("dev") is not valid semver, so
	// selfupdate.Resolve would filter a release naming it out of
	// ranking before the "same tag" case ever reached the UpToDate
	// comparison it means to test. A real-shaped tag, restored after
	// the test, is what self_test.go and version_test.go already do
	// for the same reason.
	prevVersion := Version
	Version = "v0.6.0"
	t.Cleanup(func() { Version = prevVersion })

	tests := []struct {
		name          string
		releases      []selfupdate.Release
		err           error
		wantStatus    string
		wantDetail    string
		wantNotDetail string
	}{
		{
			name:       "newer tag available",
			releases:   []selfupdate.Release{{TagName: "v99.0.0"}},
			wantStatus: "warning",
			wantDetail: "v99.0.0 available — run 'pgedge self update'",
		},
		{
			name:       "same tag is up to date",
			releases:   []selfupdate.Release{{TagName: Version}},
			wantStatus: "ok",
			wantDetail: Version + " (up to date)",
		},
		{
			name:       "gh unauthenticated",
			err:        fmt.Errorf("gh releases: %w", selfupdate.ErrGHUnauthenticated),
			wantStatus: "warning",
			wantDetail: "could not check (gh not available/authenticated)",
		},
		{
			name:          "other error",
			err:           errors.New("network unreachable"),
			wantStatus:    "warning",
			wantDetail:    "could not check",
			wantNotDetail: "gh not available",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, out := doctorRuntime(t, "text")
			deps := &DoctorDeps{Source: &fixtureSource{
				releases: tt.releases, releasesErr: tt.err,
			}}
			if err := runDoctor(rt, deps); err != nil {
				t.Fatalf("doctor: %v", err)
			}
			line := doctorRowLine(t, out.String(), "Latest version")
			if !strings.Contains(line, tt.wantStatus) {
				t.Errorf("row %q missing status %q", line, tt.wantStatus)
			}
			if !strings.Contains(line, tt.wantDetail) {
				t.Errorf("row %q missing detail %q", line, tt.wantDetail)
			}
			if tt.wantNotDetail != "" && strings.Contains(line, tt.wantNotDetail) {
				t.Errorf("row %q wrongly carries %q", line, tt.wantNotDetail)
			}
		})
	}
}

// TestRootDoctorNoVersionCheckNeverCallsTheSource: the flag
// exists so an air-gapped operator can run doctor without it dialling
// api.github.com, so the proof is a source that fails the test when
// asked, not a row that reads well.
func TestRootDoctorNoVersionCheckNeverCallsTheSource(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			rt, out := doctorRuntime(t, format)
			src := &fixtureSource{
				releasesErr: errors.New("source must not be called"),
			}
			cmd := NewDoctorCmd(rt, &DoctorDeps{Source: src})
			cmd.SetArgs([]string{"--no-version-check"})
			cmd.SetOut(out)
			cmd.SetErr(out)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("doctor: %v", err)
			}
			if src.releasesCalls != 0 {
				t.Fatalf("the release lookup ran %d time(s)", src.releasesCalls)
			}
			if format == "json" {
				if !strings.Contains(out.String(), `"checked":false`) {
					t.Errorf("json lacks checked:false:\n%s", out.String())
				}
				return
			}
			line := doctorRowLine(t, out.String(), "Latest version")
			for _, want := range []string{"ok", "not checked",
				"--no-version-check"} {
				if !strings.Contains(line, want) {
					t.Errorf("row %q lacks %q", line, want)
				}
			}
		})
	}
	// The control: without the flag the same source IS called, so the
	// assertion above is not satisfied by a source nothing ever reads.
	rt, out := doctorRuntime(t, "text")
	src := &fixtureSource{
		releasesErr: errors.New("source must not be called"),
	}
	cmd := NewDoctorCmd(rt, &DoctorDeps{Source: src})
	cmd.SetArgs([]string{})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if src.releasesCalls != 1 {
		t.Fatalf("control: source consulted %d time(s), want 1",
			src.releasesCalls)
	}
	// A preset on the deps is the flag's default, so a harness that
	// builds the command air-gapped needs no argument.
	rt, out = doctorRuntime(t, "text")
	src = &fixtureSource{releasesErr: errors.New("must not be called")}
	cmd = NewDoctorCmd(rt, &DoctorDeps{Source: src, NoVersionCheck: true})
	cmd.SetArgs([]string{})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if src.releasesCalls != 0 {
		t.Fatalf("preset ignored: source consulted %d time(s)",
			src.releasesCalls)
	}
}

// TestRootDoctorConnectionsUnconfigured covers the fresh-install case:
// no cp section at all, so only the Starfleet connection is listed, at its
// default URL.
func TestRootDoctorConnectionsUnconfigured(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	deps := doctorDepsWithError(errors.New("network unreachable"))

	if err := runDoctor(rt, deps); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "https://api.pgedge.com") {
		t.Errorf("expected the default Starfleet URL:\n%s", s)
	}
	if strings.Contains(s, "cp (") {
		t.Errorf("unconfigured cp must not be listed:\n%s", s)
	}
}

// TestRootDoctorConnectionsMultipleControlplaneURLs covers an HA controlplane profile:
// every candidate base URL earns its own line.
func TestRootDoctorConnectionsMultipleControlplaneURLs(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	deps := doctorDepsWithError(errors.New("network unreachable"))
	rt.Config.SetControlplaneProfile("default", &config.ControlplaneProfile{
		BaseURLs:   []string{"https://cp1.example", "https://cp2.example"},
		ClientCert: "/tmp/client.pem",
		ClientKey:  "/tmp/client.key",
	})

	if err := runDoctor(rt, deps); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"https://cp1.example", "https://cp2.example",
		// cp has no login; the row must say mTLS, not "credentials",
		// so an absent keypair cannot read as an unconfigured account.
		"no login required, mTLS configured, not verified",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
}

// TestRootDoctorControlplaneWithoutMTLSIsNotAWarning pins the asymmetry the
// status rule depends on: cp needs no credentials, so an absent mTLS
// keypair is normal and the row stays "ok" — but it says mTLS disabled
// rather than implying the connection was tested.
func TestRootDoctorControlplaneWithoutMTLSIsNotAWarning(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	deps := doctorDepsWithError(errors.New("network unreachable"))
	rt.Config.SetControlplaneProfile("default", &config.ControlplaneProfile{
		BaseURL: "https://cp.example",
	})

	if err := runDoctor(rt, deps); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	got := out.String()
	if !strings.Contains(got,
		`"module":"controlplane","profile":"default","url":"https://cp.example",`+
			`"has_credentials":false,"status":"ok"`) {
		t.Errorf("controlplane row without mTLS should stay ok:\n%s", got)
	}
	if !strings.Contains(got, "mTLS disabled, not verified") {
		t.Errorf("cp detail should name mTLS, not credentials:\n%s", got)
	}
}

func TestRootDoctorShellUnknown(t *testing.T) {
	t.Setenv("SHELL", "")
	rt, out := doctorRuntime(t, "text")
	deps := doctorDepsWithError(errors.New("network unreachable"))

	if err := runDoctor(rt, deps); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out.String(), "unknown") {
		t.Errorf("expected an unknown shell:\n%s", out.String())
	}
}

// The classifier itself moved to internal/selfupdate.InstallMethodFrom
// (installmethod_test.go there pins the same cases directly); doctor's
// own coverage of it is now indirect, through TestRootDoctorCommand-
// RunsFromTree's "Install method" row below.

func TestRootDoctorCommandRunsFromTree(t *testing.T) {
	rt, out := doctorRuntime(t, "text")
	deps := doctorDepsWithError(errors.New("network unreachable"))

	cmd := NewDoctorCmd(rt, deps)
	var cobraOut bytes.Buffer
	cmd.SetOut(&cobraOut)
	cmd.SetErr(&cobraOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute doctor: %v", err)
	}
	if !strings.Contains(out.String(), "Install method") {
		t.Errorf("doctor output missing 'Install method':\n%s",
			out.String())
	}
}

func TestRootDoctorRejectsStrayArgument(t *testing.T) {
	rt, _ := doctorRuntime(t, "text")
	// nil deps: RunE never runs -- cobra.NoArgs rejects the stray
	// argument before the command body (and its Source seam) executes.
	cmd := NewDoctorCmd(rt, nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"stray"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected a usage error for a stray argument")
	}
}

func TestRootDoctorIsRegisteredOnRoot(t *testing.T) {
	root := NewRootCmd(nil)
	for _, c := range root.Commands() {
		if c.Name() == "doctor" {
			return
		}
	}
	t.Error("pgedge doctor is not registered on the root command")
}

// doctor exists to answer "what is this invocation actually doing",
// and its config row was the one that answered for a different one:
// checkConfig re-derived the DEFAULT path and never saw --config.
// With both files present it printed config_exists: true and
// looked right, so a reader diagnosing a wrong-profile problem was
// shown evidence about a file they had not named.
func TestRootDoctorConfigRowFollowsTheConfigInUse(t *testing.T) {
	elsewhere := filepath.Join(t.TempDir(), "other.yaml")
	if err := os.WriteFile(elsewhere,
		[]byte("current_profile: dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(elsewhere)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	rt.Config = cfg
	deps := doctorDepsWithError(errors.New("network unreachable"))

	if err := runDoctor(rt, deps); err != nil {
		t.Fatal(err)
	}
	// The config.path FIELD, not a substring of the whole report: the
	// connections block carries api.pgedge.com, so a bare
	// Contains(got, ".pgedge") passes whatever the row says. And the
	// PATH, not the dir -- asserting only the directory let a
	// hardcoded "config.yaml" literal in the text row name a file
	// that did not exist while the test stayed green.
	dir, path := doctorConfigRow(t, out.String())
	if path != elsewhere {
		t.Errorf("config.path = %q, want %q — the row is describing a "+
			"file this invocation is not reading", path, elsewhere)
	}
	if dir != filepath.Dir(elsewhere) {
		t.Errorf("config.dir = %q, want %q", dir, filepath.Dir(elsewhere))
	}
	// And the text row must name the real file, not a literal.
	rt2, out2, _ := testsupport.NewRuntime(t, "", "text")
	rt2.Config = cfg
	if err := runDoctor(rt2, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2.String(), filepath.Base(elsewhere)) {
		t.Errorf("text row does not name %s:\n%s",
			filepath.Base(elsewhere), out2.String())
	}
}

// doctorConfigRow pulls config.dir and config.path out of a JSON
// doctor report.
func doctorConfigRow(t *testing.T, report string) (dir, path string) {
	t.Helper()
	var parsed struct {
		Config struct {
			Dir  string `json:"dir"`
			Path string `json:"path"`
		} `json:"config"`
	}
	if err := json.Unmarshal([]byte(report), &parsed); err != nil {
		t.Fatalf("parsing report: %v\n%s", err, report)
	}
	return parsed.Config.Dir, parsed.Config.Path
}

// The control: with no --config, the row still describes the default.
func TestRootDoctorConfigRowDefaultsWhenNotOverridden(t *testing.T) {
	rt, out := doctorRuntime(t, "json")
	deps := doctorDepsWithError(errors.New("network unreachable"))

	if err := runDoctor(rt, deps); err != nil {
		t.Fatal(err)
	}
	want, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if dir, _ := doctorConfigRow(t, out.String()); dir != filepath.Dir(want) {
		t.Errorf("config.dir = %q, want the default %q",
			dir, filepath.Dir(want))
	}
}
