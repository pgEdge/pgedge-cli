package clitest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A server-controlled string reaching stderr with no escaping forges
// lines: a value carrying a newline produces output a reader — or an
// agent parsing it — cannot tell from the CLI's own. stdout's
// table path is covered by output.Sanitize inside the renderer, which
// no call site can forget. stderr has no such choke point, because the
// format string's own `\n` must pass through while the ARGUMENT's must
// not, and only the call site knows which is which.
//
// So this walks the fmt.Fprintf calls it can SEE -- rt.Stderr,
// rt.Stdout, rt.Output.Out, rt.Output.Err, and names aliased from any
// of them -- and requires each %s
// or %v argument to be a string literal, wrapped in output.Sanitize, or
// listed in stderrSafeArgs below with the reason it cannot carry a
// control character. %v counts because on a string it prints the bytes
// raw, exactly as %s does.
//
// "Can see" is the honest verb. A writer reached through a PARAMETER or
// a CALL is not governed -- see writerAliases -- nor is a write that
// does not go through fmt at all, such as io.WriteString or a bare
// Write of a strings.Builder the server's text was composed into. The
// exact population count at the end of the test is what stands behind
// that last one. So this is not every
// stderr write in the tree. Ten such sites exist in controlplane's interview
// code and two in internal/cli/setup.go; none carries a server value
// today, and that was derived rather than assumed.
//
// Fprint and Fprintln are covered too, but they cannot be checked the
// same way. Their argument is a whole COMPOSED message -- a
// multi-paragraph warning built by a helper -- which legitimately
// carries the newlines Sanitize would escape, so demanding a wrap
// there would break the message. For those the BUILDER is the
// checkpoint, and this gate does the one thing it can: it enumerates
// them and makes each one a reviewed entry in
// stderrComposedMessages. Three live instances were interpolating
// server text unescaped when that enumeration was first written --
// tieNote's instance_name cell, byoc's
// backup-repository summary, and controlplane's orchestrator note -- so the
// enumeration is the point rather than paperwork.
//
// TWO LIMITS remain, both stated rather than implied.
//
// A server string interpolated by fmt.Sprintf into an ERROR message is
// out of scope here, and covered by behavioural tests instead.
//
// And a writer received as an io.Writer PARAMETER is not governed.
// Governing it would mean knowing which caller passed a governed
// writer, which is interprocedural and needs type resolution this repo
// has no dependency for. That population was derived rather than
// assumed -- roughly 40 Fprint* calls across ten files -- and the ones
// interpolating anything non-literal were checked: controlplane's interview
// writers carry only what the user typed at the prompt, and
// completion.go, version.go and llms.go write CLI-authored text. No
// server value reaches a parameter writer today. Widening the gate
// there would add forty entries for a class with no live defect, and
// make the allowlist the bigger artifact than the rule.

