package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestClientIPPrintsBareAddressAndCaveat(t *testing.T) {
	url := testsupport.NewAuthedServer(t, stubAllowlist(
		&captureRequest{}, databaseJSON(testDatabaseID, ""),
		"203.0.113.9", http.StatusOK, ""))
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "client-ip"); err != nil {
		t.Fatalf("client-ip: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "203.0.113.9" {
		t.Errorf("stdout = %q, want the bare address", got)
	}
	if !strings.Contains(errOut.String(), "not necessarily") {
		t.Errorf("caveat missing from stderr: %q", errOut.String())
	}
}

func TestClientIPJSONPassesObjectThrough(t *testing.T) {
	url := testsupport.NewAuthedServer(t, stubAllowlist(
		&captureRequest{}, databaseJSON(testDatabaseID, ""),
		"203.0.113.9", http.StatusOK, ""))
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	if err := runAuthed(t, rt, out, url, "client-ip"); err != nil {
		t.Fatalf("client-ip -o json: %v", err)
	}
	if !strings.Contains(out.String(), `"ip_address": "203.0.113.9"`) &&
		!strings.Contains(out.String(), `"ip_address":"203.0.113.9"`) {
		t.Errorf("json output = %q", out.String())
	}
}

func TestClientIPRefusesEmptyAndIPv6(t *testing.T) {
	for _, ip := range []string{"", "2001:db8::1"} {
		url := testsupport.NewAuthedServer(t, stubAllowlist(
			&captureRequest{}, databaseJSON(testDatabaseID, ""),
			ip, http.StatusOK, ""))
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		err := runAuthedSilent(t, rt, out, url, "client-ip")
		var ee *ExitError
		if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
			t.Errorf("ip %q: want exit 1, got %v", ip, err)
		}
		if out.Len() != 0 {
			t.Errorf("ip %q: stdout not empty: %q", ip, out.String())
		}
	}
}
