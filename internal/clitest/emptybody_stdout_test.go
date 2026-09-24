package clitest

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
)

// The empty-body acknowledgement contract.
//
// A verb whose success carries no body has nothing real to print, and
// the CLI prints only real API objects. So the acknowledgement
// is exit 0, and the human sentence goes to STDERR, leaving stdout
// byte-empty in every output format. `cmd -o json | jq` stays clean,
// and a script reads $? rather than a fabricated `{"rotated": true}`
// that would invite it to ignore the exit status.
//
// The alternative considered and rejected was printing the API's own
// X-Request-Id. It is a real server value rather than an invented one,
// but the API was seen returning TWO of them with different values,
// so the CLI would have to pick one and the one it picked need not be
// the one in the server's logs — which was the entire point of
// surfacing it.
//
// This gate is behavioural: it runs the real tree against a stub and
// reads the real file descriptors. It does not compare two copies of
// prose, and it cannot be satisfied by editing a sentence.

// emptyBodyMustCover names every verb the sweep is required to
// classify as an empty-body success.
//
// It is not the population — the population is DERIVED, by running
// each mutating verb against a 204 and seeing which ones succeed. This
// list exists because that derivation can silently shrink: a verb that
// gains a required flag synthesizeArgs cannot fill stops reaching its
// write, drops out of the derived set, and is then no longer checked.
// A count could not notice, since the count would drop with it.
//
// So the rule is the one the dry-run sweep learned the hard way: name
// the verbs, because a floor cannot hold one.
var emptyBodyMustCover = []string{
	"pgedge starfleet byoc backup create",
	"pgedge starfleet byoc backup-store delete",
	"pgedge starfleet byoc cloud-account delete",
	"pgedge starfleet byoc cluster delete",
	"pgedge starfleet byoc cluster share delete",
	"pgedge starfleet byoc database delete",
	"pgedge starfleet byoc database restore",
	"pgedge starfleet byoc database rotate-password",
	"pgedge starfleet byoc ingress delete",
	"pgedge starfleet byoc ingress service deregister",
	"pgedge starfleet byoc ssh-key delete",
	"pgedge starfleet client delete",
	"pgedge starfleet invite delete",
	"pgedge starfleet managed database branch delete",
	"pgedge starfleet managed database delete",
	"pgedge starfleet managed database resize",
	"pgedge starfleet managed database rotate-password",
	"pgedge starfleet membership delete",
	// The only operation in any vendored spec that DECLARES 204 and
	// whose CLI verb generic synthesis cannot reach: --token and
	// --server-url are validated by hand before the request. It is
	// here, and in sweepOverrides, because the declared 204 is exactly
	// the case this contract is about.
	"pgedge controlplane cluster join",
}

// emptyBodyCarriesABody names verbs whose success DOES carry a body,
// as the negative control. Each must fail against a 204 rather than be
// classified as an empty-body success.
//
// What it guards is the FIXTURE, not — as the obvious reading has it —
// the stdout assertion, which stays green on its own when gutted.
// Measured -- drop the Content-Type from the stub's 204 and the
// classified population balloons from 18 to 52, because without it the
// generated catch-all never fires and every verb "succeeds" on an
// empty body. All three of these fire in that state. The header is the
// whole point of the fixture, and this is what notices its loss.
var emptyBodyCarriesABody = []string{
	"pgedge starfleet managed database create",
	"pgedge starfleet byoc cluster share create",
	"pgedge controlplane database delete",
}

// TestASuccessCarryingNoBodyPrintsNothingToStdout runs every mutating
// verb against a stub that answers each write with 204 plus a JSON
// content type, and holds the contract on the ones that succeed.
//
// The 204 carries a Content-Type deliberately: that is the hostile
// shape the per-module empty-body tests use, and answering without one
// would exercise a path no server change can produce.
func TestASuccessCarryingNoBodyPrintsNothingToStdout(t *testing.T) {
	for _, shape := range emptySuccessShapes {
		t.Run(shape.name, func(t *testing.T) {
			sweepEmptySuccess(t, shape)
		})
	}
}

