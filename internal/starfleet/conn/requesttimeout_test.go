package conn

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// hangingServer accepts, completes the TLS handshake and then never
// writes a response header — the shape these tests guard. It answers the
// request context so the test's own goroutines unwind.
func hangingServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestStarfleetClientsAreBounded pins the defect directly: every client
// this package hands out bounds the whole exchange, so a server that
// accepts and never answers cannot hang the command.
//
// authHTTPClientFor is covered in BOTH branches. Its quiet branch used
// to return http.DefaultClient, which cannot be bounded here — it is a
// shared global, so setting Timeout on it would bound every other
// caller in the process too.
func TestStarfleetClientsAreBounded(t *testing.T) {
	tests := []struct {
		name string
		get  func(*module.Runtime, time.Duration) *http.Client
	}{
		{"HTTPClientFor", HTTPClientFor},
		{"authHTTPClientFor", authHTTPClientFor},
	}
	for _, tc := range tests {
		for _, logging := range []bool{false, true} {
			branch := "quiet"
			if logging {
				branch = "logging"
			}
			t.Run(tc.name+" "+branch, func(t *testing.T) {
				testsupport.ClearEnv(t)
				rt := &module.Runtime{
					Debug: logging, Stderr: io.Discard,
				}
				c := tc.get(rt, RequestTimeout)
				if c.Timeout != RequestTimeout {
					t.Errorf("Timeout = %v, want %v",
						c.Timeout, RequestTimeout)
				}
				if c == http.DefaultClient {
					t.Error("handed out the shared http.DefaultClient")
				}
			})
		}
	}
	// Bounding the clients must not have reached the global as a
	// side effect: anything else in the process still uses it.
	if http.DefaultClient.Timeout != 0 {
		t.Errorf("http.DefaultClient.Timeout = %v, want 0 — the "+
			"global was mutated", http.DefaultClient.Timeout)
	}
}

// TestBoundedClientOutlastsNoHungServer is the behavioural half. The
// table above would still pass if a wrapper in the transport stack
// swallowed the deadline, so this drives a real hung server through
// the whole stack — dry-run, error-body repair, httplog — and requires
// the call to come back.
//
// The bound is shortened on the returned client rather than run at
// RequestTimeout, which would make this a 30-second test.
func TestBoundedClientOutlastsNoHungServer(t *testing.T) {
	testsupport.ClearEnv(t)
	rt := &module.Runtime{Debug: true, Stderr: io.Discard}
	c := HTTPClientFor(rt, RequestTimeout)
	if c.Timeout == 0 {
		t.Fatal("client is unbounded; the rest of this proves nothing")
	}
	c.Timeout = 250 * time.Millisecond

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, hangingServer(t), http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	resp, err := c.Do(req) //nolint:bodyclose // err is non-nil
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a hung server produced a response")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("took %v: the transport stack defeated the bound",
			elapsed)
	}
}

// TestClientTimeoutIsIndistinguishableFromAWaitExpiry records the
// measurement the wait loops depend on, so that it fails here if a Go
// release changes it rather than silently in a wait nobody is running.
//
// Both a client Timeout and a context deadline produce a *url.Error
// that satisfies errors.Is(err, context.DeadlineExceeded) AND reports
// Timeout() true. Neither predicate can tell a single slow request
// from a whole wait expiring — which is why waitForSubjectTask in
// both modules reads ctx.Err() before cancelling instead.
func TestClientTimeoutIsIndistinguishableFromAWaitExpiry(t *testing.T) {
	testsupport.ClearEnv(t)
	target := hangingServer(t)

	byClient := func() error {
		c := &http.Client{Timeout: 200 * time.Millisecond}
		resp, err := c.Get(target) //nolint:bodyclose,noctx // err is non-nil
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}
	byContext := func() error {
		ctx, cancel := context.WithTimeout(
			context.Background(), 200*time.Millisecond)
		defer cancel()
		req, err := http.NewRequestWithContext(
			ctx, http.MethodGet, target, http.NoBody)
		if err != nil {
			return err
		}
		resp, err := (&http.Client{}).Do(req) //nolint:bodyclose // err is non-nil
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"client Timeout", byClient},
		{"context deadline", byContext},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("want a timeout error")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("errors.Is(err, DeadlineExceeded) = false "+
					"for %v", err)
			}
			var ue *url.Error
			if !errors.As(err, &ue) || !ue.Timeout() {
				t.Errorf("want a *url.Error reporting Timeout(), "+
					"got %v", err)
			}
		})
	}
}

