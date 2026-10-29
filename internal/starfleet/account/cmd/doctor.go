package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	api "github.com/pgEdge/pgedge-cli/internal/starfleet/account/api"
	byocapi "github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/useragent"
	"github.com/spf13/cobra"
)

// --- report structs ----------------------------------------------------------

type authInfo struct {
	Authenticated bool   `json:"authenticated"`
	Source        string `json:"source,omitempty"`
	// Problem names a credential-resolution failure more precise than
	// "not authenticated": a half-supplied flag or env pair, --profile
	// against the env pair, an unreadable keychain. Absent when there
	// are simply no credentials, whose remedy is the Auth row's text.
	Problem    string `json:"problem,omitempty"`
	TokenValid bool   `json:"token_valid"`
	// problemCode is the exit code Problem would cause a command.
	problemCode int
	// TokenBound reports whether the cached token was minted for the
	// credential AND resolved API URL now configured; see
	// auth.CachedToken.MintedBy. Meaningful only alongside TokenValid.
	// A cache with no fingerprint reports false, as a mismatch does.
	// It is one boolean over one digest, so it cannot say which input
	// diverged; the text wording names both.
	TokenBound bool   `json:"token_bound"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	// User is a browser session's email or subject.
	User string `json:"user,omitempty"`
	// Renews reports a browser session that can renew its own token.
	Renews bool `json:"renews"`
}

type apiInfo struct {
	Reachable bool   `json:"reachable"`
	URL       string `json:"url"`
	Status    int    `json:"status,omitempty"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
}

// environmentInfo reports presentation state, not connection steering:
// the platform doctor runs on, and whether NO_COLOR is set. Env
// credentials are reported on the Auth row, as its source.
type environmentInfo struct {
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	NoColorSet bool   `json:"no_color_set"`
}

// tenantInfo names the tenant the active credential authenticates as,
// and its plan.
//
// The plan is why this row exists. A profile carries one Starfleet
// credential, which byoc and managed share, so a profile IS a tenant,
// and the API gates capabilities per plan. A byoc list against a
// managed-plan tenant answers 200 with an empty array, so without this
// row the only symptom is an empty list or a "plan does not allow ..."
// error far from its cause.
type tenantInfo struct {
	// Resolved is false when the tenant could not be read at all, as
	// when auth is broken, distinct from a tenant with no plan.
	Resolved  bool   `json:"resolved"`
	Name      string `json:"name,omitempty"`
	ID        string `json:"id,omitempty"`
	Plan      string `json:"plan,omitempty"`
	PlanTrial bool   `json:"plan_trial,omitempty"`
	// Count makes a credential spanning more than one tenant visible
	// rather than silently reported as its first.
	Count int `json:"count,omitempty"`
	// Skipped says why the tenant was not read, when doctor chose not to.
	Skipped string `json:"skipped,omitempty"`
	// selected: a browser session's chosen tenant, not the first.
	selected bool
	// err is why the read failed, carrying its exit code.
	err error
}

// selectedTenant returns the listed tenant whose ID is id, else one
// carrying only the ID, so a report names the tenant requests use.
func selectedTenant(tenants []api.Tenant, id string) api.Tenant {
	for _, t := range tenants {
		if strings.EqualFold(t.Id, id) {
			return t
		}
	}
	return api.Tenant{Id: id}
}

// accountDoctorReport is the Starfleet connection's report: credential
// resolution, token state, reachability, the tenant that credential
// authenticates as, and the environment those are read from.
type accountDoctorReport struct {
	Verdict     string          `json:"verdict"`
	Auth        authInfo        `json:"auth"`
	API         apiInfo         `json:"api"`
	Tenant      tenantInfo      `json:"tenant"`
	Environment environmentInfo `json:"environment"`
	// BYOC is one of the byoc* values, set only on a ready verdict.
	BYOC string `json:"byoc,omitempty"`
}

// --- command wiring -----------------------------------------------------------

