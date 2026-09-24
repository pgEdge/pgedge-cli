package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const testAccountID = "33333333-4444-5555-6666-777788889999"

const accountBody = `{"id":"` + testAccountID + `","name":"aws-prod",` +
	`"type":"aws","properties":{},"created_at":"2024-03-15T10:30:00Z",` +
	`"updated_at":"2024-03-15T10:30:00Z"}`

func TestCloudAccountListRun(t *testing.T) {
	body := `[` + accountBody + `]`

	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "list"); err != nil {
			t.Fatalf("account list: %v", err)
		}
		if !strings.Contains(out.String(), "aws-prod") {
			t.Errorf("missing account name: %q", out.String())
		}
	})

	t.Run("empty json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "list"); err != nil {
			t.Fatalf("account list json: %v", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "list"); err == nil {
			t.Fatal("expected error on 500")
		}
	})

	// A managed-plan tenant's list verbs answer 400 "plan does not
	// allow ..." rather than a genuine bad request — issue #105. The
	// CLI must render this cleanly as an entitlement problem, at
	// ExitAuth, rather than the raw "API error (400): ..." passthrough.
	t.Run("plan denial renders cleanly", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		const body = `{"code":400,"message":` +
			`"plan does not allow creating cloud account read"}`
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(400, body))
		err := runAuthed(t, rt, out, url, "cloud-account", "list")
		if err == nil {
			t.Fatal("expected error on plan-denial 400")
		}
		var ee *conn.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("err = %v, want *conn.ExitError", err)
		}
		if ee.Code() != conn.ExitAuth {
			t.Errorf("code = %d, want ExitAuth (%d)", ee.Code(), conn.ExitAuth)
		}
		if strings.HasPrefix(err.Error(), "API error (400):") {
			t.Errorf("err = %v, still the raw passthrough shape", err)
		}
		if !strings.Contains(err.Error(),
			"this tenant's plan does not allow this resource") {
			t.Errorf("err = %v, want the clean plan-denial explanation",
				err)
		}
		if !strings.Contains(err.Error(), "enterprise/BYOC-plan") {
			t.Errorf("err = %v, want the fix named", err)
		}
	})

	// A non-plan 400 must keep its raw rendering — the plan-denial
	// match is a substring on "plan does not allow", not every 400.
	t.Run("non-plan 400 stays raw", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		const body = `{"code":400,"message":"malformed request"}`
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(400, body))
		err := runAuthed(t, rt, out, url, "cloud-account", "list")
		if err == nil {
			t.Fatal("expected error on 400")
		}
		var ee *conn.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("err = %v, want *conn.ExitError", err)
		}
		if ee.Code() != conn.ExitGeneral {
			t.Errorf("code = %d, want ExitGeneral (%d)",
				ee.Code(), conn.ExitGeneral)
		}
		if !strings.Contains(err.Error(), body) {
			t.Errorf("err = %v, want the raw body passed through", err)
		}
	})
}

func TestCloudAccountGetRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, accountBody))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "get", testAccountID); err != nil {
			t.Fatalf("account get: %v", err)
		}
		if !strings.Contains(out.String(), "aws-prod") {
			t.Errorf("missing name: %q", out.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(404, `nope`))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "get", testAccountID); err == nil {
			t.Fatal("expected error on 404")
		}
	})
}