// stderrSafeArgs records the %s arguments that are NOT escaped, each
// with the reason, keyed by the expression as it appears in source and
// bound to the files allowed to say it. Two reasons qualify: a TYPE
// that cannot express a control character, and a value the CLI itself
// computed that escaping would actively corrupt. Adding an entry is a
// reviewed statement about a value;
// TestStderrSafeArgAllowlistEntriesAreLive reports one the moment it
// stops matching anything, so the list cannot outlive the code.
var stderrSafeArgs = map[string]struct {
	why   string
	files map[string]bool
}{
	"id": {
		"a UUID type: %s renders hex via String(), so no " +
			"server byte reaches the verb",
		map[string]bool{
			"../starfleet/account/cmd/apiclient.go":     true,
			"../starfleet/account/cmd/invite.go":        true,
			"../starfleet/account/cmd/membership.go":    true,
			"../starfleet/byoc/cmd/backup_store.go":     true,
			"../starfleet/byoc/cmd/cloud_account.go":    true,
			"../starfleet/byoc/cmd/cluster.go":          true,
			"../starfleet/byoc/cmd/database.go":         true,
			"../starfleet/byoc/cmd/ingress.go":          true,
			"../starfleet/byoc/cmd/ssh_key.go":          true,
			"../starfleet/managed/cmd/backup.go":        true,
			"../starfleet/managed/cmd/database.go":      true,
			"../starfleet/managed/cmd/database_size.go": true,
		}},
	"branchID": {
		"a UUID type: %s renders hex via String(), so no " +
			"server byte reaches the verb",
		map[string]bool{
			"../starfleet/managed/cmd/database_branch.go": true,
		}},
	"shareID": {
		"a UUID type: %s renders hex via String(), so no " +
			"server byte reaches the verb",
		map[string]bool{
			"../starfleet/byoc/cmd/cluster_share.go": true,
		}},
	"task.TaskId": {
		"a UUID type: %s renders hex via String(), so no " +
			"server byte reaches the verb",
		map[string]bool{
			"../controlplane/cmd/database_ops.go": true,
			"../controlplane/cmd/wait.go":         true,
		}},
	"resp.JSON200.Task.TaskId": {
		"a UUID type: %s renders hex via String(), so no " +
			"server byte reaches the verb",
		map[string]bool{
			"../controlplane/cmd/host.go": true,
		}},
	"tid": {
		"a UUID type: %s renders hex via String(), so no " +
			"server byte reaches the verb",
		map[string]bool{
			"../controlplane/cmd/task.go": true,
		}},
	"t.TaskId": {
		"a UUID type: %s renders hex via String(), so no " +
			"server byte reaches the verb",
		map[string]bool{
			"../controlplane/cmd/wait.go": true,
		}},
	"path": {
		"the config file path the CLI computed, not a server value -- " +
			"and Sanitize escapes backslashes, which would turn a " +
			"Windows path into one the user cannot paste back",
		map[string]bool{"../starfleet/account/cmd/auth.go": true}},
	"msg": {
		"a task error body, printed verbatim on purpose: it owns the " +
			"region under its own Error heading, it is the last thing " +
			"printed, and these are wrapped %w chains that read better " +
			"with their line structure intact -- a newline there " +
			"cannot forge a field line above it",
		map[string]bool{
			"../starfleet/managed/cmd/task.go": true,
			"../starfleet/byoc/cmd/task.go":    true,
			"../controlplane/cmd/task.go":      true,
		}},
	"title": {
		"a section heading the CLI supplies; every call site passes a " +
			"literal (\"Instance errors\", \"Details\", \"Nodes\", ...)",
		map[string]bool{"../controlplane/cmd/database_detail.go": true}},
	"src.taskID": {
		"a UUID type: %s renders hex via String(), so no " +
			"server byte reaches the verb",
		map[string]bool{
			"../controlplane/cmd/wait.go": true,
		}},
}