// NewDoctorCmd builds the `pgedge starfleet doctor` command. The
// starfleet root owns f, so the checks below read the one set of
// connection flags bound there.
func NewDoctorCmd(rt *module.Runtime, f *conn.Flags) *cobra.Command {
	var strict bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the pgEdge Starfleet connection",
		Long: `doctor reports which credential source is in effect, whether
the cached token is still valid, whether the resolved API base URL is
reachable, which tenant the credential authenticates as, and whether
that tenant's plan includes BYOC. It never fails on missing
credentials, so it is safe to run precisely when authentication is
broken.

It changes nothing. Reading the tenant needs a token, so doctor may
exchange one, but that token is used for the single call and never
written to the token cache — including when --api-url points somewhere
other than the profile's own host. Whatever a later command would have
used, it still will.

The verdict sums the report up in one word:

  ready                      the tenant read succeeded
  no-credentials             no credential resolves, or the keychain
                             cannot be read (exit 5)
  credentials-misconfigured  a flag pair, env pair or --tenant is
                             malformed (exit 2)
  session-needs-login        the browser sign-in is gone (exit 5)
  session-unverified         the sign-in renews on next use, so it
                             was not read; run 'pgedge starfleet auth
                             status --verify' to renew and check it
                             (exit 5)
  endpoint-unreachable       the API URL does not answer (exit 1)
  credential-rejected        the API refuses the credential (exit 5)
  tenant-unresolved          the tenant could not be read for any
                             other reason (exit 1)

doctor exits 0 whatever the verdict. With --strict it exits with the
code shown when the verdict is not ready. The tenant's plan does not
affect the verdict; read tenant.plan for that.

On a ready verdict, the BYOC row (JSON byoc) reads:

  entitled      the plan includes BYOC
  not-entitled  the plan does not include BYOC; a byoc command
                prints an empty list or exits 5
  unknown       the check failed; run it again

For the install itself — version, config file, shell — run
'pgedge doctor'.

Example:
  pgedge starfleet doctor
  pgedge starfleet doctor -o json
  pgedge starfleet doctor --strict`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runDoctorStrict(rt, f, strict)
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false,
		"Exit non-zero unless the verdict is ready")
	return cmd
}

// --- check implementations ---------------------------------------------------

func checkAuth(rt *module.Runtime, f *conn.Flags) authInfo {
	// The same resolution checkAPI and every command perform: a cached
	// token is bound to the endpoint that minted it, so this row
	// would misreport the moment the two disagreed about the URL.
	res, err := conn.ResolveCredentials(
		rt, f.ClientID, f.ClientSecret, f.APIURL)
	if err != nil {
		// A usage error is not "not authenticated": the profile may
		// hold a good pair that incomplete flags override. An
		// unreadable keychain is named too; its remedy differs.
		if conn.IsUsageError(err) {
			return authInfo{Problem: err.Error(), problemCode: ExitUsage}
		}
		if !errors.Is(err, auth.ErrNoCredentials) {
			return authInfo{Problem: err.Error(), problemCode: ExitAuth}
		}
		return authInfo{}
	}

	info := authInfo{Authenticated: true, Source: res.Source}
	if res.Env {
		return info
	}
	if res.Session {
		return checkSessionAuth(rt, res, f.Tenant, info)
	}
	if f.Tenant != "" {
		return authInfo{Source: res.Source,
			Problem:     conn.ErrTenantWithAPIClient().Error(),
			problemCode: ExitUsage}
	}

	tok, err := conn.Store(rt).LoadToken()
	if err != nil {
		return info
	}

	info.ExpiresAt = tok.ExpiresAt.Format(time.RFC3339)
	info.TokenValid = time.Until(tok.ExpiresAt) > 0
	info.TokenBound = tok.MintedBy(
		res.APIURL, res.Creds.ClientID, res.Creds.ClientSecret)
	return info
}

// checkSessionAuth fills the Auth row for a browser session the way
// conn.Resolve reads it, without renewing it.
func checkSessionAuth(rt *module.Runtime, res *conn.Resolved,
	flagTenant string, info authInfo,
) authInfo {
	s := readSession(rt, res, flagTenant)
	if s.tok != nil {
		info.User = s.tok.User
		info.ExpiresAt = s.tok.ExpiresAt.Format(time.RFC3339)
	}
	info.TokenValid, info.TokenBound = s.valid, s.bound
	info.Renews = s.renews
	if s.problem != nil {
		info.Authenticated = false
		info.Problem = s.problem.Error()
		info.problemCode = cli.ExitCode(s.problem)
	}
	return info
}

