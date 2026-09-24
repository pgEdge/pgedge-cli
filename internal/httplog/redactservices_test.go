package httplog

import (
	"encoding/json"
	"strings"
	"testing"
)

// The bodies below are the shapes the CLI actually puts on the wire for
// a service write, trimmed to the fields that matter. Each carries a
// sentinel; the test fails if any sentinel reaches the diagnostic
// stream.
//
// These are regression tests for a real leak: before secretFieldNames
// covered the service configs, `--debug` on `byoc database mcp update`
// or `managed database rag update` printed the bearer token and the LLM
// API keys verbatim.

const leakSentinel = "SENTINEL-MUST-NOT-APPEAR"

func TestServiceBodiesAreRedacted(t *testing.T) {
	cases := map[string]string{
		"mcp init_tokens": `{"services":[{"service_type":"mcp",` +
			`"mcp_config":{"init_tokens":"` + leakSentinel + `"}}]}`,
		"mcp init_users": `{"services":[{"service_type":"mcp",` +
			`"mcp_config":{"init_users":"` + leakSentinel + `"}}]}`,
		"mcp embedding_api_key": `{"services":[{"service_type":"mcp",` +
			`"mcp_config":{"embedding_api_key":"` + leakSentinel + `"}}]}`,
		"rag service-level api_key": `{"services":[{"service_type":"rag",` +
			`"rag_config":{"completion_llm":{"api_key":"` +
			leakSentinel + `"}}}]}`,
		"rag per-pipeline api_key": `{"services":[{"service_type":"rag",` +
			`"rag_config":{"pipelines":[{"name":"docs",` +
			`"embedding_llm":{"api_key":"` + leakSentinel + `"}}]}}]}`,
		"postgrest jwt_secret": `{"services":[{"service_type":"postgrest",` +
			`"postgrest_config":{"jwt_secret":"` + leakSentinel + `"}}]}`,
		"cloud account credentials": `{"name":"acct",` +
			`"credentials":{"role_arn":"` + leakSentinel + `"}}`,
		"cp s3 key secret": `{"s3_key":"AKIA","s3_key_secret":"` +
			leakSentinel + `"}`,
		"cp azure key": `{"azure_key":"` + leakSentinel + `"}`,
		"cp gcs key":   `{"gcs_key":"` + leakSentinel + `"}`,
		"invite token": `{"team_name":"t","token":"` + leakSentinel + `"}`,
		"nested database user password": `{"database_users":[{"username":"u",` +
			`"password":"` + leakSentinel + `"}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			got := RedactBody([]byte(body), 4096)
			if strings.Contains(got, leakSentinel) {
				t.Fatalf("secret reached the diagnostic stream:\n%s", got)
			}
			if !strings.Contains(got, mask) {
				t.Errorf("no mask in the output, so nothing was "+
					"redacted: %s", got)
			}
		})
	}
}

// TestNonSecretBodiesStayVerbatim is the negative control. Masking a
// value the caller needed to read costs the diagnostic value --debug
// exists for, and every field here is one notSecrets declares harmless,
// so any change to them must be deliberate.
func TestNonSecretBodiesStayVerbatim(t *testing.T) {
	cases := map[string]string{
		"token_budget is an int": `{"rag_config":{"token_budget":1000}}`,
		"token_type is Bearer":   `{"token_type":"Bearer","expires_in":3600}`,
		"jwt role claim path": `{"postgrest_config":` +
			`{"jwt_role_claim_key":".role"}}`,
		"ssh public key":  `{"key_name":"laptop","public_key":"ssh-ed25519 AAAA"}`,
		"private subnets": `{"private_subnets":["subnet-1","subnet-2"]}`,
		"private domain":  `{"private_domain":"db.internal"}`,
		"auth0 id":        `{"auth0_id":"auth0|abc","name":"client"}`,
		"primary key columns": `{"primary_key":["id"],` +
			`"is_primary_key":true}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			got := RedactBody([]byte(body), 4096)
			if got != body {
				t.Errorf("body was redacted but carries no secret.\n"+
					"  in:  %s\n  out: %s\n"+
					"Over-redaction costs the diagnostic value --debug "+
					"exists for.", body, got)
			}
		})
	}
}

