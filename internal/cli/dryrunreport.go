package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/httplog"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// dryRunReport is the -o json/-o yaml shape. internal/output renders yaml
// through the JSON tags, so a yaml key always equals its json key.
type dryRunReport struct {
	DryRun bool `json:"dry_run"`
	// Always false: neither API offers a validate endpoint. Present so
	// a parser gets the same fact the text reader does.
	ServerValidated bool           `json:"server_validated"`
	ChecksPassed    []string       `json:"checks_passed"`
	Request         *dryRunRequest `json:"request"`
}

// dryRunRequest is the write that would have been sent. Body and
// BodyText are mutually exclusive: Body is raw JSON a consumer can walk
// with jq, and BodyText replaces it when the masked payload is not JSON,
// such as the prose httplog.RedactBody returns when it fails closed.
type dryRunRequest struct {
	Method   string              `json:"method"`
	URL      string              `json:"url"`
	Headers  map[string][]string `json:"headers,omitempty"`
	Body     json.RawMessage     `json:"body,omitempty"`
	BodyText string              `json:"body_text,omitempty"`
}

// RenderDryRun writes the dry-run report for rt. The report is the
// command's result, not a diagnostic, so it goes to the renderer's
// output stream, where the real run's result would have gone.
func RenderDryRun(rt *module.Runtime) error {
	run := rt.DryRun
	if run == nil {
		// A programming error at the call site, reported as one: an
		// empty report would look like a finding.
		return fmt.Errorf("RenderDryRun: no dry run in progress")
	}

	format := "text"
	if rt.Output != nil && rt.Output.Format != "" {
		format = rt.Output.Format
	}
	if output.IsText(format) {
		return writeDryRunText(dryRunOut(rt), run, rt.Debug)
	}

	report := dryRunReport{
		DryRun: true,
		// Never nil, so "no checks applied" is stated as [].
		ChecksPassed: append([]string{}, run.Checks()...),
		Request:      jsonRequest(run.Request(), rt.Debug),
	}
	return rt.Output.Print(report, nil)
}

// dryRunOut returns Output.Out, where every command's result goes, or
// rt.Stdout for a Runtime with no renderer, which only tests build.
func dryRunOut(rt *module.Runtime) io.Writer {
	if rt.Output != nil && rt.Output.Out != nil {
		return rt.Output.Out
	}
	return rt.Stdout
}

// RenderDryRunPartial reports a dry run that stopped before any write
// was intercepted, because a check failed or the command sent nothing.
//
// It writes text to stderr whatever -o asks for. A failed check exits
// non-zero, so a partial report on stdout would look like a result, and
// half a JSON envelope would look whole to a parser. An intercepted write
// is the result, which RenderDryRun sends to stdout in the chosen format.
//
// It prints nothing when no check ran, since "checks passed: none" adds
// nothing to the error being read. Write errors are swallowed so that a
// failed stderr write cannot change the command's exit code.
func RenderDryRunPartial(rt *module.Runtime) {
	if rt.DryRun == nil || rt.DryRun.Intercepted() {
		return
	}
	checks := rt.DryRun.Checks()
	if len(checks) == 0 {
		return
	}
	var w io.Writer = os.Stderr
	if rt.Stderr != nil {
		w = rt.Stderr
	}
	// The checks block only: the full renderer's "nothing would be sent"
	// is false for a run a failed check stopped, which would have sent
	// something. The command's own error follows on the same stream.
	var b strings.Builder
	writeChecks(&b, checks)
	_, _ = io.WriteString(w, b.String())
}

// jsonRequest converts a recorded request for the machine formats.
func jsonRequest(req *dryrun.Request, debug bool) *dryRunRequest {
	if req == nil {
		return nil
	}
	out := &dryRunRequest{Method: req.Method, URL: req.URL}
	if debug {
		out.Headers = maskedHeaderMap(req)
	}
	switch {
	case len(req.Body) == 0:
		// A bodyless write: neither key appears, unlike an empty body.
	case req.BodyIsJSON:
		out.Body = json.RawMessage(req.Body)
	default:
		out.BodyText = string(req.Body)
	}
	return out
}

// maskedHeaderMap re-splits httplog's masked header lines into a map, so
// the machine formats carry the text form's masking. It reads
// HeaderLines, not req.Header, because internal/httplog owns which
// header values may be printed.
func maskedHeaderMap(req *dryrun.Request) map[string][]string {
	lines := httplog.HeaderLines(req.Header)
	if len(lines) == 0 {
		return nil
	}
	out := make(map[string][]string, len(lines))
	for _, line := range lines {
		name, value, found := strings.Cut(line, ": ")
		if !found {
			continue
		}
		out[name] = append(out[name], value)
	}
	return out
}

// writeDryRunText renders the human form.
func writeDryRunText(w io.Writer, run *dryrun.Run, debug bool) error {
	var b strings.Builder
	writeChecks(&b, run.Checks())

	req := run.Request()
	if req == nil {
		b.WriteString("nothing would be sent; the server was not " +
			"consulted.\n")
		_, err := io.WriteString(w, b.String())
		return err
	}

	b.WriteString("would send:\n")
	fmt.Fprintf(&b, "  %s %s\n", req.Method, req.URL)
	if debug {
		for _, line := range httplog.HeaderLines(req.Header) {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	if len(req.Body) > 0 {
		b.WriteString(indentBody(req))
		b.WriteString("\n")
	}
	b.WriteString("nothing was written; the server was not consulted " +
		"and may still reject this request.\n")

	_, err := io.WriteString(w, b.String())
	return err
}

// writeChecks renders the ledger. An empty one is stated, since a missing
// block reads as a print failure, and a command with no client-side
// checks is worth knowing about before trusting a clean dry run.
func writeChecks(b *strings.Builder, checks []string) {
	if len(checks) == 0 {
		b.WriteString("checks passed: none — this command has no " +
			"client-side checks\n")
		return
	}
	b.WriteString("checks passed:\n")
	for _, c := range checks {
		fmt.Fprintf(b, "  ✓ %s\n", c)
	}
}

// indentBody renders the masked body indented under "would send:".
//
// json.Indent, not decode-and-re-encode: it only adds whitespace, so an
// explicit "plan_expires_at": null, which httplog.RedactBody preserves
// and a typed parser collapses, stays distinct from an omitted key, and
// the keys keep their order. A non-JSON body, such as RedactBody's
// fail-closed prose, is indented line by line and otherwise untouched.
func indentBody(req *dryrun.Request) string {
	if req.BodyIsJSON {
		var buf bytes.Buffer
		if err := json.Indent(&buf, req.Body, "  ", "  "); err == nil {
			return "  " + buf.String()
		}
		// Unexpected for bytes json.Valid accepted; print it flat.
	}
	lines := strings.Split(strings.TrimRight(string(req.Body), "\n"), "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}
