package cmd

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const otherClusterID = "9f1c2d3e-4b5a-6789-abcd-ef0123456789"

// ingressDBHandler serves a database on testClusterID, that cluster
// with the given regions, and records the one write.
func ingressDBHandler(
	regions, dbBody, writeBody string, mu *sync.Mutex, wrote *string,
) http.HandlerFunc {
	cluster := `{"id":"` + testClusterID + `","name":"prod",` +
		`"status":"available","regions":` + regions + `,` +
		`"created_at":"2024-03-15T10:30:00Z"}`
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method != http.MethodGet:
			data, _ := io.ReadAll(r.Body)
			mu.Lock()
			*wrote = string(data)
			mu.Unlock()
			testsupport.JSONHandler(200, writeBody)(w, r)
		case strings.Contains(r.URL.Path, "/clusters/"):
			testsupport.JSONHandler(200, cluster)(w, r)
		default:
			testsupport.JSONHandler(200, dbBody)(w, r)
		}
	}
}

// TestIngressCreateFromDatabase: --database-id fills the cluster and
// region, and an explicit flag that disagrees with the database is
// exit 2 with nothing created.
func TestIngressCreateFromDatabase(t *testing.T) {
	base := []string{"ingress", "create", "--name", "web",
		"--database-id", testDatabaseID}
	cases := []struct {
		name     string
		regions  string
		extra    []string
		wantCode int
		wantBody string
	}{
		{"fills cluster and region", `["us-east-1"]`, nil, 0,
			`"cluster_id":"` + testClusterID + `","name":"web",` +
				`"region":"us-east-1"`},
		{"explicit region picks one of several",
			`["us-east-1","eu-west-1"]`,
			[]string{"--region", "eu-west-1"}, 0, `"region":"eu-west-1"`},
		{"several regions need --region", `["us-east-1","eu-west-1"]`,
			nil, ExitUsage, ""},
		{"region outside the cluster", `["us-east-1"]`,
			[]string{"--region", "eu-west-1"}, ExitUsage, ""},
		{"another cluster", `["us-east-1"]`,
			[]string{"--cluster-id", otherClusterID}, ExitUsage, ""},
		{"matching cluster", `["us-east-1"]`,
			[]string{"--cluster-id", testClusterID}, 0,
			`"cluster_id":"` + testClusterID + `"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			wrote := ""
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, ingressDBHandler(
				tc.regions, dbNoServiceBody, ingressBody, &mu, &wrote))
			args := append(append([]string{}, base...), tc.extra...)
			err := runAuthed(t, rt, out, url, args...)
			if tc.wantCode != 0 {
				var ee *ExitError
				if !errors.As(err, &ee) || ee.Code() != tc.wantCode {
					t.Fatalf("want exit %d, got %v", tc.wantCode, err)
				}
				if wrote != "" {
					t.Fatalf("refused, yet wrote %s", wrote)
				}
				return
			}
			if err != nil {
				t.Fatalf("want exit 0, got %v", err)
			}
			if !strings.Contains(wrote, tc.wantBody) {
				t.Errorf("create body %s lacks %s", wrote, tc.wantBody)
			}
		})
	}

	t.Run("cluster and region required without database", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		err := runAuthed(t, rt, out, "http://127.0.0.1:1", "ingress",
			"create", "--name", "web", "--region", "us-east-1")
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code() != ExitUsage {
			t.Fatalf("want exit %d, got %v", ExitUsage, err)
		}
	})
}

// TestIngressRegisterByServiceType: --service resolves the service ID
// from the database, and a --service-id that disagrees is exit 2.
func TestIngressRegisterByServiceType(t *testing.T) {
	reg := `{"database_id":"` + testDatabaseID + `","service_id":"svc-1",` +
		`"url":"https://mcp.example.com"}`
	cases := []struct {
		name     string
		dbBody   string
		extra    []string
		wantCode int
	}{
		{"resolves the id", dbWithServiceBody,
			[]string{"--service", "mcp"}, 0},
		{"agreeing id", dbWithServiceBody,
			[]string{"--service", "mcp", "--service-id", "svc-1"}, 0},
		{"disagreeing id", dbWithServiceBody,
			[]string{"--service", "mcp", "--service-id", "svc-9"},
			ExitUsage},
		{"type not deployed", dbNoServiceBody,
			[]string{"--service", "mcp"}, ExitGeneral},
		{"unknown type", dbWithServiceBody,
			[]string{"--service", "mpc"}, ExitUsage},
		{"neither flag", dbWithServiceBody, nil, ExitUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			wrote := ""
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, ingressDBHandler(
				`["us-east-1"]`, tc.dbBody, reg, &mu, &wrote))
			args := append([]string{"ingress", "service", "register",
				testIngressID, "--database-id", testDatabaseID},
				tc.extra...)
			err := runAuthed(t, rt, out, url, args...)
			if tc.wantCode != 0 {
				var ee *ExitError
				if !errors.As(err, &ee) || ee.Code() != tc.wantCode {
					t.Fatalf("want exit %d, got %v", tc.wantCode, err)
				}
				if wrote != "" {
					t.Fatalf("refused, yet wrote %s", wrote)
				}
				return
			}
			if err != nil {
				t.Fatalf("want exit 0, got %v", err)
			}
			if !strings.Contains(wrote, `"service_id":"svc-1"`) {
				t.Errorf("register body %s names the wrong service", wrote)
			}
		})
	}
}