// TestExactKeyMatching pins that matching is by whole key. A substring
// match would blank the negative-control bodies above, and a prefix
// match would let a key like "api_key_id" through unnoticed in the
// other direction.
func TestExactKeyMatching(t *testing.T) {
	// "token" is redacted; "token_budget" and "token_type" are not.
	// That trio only works under exact matching.
	if got := RedactBody([]byte(`{"token":"x"}`), 4096); !strings.Contains(
		got, mask) {
		t.Errorf(`"token" was not redacted: %s`, got)
	}
	for _, body := range []string{
		`{"token_budget":10}`, `{"token_type":"Bearer"}`,
	} {
		if got := RedactBody([]byte(body), 4096); got != body {
			t.Errorf("%s was redacted by a non-exact match: %s", body, got)
		}
	}
}

// TestNonJSONBodiesAreUntouched pins the case that motivates dumping
// bodies at all: an HTML proxy or SSO interstitial does not parse as
// JSON and must survive verbatim.
func TestNonJSONBodiesAreUntouched(t *testing.T) {
	html := `<html><body>SSO required: password prompt</body></html>`
	if got := RedactBody([]byte(html), 4096); got != html {
		t.Errorf("HTML was redacted:\n%s", got)
	}
}

// TestSecretBearingBodyReportsItsShape pins that a masked body still
// says what arrived: the fields around the credential stay readable,
// which is the whole point of masking values rather than replacing the
// body.
func TestSecretBearingBodyReportsItsShape(t *testing.T) {
	got := RedactBody([]byte(
		`{"id":"x","connection":{"password":"p"},"status":"available"}`), 4096)
	for _, want := range []string{"connection", "id", "status"} {
		if !strings.Contains(got, want) {
			t.Errorf("redacted summary omits top-level key %q: %s",
				want, got)
		}
	}
	if strings.Contains(got, `"p"`) {
		t.Errorf("the summary leaked a value: %s", got)
	}
}

// TestNonObjectSecretBodyIsStillRedacted covers a top-level array,
// which has no object keys of its own to walk.
func TestNonObjectSecretBodyIsStillRedacted(t *testing.T) {
	body := `[{"mcp_config":{"init_tokens":"` + leakSentinel + `"}}]`
	got := RedactBody([]byte(body), 4096)
	if strings.Contains(got, leakSentinel) {
		t.Fatalf("secret leaked from a top-level array: %s", got)
	}
	if !strings.Contains(got, mask) {
		t.Errorf("no mask in the output: %s", got)
	}
}

// --- the guarantee that value-level masking rests on ---

// TestEverythingOutsideTheMaskIsByteIdentical is the core contract.
//
// Masking values is only defensible if the surrounding bytes are never
// rewritten: the moment a body is decoded and re-encoded, an explicit
// `"display_name": null` becomes indistinguishable from an omitted key,
// key order shifts, and whitespace normalises — and those are exactly
// the details --debug is turned on to inspect.
//
// The check is mechanical: splice the original secret values back into
// the masked output and require the result to equal the input byte for
// byte.
func TestEverythingOutsideTheMaskIsByteIdentical(t *testing.T) {
	cases := map[string]struct {
		body    string
		secrets []string // the exact value literals that get masked
	}{
		"compact": {
			`{"a":1,"password":"pw","b":null}`,
			[]string{`"pw"`},
		},
		"pretty printed with tabs and newlines": {
			"{\n\t\"a\": 1,\n\t\"password\": \"pw\",\n\t\"b\": null\n}",
			[]string{`"pw"`},
		},
		"spaces around the colon": {
			`{"password"   :   "pw","keep":"me"}`,
			[]string{`"pw"`},
		},
		"two secrets": {
			`{"token":"t1","mid":"keep","api_key":"k1"}`,
			[]string{`"t1"`, `"k1"`},
		},
		"secret value is a number": {
			`{"token":12345,"keep":true}`,
			[]string{`12345`},
		},
		"secret value is null": {
			`{"token":null,"keep":"me"}`,
			[]string{`null`},
		},
		"composite secret value": {
			`{"credentials":{"a":"b"},"keep":1}`,
			[]string{`{"a":"b"}`},
		},
		"duplicate explicit nulls survive": {
			`{"display_name":null,"options":null,"password":"pw"}`,
			[]string{`"pw"`},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := RedactBody([]byte(tc.body), 65536)

			// No secret literal may survive.
			for _, sec := range tc.secrets {
				if strings.Contains(got, sec) {
					t.Fatalf("secret literal %s survived: %s", sec, got)
				}
			}
			// Reverse the masking and require an exact match.
			restored := got
			for _, sec := range tc.secrets {
				restored = strings.Replace(restored, `"`+mask+`"`, sec, 1)
			}
			if restored != tc.body {
				t.Errorf("bytes outside the mask were altered.\n"+
					"  in:       %q\n  masked:   %q\n  restored: %q",
					tc.body, got, restored)
			}
		})
	}
}