// emptySuccessShape is one way a server can answer success with
// nothing usable in the body.
type emptySuccessShape struct {
	name   string
	status int
	body   string
	// canonical marks the shape the population census is taken from.
	// Only one shape can hold it: the census is an equality, and two
	// shapes need not classify the same verbs.
	canonical bool
}

// emptySuccessShapes are BOTH shapes this repo has measured, and one
// fixture cannot stand for the other.
//
// A fabricated payload survives a single-shape gate by echoing the
// body only when one arrived, which the 204 pass can never produce,
// so the response SHAPE has to vary.
var emptySuccessShapes = []emptySuccessShape{
	// managed's shape.
	{name: "204 with no body", status: http.StatusNoContent,
		canonical: true},
	// byoc's shape, and not hypothetical: against specs that declare
	// no content, byoc's success arrives as 200 carrying the JSON
	// literal `null`. Verified
	// live 2026-08-04 and recorded in
	// internal/starfleet/byoc/cmd/emptybody_run_test.go, which is where
	// that measurement lives.
	{name: "200 with a null body", status: http.StatusOK, body: "null"},
}

func sweepEmptySuccess(t *testing.T, shape emptySuccessShape) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			// The token exchange first: it is a POST, so the write
			// branch below would otherwise answer it 204 and no verb
			// would ever authenticate.
			if strings.HasSuffix(r.URL.Path, tokenPath) {
				_, _ = w.Write([]byte(`{"access_token":"tok",` +
					`"token_type":"Bearer","expires_in":3600}`))
				return
			}
			// isMutatingGETPath, not the HTTP method. controlplane's two
			// state-changing GETs (cluster init, task cancel) would
			// otherwise be served a read body, which is how the first
			// version of this sweep reported both as printing a
			// fabricated zero-valued object to stdout: the fixture,
			// not the CLI, put it there.
			read := (r.Method == http.MethodGet ||
				r.Method == http.MethodHead) &&
				!isMutatingGETPath(r.URL.Path)
			if !read {
				w.WriteHeader(shape.status)
				if shape.body != "" {
					_, _ = w.Write([]byte(shape.body))
				}
				return
			}
			switch {
			case strings.HasSuffix(r.URL.Path, "/v1/version"):
				_, _ = w.Write([]byte(`{"version":"0.10.0"}`))
			case strings.HasSuffix(r.URL.Path, "/regions"):
				_, _ = w.Write([]byte(`[{"region":"us-east-1"}]`))
			case strings.HasSuffix(r.URL.Path, "/sizes"):
				_, _ = w.Write([]byte(`[{"name":"small"}]`))
			case strings.HasSuffix(r.URL.Path, "/nodes"):
				_, _ = w.Write([]byte(stubNodes))
			case strings.Contains(r.URL.Path, sweepUUIDWithServices):
				_, _ = w.Write([]byte(stubBodyWithServices))
			default:
				_, _ = w.Write([]byte(stubBody))
			}
		}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	writeSweepConfig(t, home, srv.URL)
	t.Setenv("HOME", home)

	// The capture must be shown to WORK before any silence is read as
	// a pass. A broken fd swap makes every verb look silent, and that
	// is indistinguishable from a clean run.
	stdout, _, err := runCapturingRealFDs(t, "pgedge version", "json")
	if err != nil {
		t.Fatalf("positive control: pgedge version failed: %v", err)
	}
	if !strings.Contains(stdout, "\"version\"") {
		t.Fatalf("positive control: the capture saw %q on stdout, so "+
			"it cannot see stdout at all and every assertion below "+
			"would pass vacuously", stdout)
	}

	// The canonical shape derives the population from the whole tree.
	// A non-canonical shape re-runs exactly that population, and must
	// NOT be applied tree-wide: answering `null` to a verb whose spec
	// declares a real body makes the generated parser decode it into a
	// ZERO struct and the verb then prints an all-empty object at exit
	// 0. Measured tree-wide: 72 stdout failures across 35 verbs, 14 of
	// them byoc's, and the census goes 18 to 53. `controlplane cluster init`
	// fails in text and table as well, not only json/yaml.
	//
	// That is the fixture describing a response the API cannot send,
	// and treating it as a finding would be the same mistake as
	// answering a read body to a state-changing GET. The justification
	// is stated at its real strength: there is no evidence that byoc
	// sends `null` where a spec declares a body. The recorded
	// measurement supports the converse -- that where a spec declares
	// no content, byoc sends `null` -- and says nothing about the
	// other direction.
	//
	// It does leave a real property unguarded: nothing here would
	// notice a CREATE verb printing a fabricated zero object if the API
	// ever answered one `null`. That is a wider rule than this one and
	// belongs to its own change.
	verbs := annotatedCommandPaths(t)
	if !shape.canonical {
		verbs = emptyBodyMustCover
	}

	empty := map[string]bool{}
	for _, path := range verbs {
		// text is where the ack sentence lives; json and yaml are
		// where a pipeline reads. A rule that held in one and not the
		// others would be the defect.
		//
		// table is here because output.ValidFormat accepts four, not
		// three: it is an undocumented alias for text, and 84
		// non-generated sites in the tree branch on the literal
		// "table" — measured. The shipped prose says "every output
		// mode", so the gate covers every mode the CLI accepts rather
		// than only the three it advertises.
		for _, format := range []string{"text", "json", "yaml", "table"} {
			out, errOut, runErr := runCapturingRealFDs(t, path, format)

			// Asserted BEFORE the exit-status filter, and
			// unconditional on it, because a review made this pass by
			// rendering the fabricated object and THEN returning an
			// error: the verb still succeeded at the write, still put
			// `{"rotated":true}` on stdout, and dropping out of the
			// classified set took it out of reach of every assertion.
			// The shape is not contrived -- four of these verbs end in
			// `return trackMutation(...)`, so "render, then a
			// follow-up call fails" is what the tree already looks
			// like. Nothing in the tree may write to stdout on a
			// failure path either, so this is the stronger rule and it
			// costs nothing: all 195 runs are byte-empty today.
			if out != "" {
				t.Errorf("%s -o %s: wrote %d byte(s) to stdout "+
					"(run error: %v): %q. Nothing real came back to "+
					"print, so stdout stays empty and the "+
					"acknowledgement is exit 0 plus a line on "+
					"stderr.", path, format, len(out), runErr, out)
			}
			if runErr != nil {
				// Not an empty-body verb, or synthesis never reached
				// the write. Either way there is no success to hold to
				// the rest of the contract. The must-cover list below
				// is what keeps that from quietly swallowing a verb.
				continue
			}
			if format == "text" {
				empty[path] = true
			}
			if strings.TrimSpace(errOut) == "" {
				t.Errorf("%s -o %s: exit 0 and nothing on either "+
					"stream. Silence on stdout is the contract; "+
					"silence everywhere leaves the operator with no "+
					"acknowledgement at all.", path, format)
			}
		}
	}

	var got []string
	for p := range empty {
		got = append(got, p)
	}
	sort.Strings(got)
	t.Logf("classified %d verbs as empty-body successes:\n  %s",
		len(got), strings.Join(got, "\n  "))

	if !shape.canonical {
		// The census by NAME applies here too, and does NOT "pin an
		// incidental consequence of how null decodes": these 18
		// operations all declare no content, so BOTH shapes are
		// legitimate answers and exit 0 is the contract for each. That
		// is what the per-module emptybody tests already assert for the
		// 204.
		//
		// Without it this pass could not notice its own coverage
		// collapsing. Measured: hardening checkEmptyBodyResponse to
		// reject any body took the classified population from 18 to 7
		// -- eleven byoc verbs failing on the response byoc actually
		// sends -- and this pass said nothing, because a count nobody
		// asserts on is not a measurement.
		for _, must := range emptyBodyMustCover {
			if !empty[must] {
				t.Errorf("%s did not succeed against the %q shape. "+
					"Its spec declares no content, so both measured "+
					"shapes are legitimate answers and exit 0 is the "+
					"contract for each.", must, shape.name)
			}
		}
		// No count check here, deliberately: for a non-canonical
		// shape the verb list IS emptyBodyMustCover, so got is a
		// subset by construction and any shortfall already trips the
		// named loop above. A count would be the dead assertion the
		// canonical arm used to carry.
		return
	}

	for _, must := range emptyBodyMustCover {
		if !empty[must] {
			t.Errorf("%s was not classified as an empty-body success. "+
				"Either it stopped taking that path, or synthesized "+
				"arguments no longer reach its write — in which case "+
				"this contract has quietly stopped covering it. A "+
				"count cannot notice that, which is why it is named.",
				must)
		}
	}
	// Equality, not a floor. A floor here could never be the only
	// thing to fail -- the population is exactly the named list, so
	// anything that shrinks it also trips the loop above. What a floor
	// could not notice is the population GROWING: a new empty-body
	// verb landing unlisted, which is the case where someone has to
	// come here and decide it belongs.
	if len(got) != len(emptyBodyMustCover) {
		t.Errorf("%d verbs classified against %d named. A new verb "+
			"whose success carries no body must be added to "+
			"emptyBodyMustCover deliberately.\nClassified:\n  %s",
			len(got), len(emptyBodyMustCover),
			strings.Join(got, "\n  "))
	}

	// The negative control. See emptyBodyCarriesABody.
	for _, verb := range emptyBodyCarriesABody {
		if empty[verb] {
			t.Errorf("%s was classified as an empty-body success, but "+
				"its success carries a body. The classifier is "+
				"treating every verb as empty-bodied, so the stdout "+
				"assertions above prove nothing.", verb)
		}
	}
}

