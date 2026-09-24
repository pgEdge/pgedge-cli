package httplog

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The redaction list is hand-maintained, and it drifted: the four
// original entries predated the service configs, so `--debug` printed
// MCP bearer tokens, LLM API keys and the PostgREST signing secret in
// clear text for two whole modules.
//
// Extending the list fixes today. This gate is what stops it drifting
// again: it reads the generated clients, casts a deliberately wide net
// over their field names, and requires every catch to be classified —
// either redacted, or explicitly declared harmless with a reason.
//
// A new spec field cannot then land unnoticed. The failure mode being
// prevented is silent: nothing breaks when a secret leaks, so no other
// test would go red.

// suspiciousField is the net. It is wide on purpose — over-catching
// costs a one-line entry in notSecrets, under-catching costs a leaked
// credential.
var suspiciousField = regexp.MustCompile(
	`(?i)secret|token|password|passwd|key|cred|auth|private|passphrase`)

// notSecrets are generated field names the net catches that must NOT be
// redacted, each with the reason.
//
// Nothing needs an entry merely for containing a secret: the check is
// depth-aware, so controlplane's database_users is redacted through the password
// on each nested user spec without its own name being listed. Entries
// here are for names that LOOK credential-bearing and are not.
//
// They matter as much as the redacted set: blanking a body costs the
// diagnostic value --debug exists for, so a field that merely reads as
// sensitive should not be added to secretFieldNames on a hunch.
var notSecrets = map[string]string{
	"auth0_id":           "an identifier, not a credential",
	"is_primary_key":     "a bool describing a column",
	"jwt_role_claim_key": "a JSONPath into the JWT, not the signing secret",
	"key_name":           "the human name of an SSH key, not the key",
	"primary_key":        "column names",
	"private_domain":     "an internal hostname",
	"private_subnets":    "subnet identifiers",
	"public_key":         "public by definition; the private half is never sent",
	"ssh_key_id":         "an identifier",
	"token_budget":       "an int: max completion tokens",
	"token_type":         `the literal "Bearer"`,
}

// apiFieldNames returns every distinct JSON object key declared by the
// generated API clients.
func apiFieldNames(t *testing.T) map[string][]string {
	t.Helper()

	// The generated clients sit either as a direct sibling of this
	// package (cp) or one level deeper under internal/starfleet (byoc,
	// account, managed).
	topFiles, err := filepath.Glob(filepath.Join("..", "*", "api", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	nestedFiles, err := filepath.Glob(
		filepath.Join("..", "*", "*", "api", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	files := make([]string, 0, len(topFiles)+len(nestedFiles))
	files = append(files, topFiles...)
	files = append(files, nestedFiles...)
	// Positive control: the glob found the clients at all. A broken
	// path returns zero files and this gate would pass having read
	// nothing, which looks exactly like full coverage.
	if len(files) < 3 {
		t.Fatalf("found only %d generated client files; expected at "+
			"least 3 (byoc, cp, account, managed) — has the layout "+
			"changed?", len(files))
	}

	tag := regexp.MustCompile("`json:\"([a-z_0-9]+)")
	out := make(map[string][]string)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		mod := modFromClientPath(f)
		for _, m := range tag.FindAllStringSubmatch(string(raw), -1) {
			name := m[1]
			if !contains(out[name], mod) {
				out[name] = append(out[name], mod)
			}
		}
	}
	if len(out) < 50 {
		t.Fatalf("parsed only %d field names; the tag regexp has "+
			"probably stopped matching", len(out))
	}
	return out
}

// modFromClientPath returns the module name a generated client file
// belongs to: the path segment immediately before "api". This holds
// whether the client sits directly under internal/ (cp) or one level
// deeper under internal/starfleet/ (byoc, account, managed).
func modFromClientPath(f string) string {
	parts := strings.Split(filepath.ToSlash(f), "/")
	for i, p := range parts {
		if p == "api" && i > 0 {
			return parts[i-1]
		}
	}
	return ""
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// TestEverySuspiciousAPIFieldIsClassified is the gate.
func TestEverySuspiciousAPIFieldIsClassified(t *testing.T) {
	fields := apiFieldNames(t)

	redacted := make(map[string]bool, len(secretFieldNames))
	for _, n := range secretFieldNames {
		redacted[n] = true
	}

	var caught, unclassified []string
	for name, mods := range fields {
		if !suspiciousField.MatchString(name) {
			continue
		}
		caught = append(caught, name)
		if redacted[name] {
			continue
		}
		if _, ok := notSecrets[name]; ok {
			continue
		}
		unclassified = append(unclassified,
			name+" (in "+strings.Join(mods, ", ")+")")
	}

	// Positive control: the net caught something. A regexp that stopped
	// matching would leave this loop empty and the gate green.
	if len(caught) < 15 {
		t.Fatalf("the net caught only %d field names (%v); it has "+
			"probably stopped matching", len(caught), caught)
	}

	sort.Strings(unclassified)
	for _, u := range unclassified {
		t.Errorf("generated field %s is neither redacted nor listed in "+
			"notSecrets. If it carries a credential add it to "+
			"secretFieldNames; if it does not, add it to notSecrets "+
			"with the reason.", u)
	}
}

// TestNoFieldIsBothRedactedAndDeclaredHarmless pins that the two sets
// stay disjoint, so a reader cannot be told both things at once.
func TestNoFieldIsBothRedactedAndDeclaredHarmless(t *testing.T) {
	for _, n := range secretFieldNames {
		if reason, ok := notSecrets[n]; ok {
			t.Errorf("%q is in secretFieldNames AND in notSecrets "+
				"(%q) — decide which", n, reason)
		}
	}
}

// TestEveryHarmlessEntryCarriesAReasonAndStillExists keeps the
// allowlist from rotting. An entry that no longer matches any generated
// field is dead weight that makes the next reader think a name is
// accounted for when nothing declares it.
func TestEveryHarmlessEntryCarriesAReasonAndStillExists(t *testing.T) {
	fields := apiFieldNames(t)
	for name, reason := range notSecrets {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("notSecrets[%q] must carry a reason", name)
		}
		if _, ok := fields[name]; !ok {
			t.Errorf("notSecrets[%q] names no generated field; remove it",
				name)
		}
		if !suspiciousField.MatchString(name) {
			t.Errorf("notSecrets[%q] is not caught by the net, so the "+
				"entry excuses nothing; remove it", name)
		}
	}
}