func checkAPI(rt *module.Runtime, f *conn.Flags) apiInfo {
	apiURL := conn.ResolveAPIURL(
		rt.Config.StarfleetProfile(rt.Profile), f.APIURL)
	if res, err := conn.ResolveCredentials(
		rt, f.ClientID, f.ClientSecret, f.APIURL); err == nil {
		apiURL = res.APIURL
	}
	info := apiInfo{URL: apiURL}
	// A bare client for an unauthenticated reachability probe; `doctor`
	// takes no --dry-run. A write added here must move to
	// conn.HTTPClientFor first, or it bypasses dry-run.
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: useragent.Wrap(http.DefaultTransport, rt.UserAgent),
	}

	start := time.Now()
	resp, err := client.Get(apiURL)
	elapsed := time.Since(start)

	if err != nil {
		return info
	}
	defer func() { _ = resp.Body.Close() }()

	info.Reachable = true
	info.Status = resp.StatusCode
	info.LatencyMs = elapsed.Milliseconds()
	return info
}

// enterprisePlan is the only plan whose entitlements admit byoc. The
// API denies its capabilities (clusters, cloud accounts, backup stores,
// ssh keys) on every other plan.
const enterprisePlan = "enterprise"

// checkTenant reads the tenant the active credential authenticates as.
//
// Every failure is reported as unresolved rather than returned:
// `starfleet doctor` is run precisely when auth is broken. With
// unresolvable credentials no request is made at all.
//
// It resolves EPHEMERALLY, because doctor must not change the state it
// reports. Through conn.Resolve a cold cache would be filled after
// checkAuth had printed "token expired or missing", and since a cached
// token is bound to the API URL, `doctor --api-url <elsewhere>` would
// evict the working token for the profile's own host.
func checkTenant(
	rt *module.Runtime, f *conn.Flags,
) (tenantInfo, *conn.Conn) {
	// --timeout 0 unbounds ordinary commands, but a hung token mint
	// must not hang a diagnostic. A raised --timeout is honoured.
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = conn.RequestTimeout
	}
	cf := *f
	cf.Timeout = timeout
	client, c, err := newEphemeralAccountClient(rt, cf)
	if err != nil {
		return tenantInfo{err: err}, nil
	}

	ctx, cancel := context.WithTimeout(
		context.Background(), 10*time.Second)
	defer cancel()

	tenants, err := readTenants(ctx, client)
	if err != nil {
		return tenantInfo{err: err}, c
	}
	if len(tenants) == 0 {
		return tenantInfo{}, c
	}

	t := tenants[0]
	if c.Session != nil {
		t = selectedTenant(tenants, c.Session.TenantID)
	}
	info := tenantInfo{
		Resolved: true,
		Name:     t.Name,
		ID:       t.Id,
		Count:    len(tenants),
		selected: c.Session != nil,
	}
	if t.Plan != nil {
		info.Plan = *t.Plan
	}
	if t.PlanTrial != nil {
		info.PlanTrial = *t.PlanTrial
	}
	return info, c
}

// BYOC entitlement values.
const (
	byocEntitled    = "entitled"
	byocNotEntitled = "not-entitled"
	byocUnknown     = "unknown"
)

// checkBYOC lists cloud accounts, the one byoc read the API refuses
// outright on a plan without BYOC; the other byoc lists answer 200
// with an empty array there.
func checkBYOC(c *conn.Conn) string {
	client, err := byocapi.NewClient(c.APIURL,
		byocapi.WithRequestEditorFn(c.BearerEditor),
		byocapi.WithHTTPClient(c.HTTPClient))
	if err != nil {
		return byocUnknown
	}
	ctx, cancel := context.WithTimeout(
		context.Background(), 10*time.Second)
	defer cancel()
	resp, err := client.ListCloudAccounts(ctx)
	if err != nil {
		return byocUnknown
	}
	err = checkEmptyBodyResponse(resp, "cloud account list")
	switch {
	case err == nil:
		return byocEntitled
	case conn.IsPlanDenied(err):
		return byocNotEntitled
	}
	return byocUnknown
}

