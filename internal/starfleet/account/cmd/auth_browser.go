package cmd

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/pgEdge/pgedge-cli/internal/apidefaults"
	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	accountapi "github.com/pgEdge/pgedge-cli/internal/starfleet/account/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/signin"
)

// signInFlow builds the browser sign-in. A variable so tests can point
// it at a fake Auth0 and stand in for the browser.
var signInFlow = func(rt *module.Runtime, app apidefaults.Auth0App,
	timeout time.Duration) *signin.Flow {
	return &signin.Flow{
		BaseURL:    "https://" + app.Domain,
		ClientID:   app.ClientID,
		Audience:   app.Audience,
		Ports:      signin.DefaultPorts,
		Open:       signin.OpenBrowser,
		Wait:       signin.DefaultWait,
		HTTPClient: conn.SignInHTTPClient(rt, timeout),
		Out:        rt.Stderr,
	}
}

// promptIsInteractive reports whether a person can answer the tenant
// prompt. A variable so tests can stand in for one.
var promptIsInteractive = func(rt *module.Runtime) bool {
	in, ok := rt.Stdin.(*os.File)
	if !ok || !term.IsTerminal(int(in.Fd())) {
		return false
	}
	out, ok := rt.Stdout.(*os.File)
	return ok && term.IsTerminal(int(out.Fd()))
}

// browserLogin signs the user in through the browser and saves the
// profile as a browser session.
func browserLogin(rt *module.Runtime, f *conn.Flags) error {
	apiURL := conn.ResolveAPIURL(
		rt.Config.StarfleetProfile(rt.Profile), f.APIURL)
	app, _, ok := conn.SignInApp(apiURL)
	if !ok {
		// Exit 5, not 2: the command is well formed, and the same line
		// works once the API has a sign-in app.
		return newExitError(fmt.Sprintf("browser sign-in is not "+
			"available for %s yet: sign in with an API client instead, "+
			"with --client-id and --client-secret, or --api-client to "+
			"be prompted for them", apiURL), ExitAuth)
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = conn.RequestTimeout
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	toks, err := signInFlow(rt, app, timeout).Run(ctx)
	if err != nil {
		return newExitError(err.Error(), ExitAuth)
	}

	claims, err := auth.ParseClaims(toks.AccessToken)
	if err != nil {
		return newExitError(fmt.Sprintf("sign-in returned an access "+
			"token the CLI cannot read: %v", err), ExitAuth)
	}
	expiresAt := claims.ExpiresAt()
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(
			time.Duration(toks.ExpiresIn) * time.Second)
	}
	user := signedInUser(claims, toks.IDToken)

	tenantID, label, err := pickTenant(ctx, rt, apiURL,
		toks.AccessToken, claims, f.Tenant, timeout)
	if err != nil {
		return err
	}

	if _, err := conn.SaveSession(rt, apiURL, toks.AccessToken, user,
		expiresAt); err != nil {
		return newExitError(fmt.Sprintf("save session: %v", err),
			ExitAuth)
	}
	// A previous session's refresh token must not outlive this sign-in.
	store := conn.Store(rt)
	renews := false
	if toks.RefreshToken != "" {
		renews = store.SaveSessionRefreshToken(toks.RefreshToken) == nil
	}
	if !renews {
		_ = store.ClearSessionRefreshToken()
	}

	// One profile, one kind of credential: an API client's secret goes.
	// Saved before the secret goes, so a failed save keeps a working
	// API client.
	old := rt.Config.StarfleetProfile(rt.Profile)
	rt.Config.SetStarfleetProfile(rt.Profile, &config.StarfleetProfile{
		APIURL: apiURL, Auth: config.AuthBrowser, TenantID: tenantID})
	if err := rt.Config.Save(); err != nil {
		return fmt.Errorf("save profile: %w", err)
	}
	if old.ClientID != "" && old.ClientSecret == "" {
		if err := keychainDelete(store); err != nil {
			fmt.Fprintf(rt.Stderr, "Warning: could not remove the "+
				"previous client secret from the OS keychain: %s\n",
				output.Sanitize(err.Error()))
		}
	}

	fmt.Fprintf(rt.Stderr, "Signed in as %s.\n", output.Sanitize(user))
	fmt.Fprintf(rt.Stderr, "Tenant: %s\n", output.Sanitize(label))
	if renews {
		fmt.Fprintln(rt.Stderr, "The session renews itself while in use "+
			"and ends after a period without use; run 'pgedge starfleet "+
			"auth login' to start a new one.")
	} else {
		fmt.Fprintf(rt.Stderr, "The session lasts until %s; run 'pgedge "+
			"starfleet auth login' again after that.\n",
			expiresAt.Format(time.RFC3339))
	}
	fmt.Fprintf(rt.Stderr, "Profile %s saved to %s\n",
		output.Sanitize(rt.Profile), output.Sanitize(rt.Config.Path()))
	return nil
}