// starfleetHTTPClientSites is how many places under internal/starfleet build
// or reach for an http.Client. It is EXACT, not a floor, because the
// failure this gate exists to catch also works by SUBTRACTION: a site
// rewritten into a shape the walk cannot see leaves the population
// silently, and a "found more than zero" guard reads that as a pass.
// Raise or lower it deliberately when a client is added or removed.
const starfleetHTTPClientSites = 5

// TestEveryStarfleetHTTPClientSetsATimeout derives its population by
// walking internal/starfleet rather than naming the three known
// clients, so a client added later is covered the day it lands. The
// point-assertions above cannot do that: they can only check what
// somebody remembered to list.
//
// Ways a client can dodge a textual version of this gate, each found
// by review and each covered here: a non-literal construction
// (`var c http.Client`, `new(http.Client)`), an aliased import
// (`nethttp "net/http"`), a Timeout PRESENT but zero in any of its
// written forms, and a Timeout zeroed after construction. A zero that
// arrives through a named constant is the stated limit — see
// setsANonZeroTimeout.
//
// Generated api packages are skipped. They do contain one
// `&http.Client{}` — the fallback used when no WithHTTPClient option
// is passed — but it is unreachable while every construction site
// passes one, and that is gated separately by the dry-run and
// verbose-transport tests.
func TestEveryStarfleetHTTPClientSetsATimeout(t *testing.T) {
	root := filepath.Join("..", "..", "..", "internal", "starfleet")
	fset := gotoken.NewFileSet()

	sites := 0
	err := filepath.WalkDir(root,
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "api" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path) //nolint:gosec // a walked repo path
			if err != nil {
				return err
			}
			f, err := parser.ParseFile(fset, path, src, 0)
			if err != nil {
				return err
			}
			// The local name of net/http in THIS file. Matching the
			// identifier "http" instead would miss an aliased import
			// entirely, and report zero uses while the code has one.
			pkg, imported := httpPackageName(f)
			if !imported {
				return nil
			}
			isClient := func(e ast.Expr) bool {
				return isPkgType(e, pkg, "Client")
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CompositeLit:
					if !isClient(node.Type) {
						return true
					}
					sites++
					if !setsANonZeroTimeout(node) {
						t.Errorf("%s: this http.Client literal sets no "+
							"non-zero Timeout, so a server that "+
							"accepts and never answers hangs the "+
							"command",
							fset.Position(node.Pos()))
					}
				case *ast.ValueSpec:
					// `var c http.Client` — zero-valued, so Timeout is
					// 0 and the walk above never sees a literal.
					if !isClient(node.Type) {
						return true
					}
					sites++
					t.Errorf("%s: http.Client declared without a "+
						"Timeout; build it as a literal that sets one",
						fset.Position(node.Pos()))
				case *ast.CallExpr:
					// `new(http.Client)` — same zero value.
					id, ok := node.Fun.(*ast.Ident)
					if !ok || id.Name != "new" ||
						len(node.Args) != 1 ||
						!isClient(node.Args[0]) {
						return true
					}
					sites++
					t.Errorf("%s: new(http.Client) has a zero Timeout; "+
						"build it as a literal that sets one",
						fset.Position(node.Pos()))
				case *ast.AssignStmt:
					if zeroesTimeoutAfterConstruction(node) {
						t.Errorf("%s: Timeout is set to zero after "+
							"construction, which unbounds a client the "+
							"literal above appears to bound",
							fset.Position(node.Pos()))
					}
					return true
				case *ast.SelectorExpr:
					if node.Sel.Name != "DefaultClient" {
						return true
					}
					if id, ok := node.X.(*ast.Ident); ok &&
						id.Name == pkg {
						sites++
						t.Errorf("%s: http.DefaultClient is unbounded "+
							"and shared, so it cannot be given a "+
							"Timeout without bounding every other "+
							"caller in the process",
							fset.Position(node.Pos()))
					}
				}
				return true
			})
			return nil
		})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	// A gate that silently matched nothing looks exactly like a
	// passing one -- and so does one that matched four of five.
	if sites != starfleetHTTPClientSites {
		t.Errorf("found %d http.Client sites under %s, expected %d. "+
			"If a client was added or removed, update "+
			"starfleetHTTPClientSites; if not, one has been rewritten "+
			"into a shape this walk cannot see", sites, root,
			starfleetHTTPClientSites)
	}
}

// httpPackageName returns the name net/http is bound to in f, and
// whether f imports it at all.
func httpPackageName(f *ast.File) (string, bool) {
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "net/http" {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, true
		}
		return "http", true
	}
	return "", false
}