// readTenants is the authenticated read doctor and `auth status
// --verify` make: the cheapest call the API must authorize.
func readTenants(
	ctx context.Context, client *api.ClientWithResponses,
) ([]api.Tenant, error) {
	resp, err := client.ListTenantsWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	return *resp.JSON200, nil
}

// Doctor verdicts, each with one exit code.
const (
	verdictReady               = "ready"
	verdictNoCredentials       = "no-credentials"
	verdictCredsMisconfigured  = "credentials-misconfigured"
	verdictSessionNeedsLogin   = "session-needs-login"
	verdictSessionUnverified   = "session-unverified"
	verdictEndpointUnreachable = "endpoint-unreachable"
	verdictCredentialRejected  = "credential-rejected" //nolint:gosec // G101: a verdict name
	verdictTenantUnresolved    = "tenant-unresolved"
)

// doctorVerdict reduces a report to one verdict and the exit code
// --strict gives it. Reachability is judged before the tenant read,
// which an unreachable host also fails.
func doctorVerdict(r accountDoctorReport) (verdict string, code int) {
	switch {
	case !r.Auth.Authenticated && r.Auth.problemCode == ExitUsage:
		return verdictCredsMisconfigured, ExitUsage
	case !r.Auth.Authenticated && r.Auth.Source == auth.SourceBrowser:
		return verdictSessionNeedsLogin, ExitAuth
	case !r.Auth.Authenticated:
		return verdictNoCredentials, ExitAuth
	case !r.API.Reachable:
		return verdictEndpointUnreachable, ExitGeneral
	case r.Tenant.Skipped != "":
		return verdictSessionUnverified, ExitAuth
	case conn.IsCredentialRejected(r.Tenant.err):
		return verdictCredentialRejected, ExitAuth
	case !r.Tenant.Resolved:
		return verdictTenantUnresolved, ExitGeneral
	}
	return verdictReady, ExitOK
}

// tenantCheckRow renders the Tenant check. A plan that is not
// enterprise is a warning, not an error: the credential works, it just
// cannot drive byoc.
func tenantCheckRow(info tenantInfo) output.CheckRow {
	if info.Skipped != "" {
		return output.CheckRow{
			Check:   "Tenant",
			Status:  "warning",
			Details: "not read: " + info.Skipped,
		}
	}
	if !info.Resolved {
		return output.CheckRow{
			Check:   "Tenant",
			Status:  "warning",
			Details: "could not resolve tenant (see Auth above)",
		}
	}

	plan := info.Plan
	if plan == "" {
		plan = "unknown"
	}
	name := info.Name
	if name == "" {
		name = "a tenant missing from the tenant list"
	}
	detail := fmt.Sprintf("%s (%s, plan: %s)", name, info.ID, plan)
	if info.PlanTrial {
		detail += " [trial]"
	}
	switch {
	case info.Count > 1 && info.selected:
		detail += fmt.Sprintf(" — %d tenants reachable, reporting the "+
			"selected one", info.Count)
	case info.Count > 1:
		detail += fmt.Sprintf(" — %d tenants visible, reporting the first",
			info.Count)
	}

	status := "ok"
	if plan != enterprisePlan {
		status = "warning"
		detail += fmt.Sprintf(
			"; byoc verbs are entitlement-denied on a %s plan", plan)
	}
	return output.CheckRow{Check: "Tenant", Status: status, Details: detail}
}

func checkEnvironment() environmentInfo {
	return environmentInfo{
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		NoColorSet: os.Getenv("NO_COLOR") != "",
	}
}

// --- runner -------------------------------------------------------------------

func runDoctor(rt *module.Runtime, f *conn.Flags) error {
	return runDoctorStrict(rt, f, false)
}