// stderrComposedMessages are the non-literal arguments Fprint and
// Fprintln may pass to stderr. The entry is not "this is safe" -- it is
// "someone checked which of this message's parts come from the server,
// and where they are escaped." Keyed by the expression as it appears in
// source and bound to the files allowed to say it, so a NEW composed
// message fails loudly and has to be looked at.
var stderrComposedMessages = map[string]struct {
	why   string
	files map[string]bool
}{
	"cs.URI": {"url.URL percent-encodes the host, the userinfo and " +
		"the path, so no server byte reaches stdout unescaped; " +
		"output.Sanitize is deliberately absent, since it would " +
		"rewrite a real password",
		map[string]bool{
			"../starfleet/managed/cmd/database_connection_string.go": true,
			"../starfleet/byoc/cmd/database_connection_string.go":    true,
		}},
	"n": {"tieNote and singleSampleNote in the same file; tieNote " +
		"escapes the instance_name cell it interpolates and its time " +
		"cell is numeric by construction",
		map[string]bool{
			"../starfleet/managed/cmd/database_metrics.go": true,
			// database_branch_metrics.go's notes loop calls the same
			// helpers over the same MetricSeriesContainer shape.
			"../starfleet/managed/cmd/database_branch_metrics.go": true,
		}},
	"noMetricsMessage(f, window, startTime, endTime)": {
		"composed from flag values only -- no API field reaches it",
		map[string]bool{
			"../starfleet/managed/cmd/database_metrics.go":        true,
			"../starfleet/managed/cmd/database_branch_metrics.go": true,
		}},
	"summary + \".\"": {"the builder escapes all four fields, every " +
		"one a plain string on PgBackrestRepositoryInfo",
		map[string]bool{
			"../starfleet/byoc/cmd/backup_repository.go": true,
		}},
	"w": {"backupStoreWarning over --backup-store-id flag values",
		map[string]bool{"../starfleet/byoc/cmd/cluster.go": true}},
	"orchestratorNote(det)": {"the builder escapes the orchestrator " +
		"list, which comes from api.Host.Orchestrator; its other " +
		"interpolations are %q or the configured base URL",
		map[string]bool{"../controlplane/cmd/database_init.go": true}},
	"systemdPortWarning(n, hid)": {"node name and host id, both from " +
		"the user's own spec file and both rendered %q",
		map[string]bool{"../controlplane/cmd/database_spec_ports.go": true}},
	"\"warning: \" + msg": {"two builders, both reviewed: " +
		"uncheckable renders its only non-literal with %q, and " +
		"floorWarning escapes the server-reported version -- " +
		"belowFloor does NOT constrain it, since parseSemverish " +
		"validates a prefix and the whole string is printed",
		map[string]bool{
			"../starfleet/managed/cmd/catalog.go": true,
			"../controlplane/cmd/floor.go":        true,
		}},

	// The rt.Stdout sites, reviewed when that writer joined
	// the population. Three carry server bytes and are verbatim on
	// purpose; the rest carry none.
	"*ac.Auth0Secret": {"the API client secret, server-controlled and " +
		"deliberately verbatim: it goes to stdout ALONE so it can be " +
		"redirected or piped, and the API shows it once. Escaping it " +
		"would corrupt the credential the user is saving -- a \\n " +
		"written into a secret is a different secret. The prose " +
		"around it goes to stderr, and the sibling Auth0Id IS escaped " +
		"because it is interpolated into a line",
		map[string]bool{"../starfleet/account/cmd/apiclient.go": true}},
	"text": {"auth status: joined from the resolved client id, the " +
		"literal source word, the API URL and a time.Time through a " +
		"fixed layout -- all of them flag or local-config values. No " +
		"API response field reaches it",
		map[string]bool{"../starfleet/account/cmd/auth.go": true}},
	"line": {"a byoc database log line: server-controlled and " +
		"verbatim by the decision recorded at the call site -- it is " +
		"CONTENT under its own `==> node <==` header, and the header's " +
		"node name IS escaped. Nothing is interpolated around the " +
		"line, so a newline in it splits a line the server already " +
		"owned rather than forging a CLI-supplied field",
		map[string]bool{"../starfleet/byoc/cmd/database_logs.go": true}},
	"e.RawText": {"a journald line from a byoc node: same case as " +
		"`line` above and verbatim by the same decision, now recorded " +
		"at that call site too. One entry, one Fprintln, no header " +
		"and no columns. Contrast managed's renderer, which escapes " +
		"because it puts a time and a level on the same line",
		map[string]bool{"../starfleet/byoc/cmd/node_logs.go": true}},
	"spec": {"a DatabaseSpec or restore spec built by an interview " +
		"that reads only rt.Stdin. The one server-influenced input is " +
		"the orchestrator, and classifyOrchestrator coerces it to the " +
		"literals \"systemd\"/\"swarm\"/\"\" before it is stored, so the " +
		"server's own string never reaches the spec",
		map[string]bool{
			"../controlplane/cmd/database_init.go":             true,
			"../controlplane/cmd/database_restore_template.go": true,
		}},
	"buildSpec(v)": {"the non-interactive branch of the same builder: " +
		"it reads only the coerced orchestrator literal, the local " +
		"base URL and an int host count. Every other specValues field " +
		"is its zero value here",
		map[string]bool{"../controlplane/cmd/database_init.go": true}},
	"buildRestoreTemplate()": {"takes no argument and interpolates " +
		"nothing -- three builders writing fixed template text into a " +
		"strings.Builder. Flagged only because a call expression is " +
		"not a literal",
		map[string]bool{
			"../controlplane/cmd/database_restore_template.go": true,
		}},
	"strings.TrimSuffix(cidr, \"/32\")": {"cidr is observedIPv4's " +
		"return value, built from a netip.Addr's own String(), so it " +
		"cannot carry a control character; trimming the /32 suffix " +
		"does not change that",
		map[string]bool{
			"../starfleet/managed/cmd/client_ip.go": true,
		}},
}

