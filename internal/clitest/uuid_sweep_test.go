package clitest

import (
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/pgEdge/pgedge-cli/internal/cli"
)

// This is the DERIVED half of the full-UUID contract, and it exists because
// three hand-written lists were each proved bypassable at exactly the
// dimension they did not vary.
//
//   - directUUIDCommands names 44 invocations. Review restored the
//     confirm-before-parse defect on `cluster share delete`, which the
//     list omits, and every gate stayed green.
//   - TestMalformedIDIsRefusedBeforeThePrompt names 10. Same hole,
//     nine verbs wide.
//   - TestNoIDInputResolvesAPrefix reads source with one regex.
//     Hoisting `strings.ToLower` into a local defeats it while the
//     prefix resolves live.
//
// A list cannot cover a tree. So this walks the tree instead: every
// leaf whose `Use` declares a `<*_id>` positional is run with
// `not-a-uuid` in that slot, with NO credentials, and must answer 2.
//
// It DOES catch a resolver reinstated behind the client build: with no
// credentials that answers 5, not 2. An earlier version of this comment
// said the opposite — that such a resolver "answers 5 here either way"
// — and review proved it wrong by reddening this gate with exactly that
// mutation while the source tripwire passed. The tripwire stays for the
// dead-code case, not for this one.
//
// WHAT IT CANNOT SEE IS A FLAG. The walk reads positionals out of
// `Use`; an ID that arrives as `--database-id` is covered by
// uuidFlagSweep below, and that split is the only reason both exist.
// Review reinstated a live resolver on `byoc backup create
// --database-id` and the positional walk alone returned zero failures.
//
// It also covers the ORDERING for free, without needing --force: with
// no credentials the only two things that can answer are the parse and
// cli.Confirm, and the message says which. That is the property the
// hand lists needed a separate table for.
//
// It does NOT subsume directUUIDCommands: that list carries flag-borne
// entries and a controlplane verb, so deleting it would lose coverage.

// uuidPositionalExempt names positionals that are NOT UUIDs, with the
// reason. Anything not listed here MUST be one — the point of an
// allowlist rather than a denylist is that a new `<*_id>` positional
// is covered the day it lands, and its author has to come here to
// argue otherwise.
var uuidPositionalExempt = map[string]idExempt{
	// 8-char hex the platform assigns, as byoc.yaml describes it
	// ("SaaS-generated 8-char hex ID, assigned server-side") and as
	// `database service list` prints it.
	"service_id": {why: "8-char hex assigned server-side, not a UUID"},
}

// idExempt records why an ID-shaped input is not a pgEdge resource
// UUID, and whether that reason is confined to the controlplane module.
//
// controlplaneOnly is a FIELD, and that is the entire point of this type. The
// flag arm used to scope these by
// `strings.Contains(why, "controlplane-scoped")`, so rewording cluster-id's
// reason from "this exemption is controlplane-scoped below" to "this exemption is
// scoped to cp below" — an editorial edit no reviewer would query —
// excused byoc's --cluster-id globally, and a live prefix resolver went
// back onto it with the whole suite green. An exemption scoped by
// matching prose is a hand list in costume: the sentence is not the
// contract, and nothing stops someone improving the sentence.
//
// Both arms now read the field, so the two halves of the sweep cannot
// drift apart either.
type idExempt struct {
	why              string
	controlplaneOnly bool
}

// uuidCommandExempt names whole commands that cannot reach a parse,
// with the reason. Found by this gate on its first run, which is the
// argument for deriving the population rather than listing it: a hand
// list would simply not have contained the case.
var uuidCommandExempt = map[string]idExempt{
	"pgedge starfleet invite accept": {why: "refuses at exit 5 before reading " +
		"its argument, and that is the whole verb: accepting an invite " +
		"records which PERSON joined, so a client credential -- which " +
		"names an application -- can never do it. The refusal is the " +
		"feature, and it must not be reordered behind a parse."},
}

