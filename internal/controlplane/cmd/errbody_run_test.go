package cmd

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/errbody"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// The controlplane half of the error-body contract. The starfleet module got
// this first; controlplane's client was built with the same layering cp
// had before that fix and the same generated Content-Type catch-all,
// so the identical failure was live here.
//
// A Control Plane's OWN route miss is Go's default-mux
// "404 page not found" under text/plain, which reaches checkResponse
// unaided — which is why this was never seen against a bare CP. It
// becomes reachable the moment a reverse proxy or gateway sits in
// front of one and answers with a body that is not JSON under a JSON
// Content-Type.

// TestControlplaneErrBodyTransportIsAlwaysInstalled is controlplane's copy of the cloud
// module's layering guard, and it has to be a copy: internal/errbody
// now serves both modules, so a test living beside the transport
// cannot assert where any particular caller installed it.
//
// The failure it guards is the one that would make the fix invisible:
// a client built without the repair on an ordinary quiet run, which is
// every command that is not --verbose, --debug or --dry-run.
func TestControlplaneErrBodyTransportIsAlwaysInstalled(t *testing.T) {
	// Quiet on purpose: no --verbose, no --debug, no --dry-run.
	rt := &module.Runtime{Stderr: io.Discard}
	c, err := httpClientFor(rt, connConfig{})
	if err != nil {
		t.Fatalf("httpClientFor: %v", err)
	}
	if c.Transport == nil {
		t.Fatal("quiet client has no transport; the error-body repair " +
			"cannot run")
	}
	if _, ok := c.Transport.(*errbody.Transport); !ok {
		t.Fatalf("quiet client's outermost transport is %T, want "+
			"*errbody.Transport", c.Transport)
	}
}

// TestControlplaneMislabelledErrorBodyReachesCheckResponse is the end-to-end
// proof. Without the repair each case dies inside the generated
// Parse*Response with a json decode error, and checkResponse — which
// owns every message a user is meant to read here — never runs.
//
// The assertions are deliberately about what the user SEES, not about
// which transport ran: a decode error leaking through is the whole
// defect, so each case asserts the decode error is absent AND the
// classified message is present.
func TestControlplaneMislabelledErrorBodyReachesCheckResponse(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		wantMsg     string
		wantCode    int
	}{
		{
			// The core shape: a proxy in front of a Control Plane
			// answering a route miss, mislabelled as JSON.
			name:        "route miss under a JSON content type",
			status:      http.StatusNotFound,
			contentType: "application/json",
			body:        routeMissBody,
			wantMsg:     "does not serve an endpoint",
			wantCode:    ExitGeneral,
		},
		{
			name:        "proxy HTML error page under a JSON content type",
			status:      http.StatusBadGateway,
			contentType: "application/json",
			body:        "<html><body><h1>502 Bad Gateway</h1></body></html>",
			wantMsg:     "API error (502)",
			wantCode:    ExitGeneral,
		},
		{
			// Valid JSON, but a bare string rather than an object. The
			// generated parser unmarshals into a struct, so this fails
			// exactly like the non-JSON cases — which is why the
			// transport's test is "is it an object", not json.Valid.
			name:        "a bare JSON string is not an object",
			status:      http.StatusGatewayTimeout,
			contentType: "application/json",
			body:        `"upstream timed out"`,
			wantMsg:     "request timed out",
			wantCode:    ExitTimeout,
		},
		{
			// Control: a correctly-labelled text/plain body already
			// reached checkResponse before this change and must still.
			name:        "text/plain route miss still classified",
			status:      http.StatusNotFound,
			contentType: "text/plain",
			body:        routeMissBody,
			wantMsg:     "does not serve an endpoint",
			wantCode:    ExitGeneral,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			err := runControlplane(t, rt, out, url, "database", "list")
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), "invalid character") ||
				strings.Contains(err.Error(), "cannot unmarshal") ||
				strings.Contains(err.Error(), "unexpected end of JSON") {
				t.Fatalf("a JSON decode error reached the user, so "+
					"checkResponse never ran: %v", err)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("message = %q, want it to contain %q",
					err.Error(), tt.wantMsg)
			}
			var ee *ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("want *ExitError, got %T: %v", err, err)
			}
			if ee.Code() != tt.wantCode {
				t.Errorf("exit code = %d, want %d", ee.Code(), tt.wantCode)
			}
		})
	}
}

// TestControlplaneErrorBodyExcerptIsBounded pins the repair's second half.
//
// Before the repair, an oversized non-JSON body died in the parser and
// never reached a message. Now that those bodies are routed to
// checkResponse deliberately, an unbounded branch would print a proxy's
// entire error page. The starfleet module hit exactly this, which is what
// its own maxBodyExcerpt comment records.
func TestControlplaneErrorBodyExcerptIsBounded(t *testing.T) {
	huge := strings.Repeat("A", 20_000)
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, huge)
	})
	err := runControlplane(t, rt, out, url, "database", "list")
	if err == nil {
		t.Fatal("expected an error")
	}
	// The excerpt is bounded at maxBodyExcerpt; the message adds a
	// short prefix. A generous ceiling still fails loudly on an
	// unbounded body, which is what this guards.
	if len(err.Error()) > maxBodyExcerpt+200 {
		t.Errorf("error message is %d bytes for a %d-byte body; the "+
			"excerpt is not bounded", len(err.Error()), len(huge))
	}
	if strings.Contains(err.Error(), huge) {
		t.Error("the whole body reached the message")
	}
}

// TestDebugLogsTheContentTypeTheServerSent pins the LAYER ORDER
// behaviourally, which the type assertions above can only pin
// structurally.
//
// The repair wraps httplog rather than nesting inside it so that a
// response reaches the logger FIRST and the repair second. Nest them
// the other way and --debug prints the Content-Type the repair just
// wrote, hiding the exact symptom --debug is reached for: an operator
// diagnosing "why did this 404 come back as a JSON parse error" would
// see text/plain in the dump and conclude the server was fine.
//
// This asserts both halves at once — the dump shows the ORIGINAL
// header, and the command still produces the CLASSIFIED error — since
// either alone can be satisfied by the wrong arrangement.
func TestDebugLogsTheContentTypeTheServerSent(t *testing.T) {
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	rt.Debug = true
	url := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, routeMissBody)
	})

	err := runControlplane(t, rt, out, url, "database", "list")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "does not serve an endpoint") {
		t.Errorf("error = %q, want the classified route-miss message",
			err.Error())
	}
	dump := errOut.String()
	if !strings.Contains(dump, "application/json") {
		t.Errorf("--debug did not report the Content-Type the server "+
			"actually sent; the repair is logging over it. Dump:\n%s",
			dump)
	}
}