// stderrArgIndexRe matches an explicit argument index. It requires the
// digits, so a literal percent before a bracket (`100%%[%s]`) is not
// mistaken for one.
// The exact size of this gate's population. See the assertion at the
// end of TestStderrInterpolationsAreSanitized for why these are exact
// rather than floors.
// 143 -> 152 and 19 -> 20: the `managed database allowlist` verbs and
// the `client-ip` leaf.
// 156 -> 159 and 20 -> 22: `managed database branch metrics`/`logs`,
// reusing database_metrics.go/database_logs.go's own sites.
// 159 -> 163: `auth login` names where the secret went, and `auth
// logout` reports a keychain it could not reach.
// 180 -> 192: the `managed database link` prompt's lists.
const (
	stderrArgSites      = 192
	stderrComposedSites = 22
)

var stderrArgIndexRe = regexp.MustCompile(`%\[\d`)

var stderrVerbRe = regexp.MustCompile(`%[#+\-0-9.\[\]*]*([a-zA-Z%])`)

func stderrFormatVerbs(e ast.Expr) ([]string, bool) {
	var sb strings.Builder
	var walk func(ast.Expr) bool
	walk = func(x ast.Expr) bool {
		switch v := x.(type) {
		case *ast.BasicLit:
			if v.Kind != token.STRING {
				return false
			}
			s, err := strconv.Unquote(v.Value)
			if err != nil {
				return false
			}
			sb.WriteString(s)
			return true
		case *ast.BinaryExpr:
			if v.Op != token.ADD {
				return false
			}
			return walk(v.X) && walk(v.Y)
		}
		return false
	}
	if !walk(e) {
		return nil, false
	}
	// An explicit argument index (`%[2]s`) re-points every following
	// verb at a different argument, so the positional mapping below
	// stops meaning anything -- the same class of failure the `*`
	// handling fixes, but unfixable positionally. No production
	// format uses one today; refusing here keeps it that way rather
	// than letting one arrive and silently mis-map.
	if stderrArgIndexRe.MatchString(sb.String()) {
		return nil, false
	}
	var verbs []string
	for _, m := range stderrVerbRe.FindAllStringSubmatch(sb.String(), -1) {
		if m[1] == "%" {
			continue
		}
		// A `*` in the flags takes its OWN int argument, before the
		// one the verb consumes, so it has to occupy a slot here.
		// Without this `%-*s` counted as one verb while consuming two
		// arguments, which slid every later argument onto the wrong
		// verb and pushed the LAST one off the end of `verbs`, where
		// the loop skips it. managed's log line is `%s  %-*s  %s` and
		// the argument that fell off was the server's log MESSAGE.
		for n := strings.Count(m[0], "*"); n > 0; n-- {
			verbs = append(verbs, "*")
		}
		verbs = append(verbs, m[1])
	}
	return verbs, true
}