// nameNotUUIDPositionals are positionals that LOOK like ids and are
// names, so the full-UUID rule does not govern them.
//
// This used to exclude cp wholesale, on the grounds that "its ids are
// NAMES". That is true of `<database_id>`, `<host_id>` and
// `<instance_id>` — `controlplane database get storefront` works — and FALSE of
// controlplane's three `<task_id>` positionals, which `parseTaskID` parses as a
// real UUID and refuses at exit 2. Excluding the module hid three
// genuinely covered slots and, worse, put them outside both exemption
// maps, so nothing policed the exclusion. Naming the positionals
// instead keeps cp inside the sweep.
var nameNotUUIDPositionals = map[string]idExempt{
	"database_id": {why: "cp addresses a database by NAME (`cp " +
		"database get storefront`). The cloud modules' <database_id> " +
		"is a UUID.", controlplaneOnly: true},
	"host_id":     {why: "cp host names, not UUIDs", controlplaneOnly: true},
	"instance_id": {why: "controlplane instance names, not UUIDs", controlplaneOnly: true},
}

const controlplaneModule = "controlplane"

func TestEveryIDPositionalRefusesANonUUID(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	checked := 0
	walk(root, func(c *cobra.Command) {
		if c.RunE == nil || c.HasSubCommands() {
			return
		}
		path := strings.Fields(c.CommandPath())
		isCP := len(path) > 1 && path[1] == controlplaneModule
		if _, ok := uuidCommandExempt[c.CommandPath()]; ok {
			return
		}
		// `[flags]` is dropped so the slot index matches
		// synthesizeArgs, which skips it. No Use in the tree carries
		// one before a positional today; a `get [flags] <x_id>` would
		// otherwise misindex onto a flag token instead of tripping the
		// guard below.
		var fields []string
		for _, f := range strings.Fields(c.Use) {
			if f != "[flags]" {
				fields = append(fields, f)
			}
		}
		if len(fields) < 2 {
			return
		}
		for slot, f := range fields[1:] {
			name := strings.Trim(f, "<>[]")
			if !strings.HasSuffix(name, "_id") {
				continue
			}
			if e, ok := uuidPositionalExempt[name]; ok &&
				e.applies(isCP) {
				continue
			}
			// Same structural scoping as the flag arm, through the
			// same method: the cloud modules' <database_id> IS a UUID,
			// so these three excuse cp only.
			if e, ok := nameNotUUIDPositionals[name]; ok &&
				e.applies(isCP) {
				continue
			}
			// COPIED, because synthesizeArgs returns sweepOverrides'
			// own slice for an overridden command — a package-level map
			// of literal slices. Writing into it corrupted the dry-run
			// sweep's fixture, which review reproduced under
			// `-shuffle=1`: "only 43 of 65 verbs had a write
			// intercepted". File order hid it.
			if slot >= len(synthesizeArgs(c)) {
				t.Errorf("%s: cannot place a bad %s — synthesizeArgs "+
					"produced %d args for a Use naming %d positionals",
					c.CommandPath(), name, len(synthesizeArgs(c)),
					len(fields)-1)
				continue
			}

			checked++
			// Every hostile value, not one. See hostileIDValues: a
			// single value cannot tell a missing check from a
			// permissive one, and a permissive one is how the
			// withdrawn prefix feature would return.
			for _, bad := range hostileIDValues {
				args := append([]string(nil), synthesizeArgs(c)...)
				// Replace only THIS slot, so the other positionals
				// stay well-formed and cobra cannot fail first for an
				// unrelated reason.
				args[slot] = bad
				// --force is dropped deliberately: with the parse
				// first, the answer must be the parse's, and dropping
				// it is what makes cli.Confirm a live alternative
				// rather than a skipped one.
				args = withoutForce(args)

				path := strings.Fields(
					strings.TrimPrefix(c.CommandPath(), "pgedge "))
				full := make([]string, 0, len(path)+len(args))
				full = append(full, path...)
				full = append(full, args...)

				t.Run(strings.Join(full, " "), func(t *testing.T) {
					err := runForError(t, full...)
					if err == nil {
						t.Fatalf("`pgedge %s` succeeded with a "+
							"malformed %s",
							strings.Join(full, " "), name)
					}
					if got := cli.ExitCode(err); got != cli.ExitUsage {
						t.Errorf("exit %d, want %d for a malformed "+
							"%s.\n%v", got, cli.ExitUsage, name, err)
					}
					if !strings.Contains(err.Error(), bad) {
						t.Errorf("`pgedge %s` answered %q, which does "+
							"not name the bad %s.\nEither the ID is "+
							"not checked at all, or the check sits "+
							"after something else that refuses first "+
							"— a cli.Confirm that has not read the "+
							"argument, or a client build. Both were "+
							"live defects.",
							strings.Join(full, " "), err.Error(), name)
					}
				})
			}
		}
	})

	// A walk that found nothing looks exactly like a walk that found no
	// violations — and a LOOSE floor is barely better. At 30 against a
	// real population of 72, review dropped every `database` verb in
	// both modules (32 slots) and this still passed. The floor sits
	// just under the true count, like the others in this file.
	// EXACT, for the reason the flag arm gives. The floor here was 70
	// against a measured 75 -- five slots of room, where round 5's own
	// argument was that four slots of room is what let a reviewer
	// delete a real check and stay green. Measured: one added
	// uuidPositionalExempt entry for `ingress_id` removes five real
	// slots, lands on 70 exactly, and every gate stays green, taking
	// two verbs with it that no hand list covers either.
	//
	// The comment that used to sit here claimed 72 and the PR body
	// claimed 73; both were wrong when written, and no `Use` string
	// had changed. Hence a number that cannot drift without failing.
	// 79 -> 85: the six `managed database allowlist` verbs (get, add,
	// remove, set, open, clear) each take a `<database_id>` positional;
	// `client-ip` takes none.
	// 85 -> 91: `managed database branch` list and create take one
	// `<database_id>` positional each; get and delete take two
	// (`<database_id> <branch_id>`).
	// 91 -> 95: `managed database branch metrics` and `logs`
	// each take two (`<database_id> <branch_id>`).
	if checked != 95 {
		t.Errorf("%d ID positionals swept, want exactly 95. Adding or "+
			"removing one is a deliberate edit here.", checked)
	}
}