// runCapturingRealFDs executes one verb with synthesized arguments and
// returns what reached stdout and stderr.
//
// It swaps the process file descriptors rather than handing the
// Runtime a pair of buffers, because setupRuntime points the RENDERER
// at os.Stdout directly — so a Runtime buffer would see the
// hand-written acknowledgement lines and miss exactly the rendered
// payload this gate exists to forbid.
//
// Both pipes are drained concurrently: a verb that wrote more than the
// pipe buffer would otherwise block forever inside Execute, and a hung
// gate is worse than a red one.
func runCapturingRealFDs(t *testing.T, path, format string) (
	stdout, stderr string, err error,
) {
	t.Helper()
	rt := &module.Runtime{}
	root := cli.NewRootCmd(rt)
	for _, m := range Modules() {
		c, cErr := m.Command(rt)
		if cErr != nil {
			t.Fatal(cErr)
		}
		root.AddCommand(c)
	}

	args := strings.Fields(strings.TrimPrefix(path, "pgedge "))
	leaf, _, findErr := root.Find(args)
	if findErr != nil {
		t.Fatalf("%s: %v", path, findErr)
	}
	full := make([]string, 0, len(args)+12)
	full = append(full, args...)
	full = append(full, synthesizeArgs(leaf)...)
	full = append(full, "--output", format)

	outR, outW, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	errR, errW, pipeErr := os.Pipe()
	if pipeErr != nil {
		_ = outR.Close()
		_ = outW.Close()
		t.Fatal(pipeErr)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	var outBuf, errBuf bytes.Buffer
	drained := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(&outBuf, outR); drained <- struct{}{} }()
	go func() { _, _ = io.Copy(&errBuf, errR); drained <- struct{}{} }()

	restore := func() {
		_ = outW.Close()
		_ = errW.Close()
		<-drained
		<-drained
		os.Stdout, os.Stderr = oldOut, oldErr
		_ = outR.Close()
		_ = errR.Close()
	}

	root.SetArgs(full)
	root.SetOut(outW)
	root.SetErr(errW)
	// A panic is the repo's forbidden failure mode. Recovering here
	// names the verb instead of taking the package down with the file
	// descriptors still swapped, which would silence every later test.
	func() {
		defer func() {
			if r := recover(); r != nil {
				restore()
				t.Fatalf("%s panicked: %v", path, r)
			}
		}()
		err = root.Execute()
		restore()
	}()
	return outBuf.String(), errBuf.String(), err
}