// signedInUser names the user: the ID token's email, else the access
// token's, else its subject.
func signedInUser(claims *auth.Claims, idToken string) string {
	if idToken != "" {
		if id, err := auth.ParseClaims(idToken); err == nil &&
			id.Email != "" {
			return id.Email
		}
	}
	if claims.Email != "" {
		return claims.Email
	}
	return claims.Subject
}

// pickTenant chooses the tenant the session acts in and a label for it:
// --tenant by ID or name, the only one, a prompt, or the token's
// default.
func pickTenant(ctx context.Context, rt *module.Runtime, apiURL,
	token string, claims *auth.Claims, want string,
	timeout time.Duration,
) (id, label string, err error) {
	ids := claims.Tenants()
	if len(ids) == 0 {
		return "", "", newExitError("the signed-in user belongs to no "+
			"pgEdge Starfleet tenant; accept an invite first", ExitAuth)
	}
	// Names are a convenience: without them the IDs still work.
	tenants := reachableTenants(ctx, rt, apiURL, token, timeout, claims)
	labelFor := func(id string) string {
		for _, t := range tenants {
			if strings.EqualFold(t.Id, id) {
				return t.Name + " (" + t.Id + ")"
			}
		}
		return id
	}

	switch {
	case want != "":
		id, err = matchTenant(want, ids, tenants)
		if err != nil {
			return "", "", err
		}
	case len(ids) == 1:
		id = ids[0]
	case promptIsInteractive(rt):
		id, err = promptTenant(rt, ids, labelFor)
		if err != nil {
			return "", "", err
		}
	default:
		id = claims.TenantID
		if id == "" {
			id = ids[0]
		}
		fmt.Fprintf(rt.Stderr, "This sign-in reaches %d tenants; using "+
			"%s, the account default. Pick another with --tenant.\n",
			len(ids), output.Sanitize(labelFor(id)))
	}
	return id, labelFor(id), nil
}

// matchTenant resolves --tenant against the token's tenant IDs, then
// against the reachable tenants' names.
func matchTenant(want string, ids []string, tenants []accountapi.Tenant,
) (string, error) {
	for _, id := range ids {
		if strings.EqualFold(id, want) {
			return id, nil
		}
	}
	var found []string
	for _, t := range tenants {
		if strings.EqualFold(t.Name, want) {
			found = append(found, t.Id)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", conn.TenantUsageError(want, ids)
	}
	return "", newExitError(fmt.Sprintf("--tenant %s names %d tenants; "+
		"give the ID instead: %s", want, len(found),
		strings.Join(found, ", ")), ExitUsage)
}

// reachableTenants lists the tenants the new token reaches, keeping only
// those in its claims. Any failure returns nil.
func reachableTenants(ctx context.Context, rt *module.Runtime, apiURL,
	token string, timeout time.Duration, claims *auth.Claims,
) []accountapi.Tenant {
	client, err := accountapi.NewClientWithResponses(apiURL,
		accountapi.WithHTTPClient(conn.HTTPClientFor(rt, timeout)),
		accountapi.WithRequestEditorFn(
			func(_ context.Context, req *http.Request) error {
				req.Header.Set("Authorization", "Bearer "+token)
				return nil
			}))
	if err != nil {
		return nil
	}
	resp, err := client.ListTenantsWithResponse(ctx)
	if err != nil || resp.JSON200 == nil {
		return nil
	}
	var out []accountapi.Tenant
	for _, t := range *resp.JSON200 {
		if claims.HasTenant(t.Id) {
			out = append(out, t)
		}
	}
	return out
}

// promptTenant asks which tenant to use.
func promptTenant(rt *module.Runtime, ids []string,
	labelFor func(string) string) (string, error) {
	fmt.Fprintf(rt.Stderr, "This sign-in reaches %d tenants:\n", len(ids))
	for i, id := range ids {
		fmt.Fprintf(rt.Stderr, "  %d) %s\n", i+1,
			output.Sanitize(labelFor(id)))
	}
	fmt.Fprintf(rt.Stderr, "Tenant [1-%d]: ", len(ids))
	line, err := bufio.NewReader(rt.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", newExitError("no tenant chosen", ExitUsage)
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(ids) {
		return "", newExitError(fmt.Sprintf("%q is not a number from 1 "+
			"to %d", strings.TrimSpace(line), len(ids)), ExitUsage)
	}
	return ids[n-1], nil
}