// Every exemption must earn its place: an entry naming an input no
// command declares is a stale excuse, and the next reader takes it for
// a live fact about the tree.
//
// ALL FOUR tables are policed here, which two of them were not. The
// comment above uuidFlagExempt claimed "the same test polices it" while
// nothing did, so a dead flag entry passed — and a dead entry is how an
// exemption outlives the reason for it. nameNotUUIDPositionals was
// unpoliced too.
//
// controlplaneOnly is checked as well as existence, because a scope can rot in
// the other direction: an exemption marked controlplane-only that no longer
// appears on any controlplane command excuses nothing, and one NOT marked controlplane-only
// that appears only on cp is quietly wider than it needs to be.
func TestUUIDExemptionsAreLive(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	// Collected from leaves only, and separately per kind, so a
	// positional entry cannot be excused by a same-named flag.
	positionals := map[string][]string{}
	flags := map[string][]string{}
	paths := map[string]bool{}
	walk(root, func(c *cobra.Command) {
		paths[c.CommandPath()] = true
		if c.RunE == nil || c.HasSubCommands() {
			return
		}
		on := c.CommandPath()
		for _, f := range strings.Fields(c.Use)[1:] {
			name := strings.Trim(f, "<>[]")
			positionals[name] = append(positionals[name], on)
		}
		c.Flags().VisitAll(func(f *pflag.Flag) {
			flags[f.Name] = append(flags[f.Name], on)
		})
	})

	check := func(table string, m map[string]idExempt,
		where map[string][]string,
	) {
		for name, e := range m {
			if strings.TrimSpace(e.why) == "" {
				t.Errorf("%s[%q] carries no reason", table, name)
			}
			on := where[name]
			if len(on) == 0 {
				t.Errorf("%s excuses %q (%s), but nothing in the tree "+
					"declares it. Remove the entry: an exemption that "+
					"excuses nothing reads as a live fact about the "+
					"tree, and outlives the reason for it.",
					table, name, e.why)
				continue
			}
			// A controlplane-only exemption has to have a controlplane command to excuse,
			// and a global one has to be needed outside cp.
			var onCP, offCP int
			for _, p := range on {
				if f := strings.Fields(p); len(f) > 1 &&
					f[1] == controlplaneModule {
					onCP++
				} else {
					offCP++
				}
			}
			if e.controlplaneOnly && onCP == 0 {
				t.Errorf("%s[%q] is marked controlplaneOnly but appears on no "+
					"controlplane command (%v). The scope is wrong or the "+
					"entry is dead.", table, name, on)
			}
			if !e.controlplaneOnly && offCP == 0 {
				t.Errorf("%s[%q] is NOT marked controlplaneOnly but appears "+
					"only on controlplane commands (%v). It is wider than it "+
					"needs to be, which is exactly how byoc's "+
					"--cluster-id got excused once already.",
					table, name, on)
			}
		}
	}

	check("uuidPositionalExempt", uuidPositionalExempt, positionals)
	check("nameNotUUIDPositionals", nameNotUUIDPositionals, positionals)
	check("uuidFlagExempt", uuidFlagExempt, flags)

	for path, e := range uuidCommandExempt {
		if strings.TrimSpace(e.why) == "" {
			t.Errorf("uuidCommandExempt[%q] carries no reason", path)
		}
		if !paths[path] {
			t.Errorf("uuidCommandExempt excuses %q (%s), but no such "+
				"command exists. A renamed command silently drops out "+
				"of the sweep and keeps its excuse.", path, e.why)
		}
	}
}