func TestCloudAccountCreateRun(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{
			name: "aws",
			args: []string{"--type", "aws", "--role-arn", "arn:aws:iam::1:role/x"},
		},
		{
			name: "gcp",
			args: []string{"--type", "gcp", "--project-id", "proj",
				"--service-account", "svc@proj.iam"},
		},
		{
			name: "azure",
			args: []string{"--type", "azure", "--tenant-id", "t",
				"--subscription-id", "s", "--azure-client-id", "c",
				"--azure-client-secret", "sec", "--resource-group", "rg"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+" success", func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, accountBody))
			args := append([]string{"cloud-account", "create",
				"--name", "acct"}, tc.args...)
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("account create %s: %v", tc.name, err)
			}
			if !strings.Contains(errb.String(), "created") {
				t.Errorf("missing created message: %q", errb.String())
			}
		})
	}

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, accountBody))
		if err := runAuthed(t, rt, out, url, "cloud-account", "create",
			"--type", "aws", "--role-arn", "arn:x"); err != nil {
			t.Fatalf("account create json: %v", err)
		}
	})

	t.Run("missing aws role", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, accountBody))
		if err := runAuthed(t, rt, out, url, "cloud-account", "create",
			"--type", "aws"); err == nil {
			t.Fatal("expected error for missing role-arn")
		}
	})

	t.Run("missing gcp fields", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, accountBody))
		if err := runAuthed(t, rt, out, url, "cloud-account", "create",
			"--type", "gcp"); err == nil {
			t.Fatal("expected error for missing gcp fields")
		}
	})

	t.Run("missing azure fields", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, accountBody))
		if err := runAuthed(t, rt, out, url, "cloud-account", "create",
			"--type", "azure"); err == nil {
			t.Fatal("expected error for missing azure fields")
		}
	})

	t.Run("unknown type", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, accountBody))
		if err := runAuthed(t, rt, out, url, "cloud-account", "create",
			"--type", "oracle"); err == nil {
			t.Fatal("expected error for unknown type")
		}
	})
}

func TestCloudAccountDeleteRun(t *testing.T) {
	t.Run("force success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "delete", testAccountID, "--force"); err != nil {
			t.Fatalf("account delete: %v", err)
		}
		if !strings.Contains(errb.String(), "deleted") {
			t.Errorf("missing deleted message: %q", errb.String())
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "delete", "bad", "--force"); err == nil {
			t.Fatal("expected error on invalid id")
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "delete", testAccountID); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})
}

// cfTemplateBody is the shape devapi returns (captured 2026-08-30 with
// the tenant, external ID and suffix replaced): one object, where the
// spec declares an array of them (#218).
const cfTemplateBody = `{"url":"https://us-east-1.console.aws.amazon.com/` +
	`cloudformation/home?region=us-east-1#/stacks/create/review` +
	`?templateURL=https://pgedge-public-assets.s3.amazonaws.com/product/` +
	`templates/cloudformation.template\u0026stackName=pgedge-permissions-abc123` +
	`\u0026param_TrustedAccountID=000000000000` +
	`\u0026param_TenantID=11111111-2222-3333-4444-555555555555` +
	`\u0026param_ExternalID=placeholder-external-id` +
	`\u0026param_RandomSuffix=abc123"}`

const cfTemplateURLFragment = "param_ExternalID=placeholder-external-id"

func init() {
	if !strings.Contains(cfTemplateBody, cfTemplateURLFragment) {
		panic("cfTemplateURLFragment is not a substring of cfTemplateBody")
	}
}