// TestStderrFormatVerbsMapsArgumentSlots pins stderrFormatVerbs
// directly, because the gate's protection of its own arithmetic is
// otherwise incidental: reverting the `*` handling only fails the
// suite because an unallowlisted int then lands on a %s, and
// allowlisting that int would make the revert silent. The explicit
// index refusal is pinned by nothing else at all.
//
// One slot per ARGUMENT consumed at run time, in order. A `*` takes
// its own int before the verb's operand, so it gets a slot; `%%`
// consumes nothing, so it gets none.
func TestStderrFormatVerbsMapsArgumentSlots(t *testing.T) {
	tests := []struct {
		format string
		want   []string
		ok     bool
	}{
		{`"%s"`, []string{"s"}, true},
		{`"%v"`, []string{"v"}, true},
		{`"%d %s"`, []string{"d", "s"}, true},
		{`"%%"`, nil, true},
		{`"100%% %s"`, []string{"s"}, true},
		// The live shape: managed's log line.
		{`"%s  %-*s  %s"`, []string{"s", "*", "s", "s"}, true},
		// Two stars consume two ints -- an over- or under-count here
		// slides every later argument onto the wrong verb.
		{`"%*.*s %s"`, []string{"*", "*", "s", "s"}, true},
		{`"%.*s %s"`, []string{"*", "s", "s"}, true},
		{`"%-*v %s"`, []string{"*", "v", "s"}, true},
		// An explicit index re-points the mapping, so it is refused.
		{`"%[1]s"`, nil, false},
		{`"%s %[1]v"`, nil, false},
		// A literal percent before a bracket is NOT an index.
		{`"100%%[%s]"`, []string{"s"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.format, func(t *testing.T) {
			e, err := parser.ParseExpr(tc.format)
			if err != nil {
				t.Fatalf("parse %s: %v", tc.format, err)
			}
			got, ok := stderrFormatVerbs(e)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("verbs = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("verbs = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// isStringLiteral reports a string literal, including a `+` chain of
// them: the 79-column wrap rule splits long messages across several
// literals, and a concatenation of literals carries no server byte.
func isStringLiteral(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		return v.Kind == token.STRING
	case *ast.BinaryExpr:
		return v.Op == token.ADD &&
			isStringLiteral(v.X) && isStringLiteral(v.Y)
	}
	return false
}

// isNarrationWriter reports the writers this gate governs: stderr, and
// both of the ways a command reaches stdout. rt.Output.Out is here
// because the same defect was found there — printTaskDetail writing
// single-line fields under a table the renderer had already sanitized,
// in the stream a caller parses.
//
// rt.Stdout is the THIRD writer and rt.Output.Err the FOURTH.
// Each is a distinct field, so governing one governed
// none of the others — which is how managed's log renderer printed a
// server-controlled level and message unescaped on the stream a
// caller parses. Renderer.Err is governed on its own documentation
// ("Err is for progress/debug lines"): narration, by definition.
//
// Naming all four fields also covers a writer reached through some
// other struct, since every io.Writer field in the tree is called one
// of these. A writer reached through a function RETURN is not covered.
func isNarrationWriter(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "Stderr", "Out", "Stdout", "Err":
		return true
	}
	return false
}

// writerAliases collects the names a file binds a governed writer to —
// `out := rt.Output.Out`, `var out = rt.Stderr`, and `w := out` — so a
// call through one is still governed. Without it the gate reads the
// argument as a bare identifier and skips the call, which is exactly
// how printTaskDetail's sites hid: aliasing the writer took eight interpolations
// out of the population.
//
// Three shapes, and the third is why this iterates. Review added
// `w := out; fmt.Fprintf(w, "Name %s\n", t.Name)` to printTaskDetail itself
// and the gate PASSED with the population unchanged — an
// alias of an alias was invisible. So the walk runs to a fixed point:
// an assignment whose RHS is an identifier already in the set adds its
// LHS too.
//
// Collected per FILE rather than per scope, deliberately: it is the
// cheap direction to be wrong in, because a false positive fails
// LOUDLY (the gate names a site and you look at it) while a false
// negative fails silently. `out` is used for a non-writer elsewhere in
// this tree (internal/cli/dryrunreport.go), so the collision shape is
// real — if it ever bites, scope the check rather than adding an
// allowlist entry, because an entry added to silence a collision would
// then excuse a genuine site with the same name.
//
// A writer that arrives as a PARAMETER, or from a CALL like
// cmd.OutOrStdout(), is still out of reach: knowing whether a governed
// writer was passed in needs type resolution this repo has no
// dependency for. That is a stated limit, not coverage.
func writerAliases(f *ast.File) map[string]bool {
	alias := map[string]bool{}
	record := func(lhs []ast.Expr, rhs []ast.Expr) bool {
		if len(lhs) != 1 || len(rhs) != 1 {
			return false
		}
		id, ok := lhs[0].(*ast.Ident)
		if !ok {
			return false
		}
		governed := isNarrationWriter(rhs[0])
		if !governed {
			// An alias of an alias.
			if src, ok := rhs[0].(*ast.Ident); ok && alias[src.Name] {
				governed = true
			}
		}
		if !governed || alias[id.Name] {
			return false
		}
		alias[id.Name] = true
		return true
	}

	// To a fixed point: `w := out` can be read before `out := rt.Out`.
	for {
		added := false
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.AssignStmt:
				if record(v.Lhs, v.Rhs) {
					added = true
				}
			case *ast.ValueSpec:
				// `var out = rt.Output.Out`
				names := make([]ast.Expr, 0, len(v.Names))
				for _, nm := range v.Names {
					names = append(names, nm)
				}
				if record(names, v.Values) {
					added = true
				}
			}
			return true
		})
		if !added {
			return alias
		}
	}
}

// isGovernedWriter is isNarrationWriter plus the aliases above.
func isGovernedWriter(e ast.Expr, alias map[string]bool) bool {
	if isNarrationWriter(e) {
		return true
	}
	id, ok := e.(*ast.Ident)
	return ok && alias[id.Name]
}

func isSanitizeCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Sanitize" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "output"
}

