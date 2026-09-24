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
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
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
	// TokenBound reports whether the cached token was minted for the
	// credential AND resolved API URL now configured; see
	// auth.CachedToken.MintedBy. Meaningful only alongside TokenValid.
	// A cache with no fingerprint reports false, as a mismatch does.
	// It is one boolean over one digest, so it cannot say which input
	// diverged; the text wording names both.
	TokenBound bool   `json:"token_bound"`
	ExpiresAt  string `json:"expires_at,omitempty"`
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
}

// accountDoctorReport is the Starfleet connection's report: credential
// resolution, token state, reachability, the tenant that credential
// authenticates as, and the environment those are read from.
type accountDoctorReport struct {
	Auth        authInfo        `json:"auth"`
	API         apiInfo         `json:"api"`
	Tenant      tenantInfo      `json:"tenant"`
	Environment environmentInfo `json:"environment"`
}

// --- command wiring -----------------------------------------------------------

// NewDoctorCmd builds the `pgedge starfleet doctor` command. The
// starfleet root owns f, so the checks below read the one set of
// connection flags bound there.
func NewDoctorCmd(rt *module.Runtime, f *conn.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the pgEdge Starfleet connection",
		Long: `doctor reports which credential source is in effect, whether
the cached token is still valid, whether the resolved API base URL is
reachable, and which tenant the credential authenticates as. It never
fails on missing credentials, so it is safe to run precisely when
authentication is broken.

It changes nothing. Reading the tenant needs a token, so doctor may
exchange one, but that token is used for the single call and never
written to the token cache — including when --api-url points somewhere
other than the profile's own host. Whatever a later command would have
used, it still will.

For the install itself — version, config file, shell — run
'pgedge doctor'.

Example:
  pgedge starfleet doctor
  pgedge starfleet doctor -o json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runDoctor(rt, f)
		},
	}
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
		if conn.IsUsageError(err) || !errors.Is(err, auth.ErrNoCredentials) {
			return authInfo{Problem: err.Error()}
		}
		return authInfo{}
	}

	info := authInfo{Authenticated: true, Source: res.Source}
	if res.Env {
		return info
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
	client := &http.Client{Timeout: 10 * time.Second}

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
func checkTenant(rt *module.Runtime, f *conn.Flags) tenantInfo {
	// --timeout 0 unbounds ordinary commands, but a hung token mint
	// must not hang a diagnostic. A raised --timeout is honoured.
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = conn.RequestTimeout
	}
	client, err := newEphemeralAccountClient(
		rt, f.ClientID, f.ClientSecret, f.APIURL, timeout)
	if err != nil {
		return tenantInfo{}
	}

	ctx, cancel := context.WithTimeout(
		context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ListTenantsWithResponse(ctx)
	if err != nil || resp.JSON200 == nil {
		return tenantInfo{}
	}
	tenants := *resp.JSON200
	if len(tenants) == 0 {
		return tenantInfo{}
	}

	t := tenants[0]
	info := tenantInfo{
		Resolved: true,
		Name:     t.Name,
		ID:       t.Id,
		Count:    len(tenants),
	}
	if t.Plan != nil {
		info.Plan = *t.Plan
	}
	if t.PlanTrial != nil {
		info.PlanTrial = *t.PlanTrial
	}
	return info
}

// tenantCheckRow renders the Tenant check. A plan that is not
// enterprise is a warning, not an error: the credential works, it just
// cannot drive byoc.
func tenantCheckRow(info tenantInfo) output.CheckRow {
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
	detail := fmt.Sprintf("%s (%s, plan: %s)", info.Name, info.ID, plan)
	if info.PlanTrial {
		detail += " [trial]"
	}
	if info.Count > 1 {
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
	report := accountDoctorReport{
		Auth:        checkAuth(rt, f),
		API:         checkAPI(rt, f),
		Tenant:      checkTenant(rt, f),
		Environment: checkEnvironment(),
	}

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
	switch {
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
		output.CheckRow{
			Check:   "Environment",
			Status:  "ok",
			Details: strings.Join(envParts, ", "),
		},
	}

	return rt.Output.Print(rows, output.CheckHeaders())
}