func TestCloudAccountCFTemplateRun(t *testing.T) {
	t.Run("object text", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, cfTemplateBody))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "cloudformation-template"); err != nil {
			t.Fatalf("cf template: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, cfTemplateURLFragment) {
			t.Errorf("missing url: %q", got)
		}
		// The escaped ampersands must arrive as one URL a shell can
		// pass to a browser, on exactly one line.
		if !strings.Contains(got, "&param_TenantID=") ||
			strings.Count(strings.TrimSpace(got), "\n") != 0 {
			t.Errorf("want one decoded url line, got %q", got)
		}
	})

	t.Run("object json keeps the object", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, cfTemplateBody))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "cloudformation-template"); err != nil {
			t.Fatalf("cf template json: %v", err)
		}
		got := strings.TrimSpace(out.String())
		if !strings.HasPrefix(got, "{") {
			t.Errorf("want the object the API sent, got %q", got)
		}
		if !strings.Contains(got, `"url"`) ||
			!strings.Contains(got, cfTemplateURLFragment) {
			t.Errorf("missing url field: %q", got)
		}
	})

	t.Run("array text", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[{"url":"https://cf.example.com/t"}]`))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "cloudformation-template"); err != nil {
			t.Fatalf("cf template: %v", err)
		}
		if !strings.Contains(out.String(), "https://cf.example.com/t") {
			t.Errorf("missing url: %q", out.String())
		}
	})

	t.Run("array json keeps the array", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[{"url":"https://cf.example.com/t"}]`))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "cloudformation-template"); err != nil {
			t.Fatalf("cf template json: %v", err)
		}
		if !strings.HasPrefix(strings.TrimSpace(out.String()), "[") {
			t.Errorf("want the array the API sent, got %q", out.String())
		}
	})

	for name, body := range map[string]string{
		"empty array":  `[]`,
		"empty object": `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, body))
			if err := runAuthed(t, rt, out, url,
				"cloud-account", "cloudformation-template"); err != nil {
				t.Fatalf("cf template empty: %v", err)
			}
			if out.Len() != 0 {
				t.Errorf("stdout must stay empty, got %q", out.String())
			}
			if !strings.Contains(errb.String(), "No template returned") {
				t.Errorf("want empty message, got %q", errb.String())
			}
		})
	}

	t.Run("json prints the body as it arrived", func(t *testing.T) {
		for name, body := range map[string]string{
			"empty object":   `{}`,
			"unknown fields": `{"url":"https://x/y","expires_at":"2026-09-01"}`,
		} {
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, body))
			if err := runAuthed(t, rt, out, url,
				"cloud-account", "cloudformation-template"); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			var got, want any
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("%s: stdout is not json: %q", name, out.String())
			}
			_ = json.Unmarshal([]byte(body), &want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s: want %s as sent, got %q", name, body, out.String())
			}
		}
	})

	t.Run("array entry without a url prints nothing", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[{"url":""}]`))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "cloudformation-template"); err != nil {
			t.Fatalf("cf template blank: %v", err)
		}
		if out.Len() != 0 || !strings.Contains(errb.String(), "No template") {
			t.Errorf("want empty stdout and notice, got %q / %q",
				out.String(), errb.String())
		}
	})

	// A managed-plan tenant answers this verb with a 400 "plan does not
	// allow ..." body. The untyped read must still route it through
	// checkResponse: without that the denial decodes as an object with
	// no url and the command reports success (#218 review).
	t.Run("plan denial exits 5", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(400,
			`{"code":400,"message":`+
				`"plan does not allow creating cloud account read"}`))
		err := runAuthed(t, rt, out, url,
			"cloud-account", "cloudformation-template")
		var exitErr *conn.ExitError
		if !errors.As(err, &exitErr) || exitErr.Code() != conn.ExitAuth {
			t.Fatalf("want ExitAuth, got %v", err)
		}
	})

	t.Run("malformed body is an error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `not json`))
		err := runAuthed(t, rt, out, url,
			"cloud-account", "cloudformation-template")
		if err == nil || !strings.Contains(err.Error(),
			"get cloudformation template") {
			t.Fatalf("want decode error, got %v", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(500, `{"code":500,"message":"boom"}`))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "cloudformation-template"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

// zonesBody is the shape dev returns: a single object wrapping the
// list, not a bare array.
const zonesBody = `{"availability_zones":` +
	`["us-east-2a","us-east-2b","us-east-2c"]}`

func TestCloudAccountAvailabilityZonesRun(t *testing.T) {
	args := []string{"cloud-account", "availability-zones",
		testAccountID, "--region", "us-east-2"}

	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, zonesBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("availability-zones: %v", err)
		}
		for _, want := range []string{
			"us-east-2a", "us-east-2b", "us-east-2c",
		} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing zone %q: %q", want, out.String())
			}
		}
	})

	// The region reaches the path, not a query parameter.
	t.Run("region reaches the request path", func(t *testing.T) {
		var got string
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(zonesBody))
			})
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("availability-zones: %v", err)
		}
		want := "/byoc/v1/cloud-accounts/" + testAccountID +
			"/regions/us-east-2/availability-zones"
		if got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, zonesBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("availability-zones json: %v", err)
		}
		if !strings.Contains(out.String(), "us-east-2a") {
			t.Errorf("json missing zone: %q", out.String())
		}
	})

	t.Run("empty", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
			200, `{"availability_zones":[]}`))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("availability-zones empty: %v", err)
		}
		if !strings.Contains(errb.String(),
			"No availability zones found") {
			t.Errorf("want empty message, got %q", errb.String())
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, zonesBody))
		if err := runAuthed(t, rt, out, url,
			"cloud-account", "availability-zones", "bad",
			"--region", "us-east-2"); err == nil {
			t.Fatal("expected error on invalid id")
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url, args...); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