// withoutForce drops --force from a synthesised argument list.
func withoutForce(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != "--force" {
			out = append(out, a)
		}
	}
	return out
}

// uuidFlagExempt names `--*-id` flags that are NOT pgEdge resource
// UUIDs, with the reason. This is the same allowlist discipline as the
// positional map, and the same test polices it.
//
// The five in the middle are why byoc's reference carries a table
// rather than a blanket sentence: an earlier draft said every `--*-id`
// takes a full UUID, and review probed all five to show it false.
var uuidFlagExempt = map[string]idExempt{
	"service-id": {why: "8-char hex the platform assigns, as " +
		"byoc.yaml describes it and as `database service list` prints " +
		"it"},
	"tenant-id": {why: "the cloud provider's own tenant identifier, " +
		"forwarded as a credential"},
	"subscription-id": {why: "an Azure subscription identifier, " +
		"forwarded as a credential"},
	"azure-client-id": {why: "an Azure client identifier, forwarded " +
		"as a credential"},
	"project-id": {why: "a GCP project id, which is a NAME and can " +
		"never be a UUID"},
	"cluster-id": {why: "cp addresses a cluster by NAME (`controlplane cluster " +
		"init --cluster-id prod`). byoc's --cluster-id IS a UUID and " +
		"is swept.", controlplaneOnly: true},
	"entity-id": {why: "cp entity names, not UUIDs", controlplaneOnly: true},
}