func isPkgType(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// setsANonZeroTimeout reports a Timeout field set to something that is
// not syntactically zero. Presence alone is not enough: a zero Timeout
// is exactly as unbounded as no field at all, while reading as bounded.
//
// Round one of review caught the bare literal; round two got past that
// with `time.Duration(0)` and `0 * time.Second`, both of which left
// `cloud doctor`'s probe unbounded with the whole suite green. So this
// folds the shapes a duration is written in rather than matching one.
//
// LIMIT, stated rather than claimed closed: a zero reached through a
// named constant or a variable is not detected, because that needs
// type information this walk does not build. `sites` and the
// post-construction assignment check below are what stand behind it.
func setsANonZeroTimeout(lit *ast.CompositeLit) bool {
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		id, ok := kv.Key.(*ast.Ident)
		if !ok || id.Name != "Timeout" {
			continue
		}
		return !isSyntacticZero(kv.Value)
	}
	return false
}

// isSyntacticZero reports an expression that is plainly the constant
// zero: the literal, any conversion of it (`time.Duration(0)`), a
// parenthesised one, and any product with a zero factor
// (`0 * time.Second`).
func isSyntacticZero(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		return v.Kind == gotoken.INT && v.Value == "0"
	case *ast.ParenExpr:
		return isSyntacticZero(v.X)
	case *ast.BinaryExpr:
		if v.Op != gotoken.MUL {
			return false
		}
		return isSyntacticZero(v.X) || isSyntacticZero(v.Y)
	case *ast.CallExpr:
		// A conversion: T(0). One argument, and the callee is a type
		// name rather than a function we could evaluate.
		if len(v.Args) != 1 {
			return false
		}
		return isSyntacticZero(v.Args[0])
	}
	return false
}

// zeroesTimeoutAfterConstruction reports `c.Timeout = 0`, which undoes
// a correctly bounded literal a line earlier and is invisible to the
// composite-literal walk.
func zeroesTimeoutAfterConstruction(n ast.Node) bool {
	as, ok := n.(*ast.AssignStmt)
	if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
		return false
	}
	sel, ok := as.Lhs[0].(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Timeout" {
		return false
	}
	return isSyntacticZero(as.Rhs[0])
}

// TestLoginTimeoutReachesTheExchange proves the --timeout VALUE
// bounds the token exchange, not just the resource clients: review
// mutated authHTTPClientFor to ignore its parameter and every test
// in the repo stayed green. A hung token endpoint under a 100ms
// timeout must fail fast; the parameter-ignoring mutation turns this
// into a 30-second test, which the elapsed assertion refuses.
func TestLoginTimeoutReachesTheExchange(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())
	rt := &module.Runtime{Stderr: io.Discard}

	// Not hangingServer: the exchange is a POST, and a handler that
	// parks without draining the body keeps the server from ever
	// observing the client's disconnect, so srv.Close in cleanup
	// waits on the handler forever. Drain first, then park.
	srv := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
		}))
	t.Cleanup(srv.Close)

	start := time.Now()
	_, err := Login(context.Background(), rt, srv.URL, "id", "secret",
		100*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a hung token endpoint produced a token")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("took %v: the timeout did not reach the exchange",
			elapsed)
	}
}

// TestMintFloorsAnUnboundedTimeout holds the rule that the token
// exchange is never unbounded: review measured a cold cache under
// --timeout 0 hanging at the mint before task wait's own bounds
// could exist, defeating the --wait-timeout the user also passed.
// mint floors 0 to RequestTimeout, so this returns at ~30s; the
// select guard turns a removed floor into a failure instead of a
// hung test run. Slow by construction: the floor IS the 30-second
// default.
func TestMintFloorsAnUnboundedTimeout(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())
	rt := &module.Runtime{Stderr: io.Discard}

	// Drain the body (an unread POST body blinds the server to the
	// client's disconnect), and free the handler at 90s so a
	// regression fails at this test's guard instead of deadlocking
	// srv.Close until the suite's timeout.
	srv := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			select {
			case <-r.Context().Done():
			case <-time.After(90 * time.Second):
			}
		}))
	t.Cleanup(srv.Close)

	done := make(chan error, 1)
	go func() {
		_, err := Login(context.Background(), rt, srv.URL, "id", "secret", 0)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a hung token endpoint produced a token")
		}
	case <-time.After(45 * time.Second):
		t.Fatal("the mint is unbounded: --timeout 0 reached the " +
			"token exchange unfloored")
	}
}