// TestAvailabilityZonesCannotForgeAZone covers a print site that does
// NOT go through the renderer, and therefore does not get the
// renderer's escaping for free (#323).
//
// This list is one zone per line, so the line structure belongs to the
// CLI and a zone carrying a newline forges an extra zone. The test
// exists because reverting the Sanitize call at this site left the
// entire suite green while the statement still showed as covered — the
// line executes, so the coverage gate is satisfied by a line that
// asserts nothing.
func TestAvailabilityZonesCannotForgeAZone(t *testing.T) {
	const body = `{"availability_zones":` +
		`["us-east-2a\nus-east-2-FORGED","us-east-2b"]}`
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(200, body))
	if err := runAuthed(t, rt, out, url, "cloud-account",
		"availability-zones", testAccountID, "--region", "us-east-2"); err != nil {
		t.Fatalf("availability-zones: %v", err)
	}
	got := strings.TrimRight(out.String(), "\n")
	if n := len(strings.Split(got, "\n")); n != 2 {
		t.Errorf("2 zones printed %d lines, want 2 — a zone name is "+
			"forging a zone:\n%s", n, got)
	}
	if !strings.Contains(got, `us-east-2a\nus-east-2-FORGED`) {
		t.Errorf("the offending zone is not recoverable from the "+
			"output:\n%s", got)
	}
}

// TestCloudAccountCreateClientSideRefusalsExitTwo pins the exit code
// AND the ordering for every refusal `cloud-account create` makes on
// its own. Each case runs with no credentials and no --api-url, so a
// check placed after clientFromCmd would answer ExitAuth (5) for
// "no credentials found" rather than the usage code the mistake
// deserves — the failure mode managed's create carries a call-site
// comment about. The positive control below is what proves the runner
// would surface that 5.
func TestCloudAccountCreateClientSideRefusalsExitTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "aws without --role-arn",
			args: []string{"--type", "aws"},
			want: "--role-arn is required for --type aws",
		},
		{
			name: "azure with nothing",
			args: []string{"--type", "azure"},
			want: "--tenant-id, --subscription-id, --azure-client-id, " +
				"--azure-client-secret required for --type azure",
		},
		{
			name: "azure missing one flag",
			args: []string{"--type", "azure", "--tenant-id", "t",
				"--subscription-id", "s", "--azure-client-id", "c"},
			want: "--azure-client-secret required for --type azure",
		},
		{
			name: "gcp with nothing",
			args: []string{"--type", "gcp"},
			want: "--project-id, --service-account required for --type gcp",
		},
		{
			name: "gcp missing one flag",
			args: []string{"--type", "gcp", "--project-id", "proj"},
			want: "--service-account required for --type gcp",
		},
		{
			name: "unrecognised type",
			args: []string{"--type", "oracle"},
			want: `unknown provider type "oracle": must be aws, azure, or gcp`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, _, _ := testsupport.NewRuntime(t, "", "text")
			var out bytes.Buffer
			args := append([]string{"cloud-account", "create"}, tc.args...)
			err := runByoc(t, rt, &out, args...)
			if err == nil {
				t.Fatal("want a refusal, got nil")
			}
			var ee *conn.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("err = %v, want *conn.ExitError", err)
			}
			if ee.Code() != conn.ExitUsage {
				t.Errorf("code = %d, want ExitUsage (%d); err = %v",
					ee.Code(), conn.ExitUsage, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to contain %q",
					err.Error(), tc.want)
			}
		})
	}

	// The positive control: the same runner, the same absent
	// credentials, but nothing client-side left to refuse. It must
	// reach credential resolution and report ExitAuth, or every case
	// above would pass on a runner that never got that far.
	t.Run("control: a complete aws create reaches credentials", func(t *testing.T) {
		rt, _, _ := testsupport.NewRuntime(t, "", "text")
		var out bytes.Buffer
		err := runByoc(t, rt, &out, "cloud-account", "create",
			"--type", "aws", "--role-arn", "arn:aws:iam::1:role/x")
		if err == nil {
			t.Fatal("want a credential failure, got nil")
		}
		var ee *conn.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("err = %v, want *conn.ExitError", err)
		}
		if ee.Code() != conn.ExitAuth {
			t.Fatalf("code = %d, want ExitAuth (%d); err = %v",
				ee.Code(), conn.ExitAuth, err)
		}
	})
}