// isFormattedValue reports a .Format(...) call — a time rendered by the
// CLI through a layout it chose, so no server byte survives into it.
func isFormattedValue(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Format"
}

func stderrGoFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir("..", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// The generated clients are not hand-written and print nothing.
			if d.Name() == "api" {
				return filepath.SkipDir
			}
			// httplog is the --debug transport dump. Reproducing what
			// went over the wire is its entire job, so escaping there
			// would defeat the tool rather than protect anyone -- and
			// it already bounds and redacts what it prints. Excluded as
			// a package, with a reason, rather than as the 12
			// individual entries it would otherwise need, which would
			// read as oversights.
			//
			// Note this is a STDERR exclusion despite the field being
			// named Out: httplog is wired with rt.Stderr at all three
			// call sites (conn.HTTPClientFor, authHTTPClientFor, and
			// controlplane's client).
			if d.Name() == "httplog" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 50 {
		t.Fatalf("walk found only %d files; the tree moved and this "+
			"gate is measuring nothing", len(out))
	}
	return out
}

func TestStderrInterpolationsAreSanitized(t *testing.T) {
	fset := token.NewFileSet()
	var sites, composed int
	for _, path := range stderrGoFiles(t) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		alias := writerAliases(f)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 3 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Fprintf" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "fmt" {
				return true
			}
			if !isGovernedWriter(call.Args[0], alias) {
				return true
			}
			verbs, ok := stderrFormatVerbs(call.Args[1])
			if !ok {
				t.Errorf("%s: fmt.Fprintf to a governed writer "+
					"with a format this gate cannot map to its "+
					"arguments — make it a literal, with no "+
					"explicit argument index (%%[n]s)",
					fset.Position(call.Pos()))
				return true
			}
			for i, arg := range call.Args[2:] {
				if i >= len(verbs) ||
					(verbs[i] != "s" && verbs[i] != "v") {
					continue
				}
				sites++
				if isStringLiteral(arg) {
					continue
				}
				if isSanitizeCall(arg) || isFormattedValue(arg) {
					continue
				}
				expr := types.ExprString(arg)
				if e, ok := stderrSafeArgs[expr]; ok &&
					e.files[path] {
					continue
				}
				t.Errorf("%s: %%s argument %s reaches a governed "+
					"writer unescaped. Wrap it in output.Sanitize, or if "+
					"its TYPE cannot carry a control character, add "+
					"it to stderrSafeArgs with the reason.",
					fset.Position(arg.Pos()), expr)
			}
			return true
		})

		// The Fprint/Fprintln pass. Separate from the Fprintf walk
		// above because there is no format string to read verbs from:
		// the whole argument is the message.
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Fprint" &&
				sel.Sel.Name != "Fprintln") {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok ||
				pkg.Name != "fmt" {
				return true
			}
			if !isGovernedWriter(call.Args[0], alias) {
				return true
			}
			for _, arg := range call.Args[1:] {
				if isStringLiteral(arg) {
					continue
				}
				// An argument the call site already wrapped needs no
				// reviewed entry: the whole message is escaped, which
				// is a stronger statement than an entry could make.
				if isSanitizeCall(arg) {
					continue
				}
				composed++
				expr := types.ExprString(arg)
				if e, ok := stderrComposedMessages[expr]; ok &&
					e.files[path] {
					continue
				}
				t.Errorf("%s: Fprint/Fprintln passes the composed "+
					"message %s to stderr. Its own newlines may be "+
					"deliberate, so wrapping it is usually WRONG -- "+
					"check which parts come from the server, escape "+
					"them in the BUILDER, then record it in "+
					"stderrComposedMessages.",
					fset.Position(arg.Pos()), expr)
			}
			return true
		})
	}
	// EXACT, not a floor. A floor only catches a walk that reaches
	// nothing; it cannot see the population SHRINK, and shrinking is
	// how a site escapes in practice -- review rewrote three
	// interpolations into a strings.Builder emitted with
	// io.WriteString, which is outside this gate's reach, and the
	// count fell from 118 to 116 with every gate still green.
	//
	// So a site that leaves the population fails here, whatever shape
	// it left in. Update these deliberately when a call is genuinely
	// added or removed, the way starfleetHTTPClientSites is updated.
	if sites != stderrArgSites || composed != stderrComposedSites {
		t.Errorf("found %d %%s/%%v arguments and %d composed messages, "+
			"expected %d and %d. If a governed call was added or "+
			"removed, update stderrArgSites/stderrComposedSites; if "+
			"not, one has been rewritten into a shape this walk "+
			"cannot see -- a strings.Builder emitted with "+
			"io.WriteString is the known way",
			sites, composed, stderrArgSites, stderrComposedSites)
	}
}