func runDoctorStrict(rt *module.Runtime, f *conn.Flags, strict bool) error {
	var c *conn.Conn
	report := accountDoctorReport{
		Auth:        checkAuth(rt, f),
		API:         checkAPI(rt, f),
		Environment: checkEnvironment(),
	}
	// Reading the tenant would need the renewal doctor must not make.
	if a := report.Auth; a.Source == auth.SourceBrowser &&
		a.Authenticated && !a.TokenValid {
		report.Tenant.Skipped = "the browser session renews on next use"
	} else {
		report.Tenant, c = checkTenant(rt, f)
	}
	verdict, code := doctorVerdict(report)
	report.Verdict = verdict
	if verdict == verdictReady && c != nil {
		report.BYOC = checkBYOC(c)
	}
	if err := printDoctor(rt, report); err != nil {
		return err
	}
	if strict && code != ExitOK {
		return newExitError(
			"starfleet doctor --strict: verdict is "+verdict, code)
	}
	return nil
}

func printDoctor(rt *module.Runtime, report accountDoctorReport) error {
	if rt.Output.Structured() {
		return rt.Output.Print(report, nil)
	}

	env := report.Environment

	// TokenValid && TokenBound before TokenValid alone: a mismatched
	// cache is still valid by expiry.
	authStatus := "error"
	authDetail := "not authenticated"
	if report.Auth.Problem != "" {
		authDetail = report.Auth.Problem
	}
	session := report.Auth.Source == auth.SourceBrowser
	switch {
	case session && report.Auth.Authenticated:
		authStatus = "ok"
		state := "expires " + report.Auth.ExpiresAt
		switch {
		case !report.Auth.TokenValid:
			state = "token expired, renews on next use"
		case report.Auth.Renews:
			state += ", renews itself"
		}
		authDetail = fmt.Sprintf("signed in as %s via browser (%s)",
			output.Sanitize(report.Auth.User), state)
	case report.Auth.Authenticated && report.Auth.Source == auth.SourceEnv:
		authStatus = "ok"
		authDetail = fmt.Sprintf("authenticated via %s and %s "+
			"(source: env; tokens are never cached)",
			auth.EnvClientID, auth.EnvClientSecret)
	case report.Auth.Authenticated && report.Auth.TokenValid &&
		report.Auth.TokenBound:
		authStatus = "ok"
		authDetail = fmt.Sprintf("authenticated via %s (expires %s)",
			report.Auth.Source, report.Auth.ExpiresAt)
	case report.Auth.Authenticated && report.Auth.TokenValid:
		// Minted for another connection (a rekey, a flag override, or
		// another --api-url); conn.token re-authenticates on the next
		// call, so a warning. Naming only the credential would send an
		// operator who typed --api-url looking for a rekey.
		authStatus = "warning"
		authDetail = fmt.Sprintf(
			"the cached token was minted with a different credential "+
				"or API URL — the next command will re-authenticate "+
				"(source: %s)",
			report.Auth.Source)
	case report.Auth.Authenticated:
		authStatus = "warning"
		authDetail = fmt.Sprintf("token expired or missing (source: %s)",
			report.Auth.Source)
	}

	apiStatus := "error"
	apiDetail := fmt.Sprintf("%s (unreachable)", report.API.URL)
	if report.API.Reachable {
		apiStatus = "ok"
		apiDetail = fmt.Sprintf("%s (%d, %dms)",
			report.API.URL, report.API.Status, report.API.LatencyMs)
	}

	envParts := []string{env.OS + "/" + env.Arch}
	if env.NoColorSet {
		envParts = append(envParts, "NO_COLOR: set")
	}

	rows := []output.Row{
		output.CheckRow{
			Check: "Auth", Status: authStatus, Details: authDetail,
		},
		output.CheckRow{
			Check:   "API connectivity",
			Status:  apiStatus,
			Details: apiDetail,
		},
		tenantCheckRow(report.Tenant),
	}
	if report.BYOC != "" {
		status := "warning"
		if report.BYOC == byocEntitled {
			status = "ok"
		}
		rows = append(rows, output.CheckRow{
			Check: "BYOC", Status: status, Details: report.BYOC,
		})
	}
	rows = append(rows,
		output.CheckRow{
			Check:   "Environment",
			Status:  "ok",
			Details: strings.Join(envParts, ", "),
		},
		output.CheckRow{
			Check:   "Verdict",
			Status:  verdictStatus(report.Verdict),
			Details: report.Verdict,
		},
	)

	return rt.Output.Print(rows, output.CheckHeaders())
}

func verdictStatus(verdict string) string {
	if verdict == verdictReady {
		return "ok"
	}
	return "error"
}