// The FLAG half of the sweep, and the reason it is separate: the
// positional walk reads `Use`, so an ID arriving as `--database-id` was
// covered by nothing. Review reinstated a live prefix resolver on
// `byoc backup create --database-id` — behind clientFromCmd, with the
// hoisted-ToLower spelling the source regex cannot see — and the whole
// suite returned zero failures.
//
// Six verbs were in that hole, every one of them with a real
// parseUUIDArg pinned by nothing: both products' `backup create`,
// `byoc backup-store create`, `byoc cluster create` and `update`,
// `byoc backup-repository list` and `byoc ingress service register`.
func TestEveryIDFlagRefusesANonUUID(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	checked := 0
	walk(root, func(c *cobra.Command) {
		if c.RunE == nil || c.HasSubCommands() {
			return
		}
		path := strings.Fields(c.CommandPath())
		isCP := len(path) > 1 && path[1] == controlplaneModule
		if _, ok := uuidCommandExempt[c.CommandPath()]; ok {
			return
		}

		var names []string
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if !strings.HasSuffix(f.Name, "-id") {
				return
			}
			if e, ok := uuidFlagExempt[f.Name]; ok && e.applies(isCP) {
				return
			}
			names = append(names, f.Name)
		})
		sort.Strings(names)

		for _, name := range names {
			checked++
			for _, bad := range hostileIDValues {
				args := append([]string(nil), synthesizeArgs(c)...)
				args = withoutForce(args)
				// Replace this flag's value where synthesizeArgs
				// already supplied it; append the pair otherwise.
				// Either way the other flags keep the values that get
				// the verb this far.
				if i := indexOf(args, "--"+name); i >= 0 &&
					i+1 < len(args) {
					args[i+1] = bad
				} else {
					args = append(args, "--"+name, bad)
				}

				full := make([]string, 0, len(path)+len(args))
				full = append(full, path[1:]...)
				full = append(full, args...)

				t.Run(strings.Join(full, " "), func(t *testing.T) {
					err := runForError(t, full...)
					if err == nil {
						t.Fatalf("`pgedge %s` succeeded with a "+
							"malformed --%s",
							strings.Join(full, " "), name)
					}
					if got := cli.ExitCode(err); got != cli.ExitUsage {
						t.Errorf("exit %d, want %d for a malformed "+
							"--%s.\n%v",
							got, cli.ExitUsage, name, err)
					}
					if !strings.Contains(err.Error(), bad) {
						t.Errorf("`pgedge %s` answered %q, which "+
							"does not name the bad --%s.\nEither the "+
							"flag is not checked, or the check sits "+
							"behind something that refuses first — a "+
							"client build, or a cli.Confirm that has "+
							"not read it. If the flag is not a pgEdge "+
							"resource UUID at all, say so in "+
							"uuidFlagExempt.",
							strings.Join(full, " "), err.Error(), name)
					}
				})
			}
		}
	})

	// 13 against 14 real slots, counted from the tree: 2
	// --backup-store-id, 2 --cloud-account-id, 3 --cluster-id (byoc's;
	// controlplane's is exempt), 5 --database-id and 2 --subject-id. One verb's
	// worth of slack so a new required flag cannot redden the build on
	// its own, and no more — the old floor of 10 left four slots of
	// room, which is what let a reviewer delete a real check and stay
	// green. A round number is not a measurement.
	// EXACT, not a floor, and that is the round-6 fix. Any floor
	// leaves slack, and the slack IS the escape: with 13 against 14, a
	// review added one uuidCommandExempt entry for `cluster update`
	// and deleted that verb's real --backup-store-id parse, landing on
	// the floor exactly with build, vet, make test at 91.3% and lint
	// all green. uuidCommandExempt is read by both arms with a bare
	// lookup and removes a WHOLE command, so it was the one table
	// round 5 left unscoped and unfloored.
	//
	// An equality makes both directions deliberate: removing an ID
	// input reddens the build, and adding one reddens it too, so a new
	// flag cannot arrive unswept. The derivation, walked from the
	// tree: 2 --backup-store-id, 2 --cloud-account-id, 3 byoc
	// --cluster-id (controlplane's is exempt), 5 --database-id, 2 --subject-id.
	if checked != 14 {
		t.Errorf("%d ID flags swept, want exactly 14. Adding or "+
			"removing one is a deliberate edit here -- and check "+
			"uuidCommandExempt, which removes a whole command from "+
			"both arms and is the only table that can do so.", checked)
	}
}

// indexOf returns the position of want in args, or -1.
func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// hostileIDValues are the values every ID input must refuse, and the
// list exists because every gate in this repo probed exactly ONE.
//
// A single hostile value can only ever catch a check that is absent.
// It cannot catch a check that is PRESENT and too permissive, and that
// is the shape the withdrawn feature would come back in: reinstate
// prefix acceptance for lengths 12 to 31 and every gate here stays
// green, because `not-a-uuid` is 10 characters and the 8-hex prefix the
// other tests use is 8. The dimension to vary is the LENGTH.
//
// Lengths 4, 8, 12, 20, 31 and 35 bracket that window from both sides:
// 4 and 8 are the short prefixes a person would type, 12 to 31 is the
// window a permissive parser would accept, and 35 is one character
// short of a whole UUID — the value a truncating copy-paste produces.
// All six are prefixes of a REAL UUID, so nothing but the length makes
// them invalid, and a check that merely pattern-matches hex will pass
// them through.
// The base is deliberately NOT sweepUUID. synthesizeArgs feeds
// sweepUUID to every sibling ID slot, so a prefix cut from it can be
// matched by an error naming a DIFFERENT argument, and the
// strings.Contains assertion would pass for the wrong reason.
const hostileIDBase = "a1b2c3d4-1111-4111-8111-111111111111"