func TestStderrSafeArgAllowlistEntriesAreLive(t *testing.T) {
	// Re-derived from the AST rather than by searching the file text.
	// A text search is VACUOUS for a short expression: every Go file
	// contains "id", so the largest entry could never be reported
	// stale. What makes an entry live is that a real stderr site
	// actually used it.
	usedArgs := map[string]map[string]bool{}
	usedMsgs := map[string]map[string]bool{}
	note := func(m map[string]map[string]bool, expr, path string) {
		if m[expr] == nil {
			m[expr] = map[string]bool{}
		}
		m[expr][path] = true
	}

	fset := token.NewFileSet()
	for _, path := range stderrGoFiles(t) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		alias := writerAliases(f)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok ||
				pkg.Name != "fmt" {
				return true
			}
			if !isGovernedWriter(call.Args[0], alias) {
				return true
			}
			switch sel.Sel.Name {
			case "Fprintf":
				if len(call.Args) < 3 {
					return true
				}
				verbs, ok := stderrFormatVerbs(call.Args[1])
				if !ok {
					return true
				}
				for i, arg := range call.Args[2:] {
					if i >= len(verbs) ||
						(verbs[i] != "s" && verbs[i] != "v") {
						continue
					}
					note(usedArgs, types.ExprString(arg), path)
				}
			case "Fprint", "Fprintln":
				for _, arg := range call.Args[1:] {
					note(usedMsgs, types.ExprString(arg), path)
				}
			}
			return true
		})
	}

	check := func(name string, want map[string]map[string]bool,
		entries map[string]struct {
			why   string
			files map[string]bool
		},
	) {
		for expr, e := range entries {
			if e.why == "" {
				t.Errorf("%s[%q] carries no reason", name, expr)
			}
			for path := range e.files {
				if !want[expr][path] {
					t.Errorf("%s[%q] names %s, where no stderr site "+
						"passes it any more — the entry excuses "+
						"nothing", name, expr, path)
				}
			}
		}
	}
	check("stderrSafeArgs", usedArgs, stderrSafeArgs)
	check("stderrComposedMessages", usedMsgs, stderrComposedMessages)
}