// TestSecretNameAsAnArrayElementIsNotMasked pins the key-versus-value
// distinction. A JSON string is a key only in an object and only in the
// key position; the same bytes inside an array are data. Losing that
// would mask an innocent array element and, worse, suggests the walk
// does not know where it is.
func TestSecretNameAsAnArrayElementIsNotMasked(t *testing.T) {
	body := `{"options":["password","token","api_key"],"keep":1}`
	got := RedactBody([]byte(body), 4096)
	if got != body {
		t.Errorf("array elements named like secrets were masked.\n"+
			"  in:  %s\n  out: %s", body, got)
	}
}

// TestSecretKeyDeepInsideAnArrayIsMasked is the other half: nesting
// inside arrays must not hide a real key.
func TestSecretKeyDeepInsideAnArrayIsMasked(t *testing.T) {
	body := `{"a":[[{"b":[{"jwt_secret":"` + leakSentinel + `"}]}]]}`
	got := RedactBody([]byte(body), 4096)
	if strings.Contains(got, leakSentinel) {
		t.Fatalf("a deeply nested secret survived: %s", got)
	}
	if !strings.Contains(got, mask) {
		t.Errorf("no mask in the output: %s", got)
	}
}

// TestNestedSecretInsideACompositeSecretIsCoveredOnce pins that masking
// a composite value swallows anything inside it, rather than trying to
// mask into a span that has already been replaced.
func TestNestedSecretInsideACompositeSecretIsCoveredOnce(t *testing.T) {
	body := `{"credentials":{"password":"` + leakSentinel +
		`","token":"` + leakSentinel + `"},"keep":1}`
	got := RedactBody([]byte(body), 4096)
	if strings.Contains(got, leakSentinel) {
		t.Fatalf("secret survived inside a composite: %s", got)
	}
	if got != `{"credentials":"`+mask+`","keep":1}` {
		t.Errorf("unexpected shape: %s", got)
	}
}

// TestMaskedBodyIsStillValidJSON matters because a dumped body gets
// piped into jq often enough that emitting something unparseable would
// be its own bug.
func TestMaskedBodyIsStillValidJSON(t *testing.T) {
	for _, body := range []string{
		`{"password":"pw"}`,
		`{"token":123}`,
		`{"credentials":{"a":[1,2]}}`,
		`[{"api_key":"k"}]`,
	} {
		got := RedactBody([]byte(body), 4096)
		var doc any
		if err := json.Unmarshal([]byte(got), &doc); err != nil {
			t.Errorf("masked output is not valid JSON: %v\n  %s", err, got)
		}
	}
}

// TestFailClosedFallsBackToTheWholeBodySummary pins the safety net. If
// the spans cannot be located, the body must not be echoed — the old
// whole-body summary is the correct answer, not a verbatim dump.
func TestFailClosedFallsBackToTheWholeBodySummary(t *testing.T) {
	// carriesSecret walks a decoded document, so it accepts a duplicate
	// key; the span walk sees both. Whatever the two disagree about,
	// the result must never contain the value.
	got := wholeBodySummary(map[string]any{
		"password": "pw", "id": "x",
	})
	if strings.Contains(got, "pw") {
		t.Fatalf("the fallback leaked a value: %s", got)
	}
	for _, want := range []string{"redacted", "id", "password"} {
		if !strings.Contains(got, want) {
			t.Errorf("fallback summary omits %q: %s", want, got)
		}
	}
	// And the no-top-level-keys shape still says something.
	if s := wholeBodySummary([]any{1}); !strings.Contains(s, "redacted") {
		t.Errorf("fallback for a non-object said: %s", s)
	}
}