// hostileIDLengths are the prefix lengths, bracketing the window a
// permissive parser would accept from both sides: 4 and 8 are what a
// person types, 12 to 31 is the window, and 35 is one character short
// of a whole UUID -- what a truncating copy-paste produces.
var hostileIDLengths = []int{4, 8, 12, 20, 31, 35}

var hostileIDValues = func() []string {
	out := []string{
		"not-a-uuid",
		// FULL LENGTH, wrong internal structure. Every other value
		// here is a prefix, so all of them are the right alphabet and
		// the right hyphen positions FOR THEIR LENGTH, and none is 36
		// characters. A check that validates the SHAPE rather than
		// parsing -- `^[0-9a-fA-F-]{36}$` -- therefore passed the whole
		// table, and 70 call sites would then send the zero UUID to the
		// API while four sent the malformed string. Measured: the
		// entire repo suite green under exactly that mutation.
		hostileIDBase[:35] + "-",
		// An explicitly empty value. Two flags used to treat it as an
		// omitted filter, which is why it could not be here before:
		// `backup-repository list --database-id ""` silently widened
		// the read to every database, and `cluster update
		// --backup-store-id ""` answered exit 1 from the
		// at-least-one rule. Both are exit 2 now.
		"",
	}
	for _, n := range hostileIDLengths {
		out = append(out, hostileIDBase[:n])
	}
	return out
}()

// TestHostileIDValuesCoverTheLengthWindow guards the table itself,
// which nothing did. Emptying hostileIDLengths left `go test ./...`
// rc=0 with both sweeps intact, because a floor counts SLOTS and
// cannot see the value list shrink -- so the round's headline artefact
// was one deleted literal from being inert. Its two sibling tables in
// this file both carry guards; this one did not.
func TestHostileIDValuesCoverTheLengthWindow(t *testing.T) {
	want := []int{4, 8, 12, 20, 31, 35}
	if len(hostileIDLengths) != len(want) {
		t.Fatalf("hostileIDLengths has %d entries, want %d: %v",
			len(hostileIDLengths), len(want), hostileIDLengths)
	}
	for i, n := range want {
		if hostileIDLengths[i] != n {
			t.Errorf("hostileIDLengths[%d] = %d, want %d",
				i, hostileIDLengths[i], n)
		}
	}
	// The three that are not prefixes, named so shortening the list
	// cannot drop them silently either.
	const wantValues = 3 + 6
	if len(hostileIDValues) != wantValues {
		t.Errorf("hostileIDValues has %d entries, want %d: %q",
			len(hostileIDValues), wantValues, hostileIDValues)
	}
	for _, must := range []string{
		"not-a-uuid", "", hostileIDBase[:35] + "-",
	} {
		found := false
		for _, v := range hostileIDValues {
			if v == must {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("hostileIDValues no longer carries %q", must)
		}
	}
	// And every prefix must genuinely fail to parse, or it is not
	// hostile at all -- which is what rules the braced and
	// urn:uuid: forms out: uuid.Parse ACCEPTS both.
	for _, v := range hostileIDValues {
		if _, err := uuid.Parse(v); err == nil {
			t.Errorf("%q parses as a UUID, so it cannot test a "+
				"refusal", v)
		}
	}
}

// applies reports whether this exemption covers the command now being
// walked. A controlplane-only exemption excuses nothing outside cp.
func (e idExempt) applies(isCP bool) bool { return !e.controlplaneOnly || isCP }
