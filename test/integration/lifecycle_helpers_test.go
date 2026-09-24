package integration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The lifecycle suite creates real cloud infrastructure and destroys it
// again, so it is gated separately from the read-only sweep: a run
// costs money and takes tens of minutes.
const (
	lifecycleEnv = "PGEDGE_INTEGRATION_LIFECYCLE"

	// exitNotFound is the CLI's exit code for a 404, used to tell "the
	// resource is gone" apart from "the call failed".
	exitNotFound = 4
)

// lifecycleCfg holds everything the lifecycle test needs from the
// environment. Every value has a default except the cloud account,
// which is tenant-specific and must be supplied deliberately.
type lifecycleCfg struct {
	cloudAccountID string
	region         string
	instanceType   string
	volumeSize     string
	pgVersion      string
	namePrefix     string

	// runID is a short lowercase-alphanumeric token shared by every
	// resource in one run, so they are recognisably one set and
	// repeated runs cannot collide.
	runID string

	// existingStoreID, when set, is used instead of creating a backup
	// store. A store that has hosted a database keeps that database's
	// backup data, and pgEdge deletes a store only when it is empty
	// (see bucketNotEmpty), so each run that creates its own store
	// leaves one behind for the customer to clear. Pointing repeated
	// runs at a single store keeps that to one.
	existingStoreID string

	// llmAPIKey is used for the RAG leg. When empty a placeholder is
	// sent: the service is still registered on the database, but it
	// cannot be expected to reach a running state, so the RAG check is
	// downgraded to a warning.
	llmAPIKey string

	clusterTimeout  time.Duration
	databaseTimeout time.Duration
	storeTimeout    time.Duration
	serviceTimeout  time.Duration
	backupTimeout   time.Duration
	deleteTimeout   time.Duration
}

// requireLifecycle skips unless the caller has opted into creating real
// infrastructure and named a cloud account to create it in.
func requireLifecycle(t *testing.T) lifecycleCfg {
	t.Helper()
	if os.Getenv(lifecycleEnv) != "1" {
		t.Skipf("set %s=1 to run the build-up/tear-down suite "+
			"(creates real cloud infrastructure)", lifecycleEnv)
	}

	cfg := lifecycleCfg{
		cloudAccountID: os.Getenv("PGEDGE_INTEGRATION_CLOUD_ACCOUNT_ID"),
		region:         envOr("PGEDGE_INTEGRATION_REGION", "us-east-2"),
		// r6g.medium is the second entry in the API's own AWS default
		// preference list and the smallest of those with enough memory
		// to host Postgres plus three services. Overridable, but note
		// the API only checks that AWS offers the type in the region,
		// so an under-sized type fails late, during provisioning.
		instanceType: envOr("PGEDGE_INTEGRATION_INSTANCE_TYPE",
			"r6g.medium"),
		volumeSize: envOr("PGEDGE_INTEGRATION_VOLUME_SIZE", "30"),
		pgVersion:  envOr("PGEDGE_INTEGRATION_PG_VERSION", "16"),
		namePrefix: envOr("PGEDGE_INTEGRATION_NAME_PREFIX", "clitest"),
		existingStoreID: os.Getenv(
			"PGEDGE_INTEGRATION_BACKUP_STORE_ID"),
		llmAPIKey: os.Getenv("PGEDGE_INTEGRATION_LLM_API_KEY"),

		clusterTimeout:  envDuration(t, "PGEDGE_INTEGRATION_CLUSTER_TIMEOUT", 60*time.Minute),
		databaseTimeout: envDuration(t, "PGEDGE_INTEGRATION_DATABASE_TIMEOUT", 30*time.Minute),
		storeTimeout:    envDuration(t, "PGEDGE_INTEGRATION_STORE_TIMEOUT", 15*time.Minute),
		serviceTimeout:  envDuration(t, "PGEDGE_INTEGRATION_SERVICE_TIMEOUT", 20*time.Minute),
		backupTimeout:   envDuration(t, "PGEDGE_INTEGRATION_BACKUP_TIMEOUT", 5*time.Minute),
		deleteTimeout:   envDuration(t, "PGEDGE_INTEGRATION_DELETE_TIMEOUT", 30*time.Minute),
	}

	if cfg.cloudAccountID == "" {
		t.Fatalf("%s=1 requires PGEDGE_INTEGRATION_CLOUD_ACCOUNT_ID "+
			"(the cloud account to build in — use your own, the dev "+
			"tenant is shared)", lifecycleEnv)
	}

	// Base 36 keeps the shared token at 6 characters, which matters
	// because the backup store name budget is only 14.
	cfg.runID = strconv.FormatInt(time.Now().UTC().Unix(), 36)

	if !validNamePrefix.MatchString(cfg.namePrefix) {
		t.Fatalf("PGEDGE_INTEGRATION_NAME_PREFIX=%q: must be lowercase "+
			"alphanumeric or hyphens and end alphanumeric",
			cfg.namePrefix)
	}
	if budget := storeNameMaxLen - len(cfg.runID) - 1; len(cfg.namePrefix) > budget {
		t.Fatalf("PGEDGE_INTEGRATION_NAME_PREFIX=%q is %d characters; "+
			"backup store names are capped at %d and this run needs "+
			"%d for its id, so the prefix must be at most %d",
			cfg.namePrefix, len(cfg.namePrefix), storeNameMaxLen,
			len(cfg.runID)+1, budget)
	}
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(t *testing.T, key string, fallback time.Duration) time.Duration {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		t.Fatalf("%s=%q: %v", key, v, err)
	}
	return d
}

// Each resource type enforces its own name rules server-side. Getting
// them wrong wastes a run, so they are encoded here rather than
// discovered:
//
//   - backup store (AWS/GCP): at most 14 characters, [a-z0-9-]. The
//     limit exists because the name is embedded in an S3 bucket name
//     alongside a store id, a "pgedge" prefix and a "logs" suffix.
//     Azure allows 19; 14 is used for all of them.
//   - cluster and node logical names: at most 63, [a-z0-9-], must
//     start and end alphanumeric, and cannot be localhost, primary or
//     replica.
//   - database: at most 63, lowercase, must start with a letter or
//     underscore, and may contain only letters, digits and
//     underscores — no hyphens.
const (
	storeNameMaxLen = 14
)

// validNamePrefix mirrors the strictest charset the prefix must satisfy
// across all of the above.
var validNamePrefix = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// storeName fits the 14-character backup store budget.
func (c lifecycleCfg) storeName() string {
	return c.namePrefix + "-" + c.runID
}

// clusterName and sshKeyName have room for a type marker.
func (c lifecycleCfg) clusterName() string {
	return c.namePrefix + "-cl-" + c.runID
}

func (c lifecycleCfg) sshKeyName() string {
	return c.namePrefix + "-sk-" + c.runID
}

// databaseName drops the hyphens the Postgres name rules reject.
func (c lifecycleCfg) databaseName() string {
	return strings.ReplaceAll(c.namePrefix, "-", "") + c.runID
}

// --- running the CLI without failing the test ---

// cliResult is the outcome of one CLI invocation.
type cliResult struct {
	stdout   []byte
	stderr   string
	exitCode int
	err      error
}

// notFound reports whether the call failed because the resource does
// not exist.
func (r cliResult) notFound() bool {
	return r.exitCode == exitNotFound
}

// runCLI runs the CLI and returns the result rather than failing the
// test. Teardown and polling both need to inspect a failure and carry
// on; runJSON's fail-fast behaviour would abandon live infrastructure.
func runCLI(t *testing.T, args ...string) cliResult {
	t.Helper()
	full := append([]string{
		"--profile", profile(), "-o", "json",
	}, args...)

	cmd := exec.Command(binary(t), full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := cliResult{
		stdout: stdout.Bytes(),
		stderr: stderr.String(),
		err:    err,
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.exitCode = exitErr.ExitCode()
	}
	return res
}

// mustObject runs the CLI and decodes a single JSON object, failing the
// test on either a non-zero exit or an undecodable body.
func mustObject(t *testing.T, args ...string) map[string]any {
	t.Helper()
	res := runCLI(t, args...)
	if res.err != nil {
		t.Fatalf("pgedge %s: %v\nstdout: %s\nstderr: %s",
			strings.Join(args, " "), res.err,
			truncate(res.stdout), res.stderr)
	}
	var obj map[string]any
	if err := json.Unmarshal(res.stdout, &obj); err != nil {
		t.Fatalf("pgedge %s: decode object: %v\nbody: %s",
			strings.Join(args, " "), err, truncate(res.stdout))
	}
	return obj
}

// idOf pulls the "id" field out of a created resource.
func idOf(t *testing.T, what string, obj map[string]any) string {
	t.Helper()
	id, ok := obj["id"].(string)
	if !ok || id == "" {
		t.Fatalf("%s: response carries no id: %v", what, obj)
	}
	return id
}

// stringField reads a string field, returning "" when absent or of
// another type.
func stringField(obj map[string]any, key string) string {
	v, _ := obj[key].(string)
	return v
}

// --- teardown ---

// teardownStep is one resource to remove, in the order it must happen.
type teardownStep struct {
	what   string
	remove func() error

	// independent marks a resource that survives on its own if removal
	// fails, and therefore belongs in the leak report. A service does
	// not: it lives inside its database, so deleting the database
	// removes it. Reporting one as leaked sends the operator chasing
	// something that no longer exists.
	independent bool
}

// teardown collects removal steps and runs them in reverse order of
// registration, so resources come down in the opposite order to the
// one they were built in. Every step runs even if an earlier one
// fails: the alternative is leaving paid cloud resources running.
type teardown struct {
	t     *testing.T
	steps []teardownStep
}

func newTeardown(t *testing.T) *teardown {
	return &teardown{t: t}
}

// add registers a removal step for a resource that survives on its own.
// Call it immediately after the resource is created, before any
// assertion on it, so a failed assertion still tears it down.
func (td *teardown) add(what string, remove func() error) {
	td.steps = append(td.steps, teardownStep{
		what: what, remove: remove, independent: true,
	})
}

// addDependent registers a removal step for a resource that cannot
// outlive its parent, so a failure is reported but not counted as a
// leak.
func (td *teardown) addDependent(what string, remove func() error) {
	td.steps = append(td.steps, teardownStep{
		what: what, remove: remove, independent: false,
	})
}

// errSkipRemoval is returned by a removal step that deliberately left
// the resource in place, so the log does not claim it was removed.
var errSkipRemoval = errors.New("left in place")

// run removes every registered resource in reverse order. It reports
// each failure through t.Errorf and, at the end, lists everything left
// behind in one place so a failed run cannot quietly leak
// infrastructure.
func (td *teardown) run() {
	t := td.t
	var leaked, retained []string

	for i := len(td.steps) - 1; i >= 0; i-- {
		step := td.steps[i]
		t.Logf("teardown: removing %s", step.what)
		err := step.remove()
		switch {
		case errors.Is(err, errSkipRemoval):
			// Deliberately kept, per documented product behaviour.
			// Reported so nobody has to go looking, but not a failure:
			// the verb did the right thing.
			t.Logf("teardown: left %s in place by design — see the note "+
				"above", step.what)
			retained = append(retained, step.what)
		case err != nil:
			t.Errorf("teardown: FAILED to remove %s: %v", step.what, err)
			if step.independent {
				leaked = append(leaked, step.what)
			}
		default:
			t.Logf("teardown: removed %s", step.what)
		}
	}

	if len(retained) > 0 {
		t.Logf("retained by design, and yours to clear when you want "+
			"the resources gone:\n  %s", strings.Join(retained, "\n  "))
	}
	if len(leaked) > 0 {
		t.Errorf("teardown incomplete — these resources survive on "+
			"their own and cost money until removed by hand:\n  %s",
			strings.Join(leaked, "\n  "))
	}
}

// waitDatabaseModifiable blocks until a database will accept another
// change. The API rejects a service edit with
// "database in unmodifiable state: modifying" while a previous change
// is still settling, which made teardown fail to remove two services
// that a preceding deploy had left in flight.
func waitDatabaseModifiable(
	t *testing.T, dbID string, timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string

	for {
		res := runCLI(t, "byoc", "database", "get", dbID)
		if res.notFound() {
			return
		}
		if res.err == nil {
			var obj map[string]any
			if json.Unmarshal(res.stdout, &obj) == nil {
				status := stringField(obj, "status")
				if status != last {
					t.Logf("database %s: status %q, waiting to be "+
						"modifiable", dbID, status)
					last = status
				}
				if done, _ := terminalStatus(status); done {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Logf("database %s: still %q after %s; continuing anyway",
				dbID, last, timeout)
			return
		}
		time.Sleep(15 * time.Second)
	}
}

// --- polling ---

// terminalStatus classifies a resource status or service state as
// good, bad, or still in progress.
//
// The vocabulary is the API's SystemStatus set (queued, creating,
// modifying, deleting, available, degraded, failed, maintenance,
// rebooting, backing-up, archiving, archived, restoring, suspended)
// for clusters, databases, backup stores and ingresses, plus
// "available" for a finished pgbackrest backup and the control plane's
// own state strings for services.
//
// It is deliberately NOT for task status: a task reports "running"
// while still in progress, whereas a running service is up. This
// suite polls resources and services, never tasks.
func terminalStatus(status string) (done, ok bool) {
	switch strings.ToLower(status) {
	case "available", "active", "running", "completed", "complete",
		"succeeded", "success", "done":
		return true, true
	case "failed", "error", "errored", "cancelled", "canceled":
		return true, false
	case "degraded", "suspended", "archived":
		// Terminal, and not what a freshly built resource should
		// settle into. Classed as failure so the run reports it and
		// tears down immediately instead of polling to its deadline.
		return true, false
	default:
		// queued, creating, modifying, deleting, rebooting,
		// backing-up, archiving, restoring, maintenance, and anything
		// unrecognised: keep polling until the deadline.
		return false, false
	}
}

// waitForStatus polls a get verb until the resource reaches a terminal
// status, failing the test on a bad terminal status or on timeout.
func waitForStatus(
	t *testing.T, what string, timeout time.Duration, getArgs ...string,
) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	interval := 15 * time.Second
	var last string

	for {
		res := runCLI(t, getArgs...)
		if res.err != nil {
			// A transient API error should not abandon live
			// infrastructure; keep polling until the deadline.
			t.Logf("%s: get failed (retrying): %v\nstderr: %s",
				what, res.err, res.stderr)
		} else {
			var obj map[string]any
			if err := json.Unmarshal(res.stdout, &obj); err != nil {
				t.Logf("%s: undecodable body (retrying): %s",
					what, truncate(res.stdout))
			} else {
				status := stringField(obj, "status")
				if status != last {
					t.Logf("%s: status %q (%s left of %s budget)",
						what, status,
						time.Until(deadline).Round(time.Second),
						timeout)
					last = status
				}
				if done, good := terminalStatus(status); done {
					if !good {
						t.Fatalf("%s: reached failed status %q: %v",
							what, status, obj)
					}
					return obj
				}
			}
		}

		if time.Now().After(deadline) {
			t.Fatalf("%s: still %q after %s", what, last, timeout)
		}
		time.Sleep(interval)
	}
}

// waitGone polls a get verb until it reports the resource is missing.
// Deletion is asynchronous, and a dependent resource (a backup store
// attached to a cluster) cannot be removed until its dependant is
// really gone.
func waitGone(
	t *testing.T, what string, timeout time.Duration, getArgs ...string,
) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	interval := 15 * time.Second

	var last string
	for {
		res := runCLI(t, getArgs...)
		if res.notFound() {
			return nil
		}
		// Status is logged but never concluded from. A resource on its
		// way out legitimately passes through statuses that
		// terminalStatus classes as failure — an observed database
		// delete went "degraded" before disappearing. Treating that as
		// terminal abandoned the wait early, so the dependent cluster
		// delete then failed with "cluster has databases" and the
		// store delete with "backup store is in use by clusters": one
		// misread status stranded three live resources. Gone or not
		// gone is the only signal here.
		if res.err == nil {
			var obj map[string]any
			if json.Unmarshal(res.stdout, &obj) == nil {
				if status := stringField(obj, "status"); status != last {
					t.Logf("%s: deleting, status %q", what, status)
					last = status
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: still present after %s (last status %q)",
				what, timeout, last)
		}
		time.Sleep(interval)
	}
}

// deleteAndWait issues a forced delete and waits for the resource to
// disappear. Returned errors are teardown failures, reported rather
// than fatal.
func deleteAndWait(
	t *testing.T, what string, timeout time.Duration,
	deleteArgs []string, getArgs []string,
) error {
	t.Helper()
	deadline := time.Now().Add(timeout)

	for {
		res := runCLI(t, deleteArgs...)
		if res.err == nil || res.notFound() {
			break
		}
		// The API rejects a delete while a dependant still exists, and
		// deletion is asynchronous, so a correctly ordered teardown can
		// still arrive a moment early. Retry those rather than
		// stranding the resource.
		if !dependencyStillExists(res) || time.Now().After(deadline) {
			return fmt.Errorf("delete %s: %v\nstderr: %s",
				what, res.err, res.stderr)
		}
		t.Logf("%s: waiting for a dependant to finish deleting: %s",
			what, strings.TrimSpace(res.stderr))
		time.Sleep(15 * time.Second)
	}
	return waitGone(t, what, timeout, getArgs...)
}

// dependencyStillExists reports whether a delete was refused only
// because something that depends on the resource has not finished
// deleting yet.
func dependencyStillExists(res cliResult) bool {
	for _, msg := range []string{
		"cluster has databases",
		"backup store is in use by clusters",
	} {
		if strings.Contains(res.stderr, msg) {
			return true
		}
	}
	return false
}

// bucketNotEmpty reports whether a backup store delete was refused
// because its S3 bucket still holds objects.
//
// This is intended behaviour, not a fault: pgEdge deletes a backup
// store only when it is empty, and never deletes customer backup data
// itself, because those backups may be the last surviving copy.
// Deleting a database therefore leaves its pgbackrest data in place,
// and removing the store is a deliberate two-step operation the
// customer owns — empty the bucket with their own cloud credentials,
// then delete the store.
func bucketNotEmpty(res cliResult) bool {
	return strings.Contains(res.stderr, "bucket is not empty")
}

// storeBucketNames reads the S3 bucket names off a backup store so the
// operator can be told exactly what to empty.
func storeBucketNames(t *testing.T, storeID string) []string {
	t.Helper()
	res := runCLI(t, "byoc", "backup-store", "get", storeID)
	if res.err != nil {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal(res.stdout, &obj) != nil {
		return nil
	}
	props, _ := obj["properties"].(map[string]any)
	var names []string
	for _, key := range []string{"bucket_name", "log_bucket_name"} {
		if name, ok := props[key].(string); ok && name != "" {
			names = append(names, name)
		}
	}
	return names
}

// applyService runs a service deploy or update. A non-zero exit is
// fatal, but a missing JSON body is only recorded: the machine-readable
// output contract is worth asserting live, and losing the rest of an
// expensive run over it is not. A live run found these verbs writing
// nothing at all to stdout under -o json.
func applyService(t *testing.T, what string, args ...string) {
	t.Helper()
	res := runCLI(t, args...)
	if res.err != nil {
		t.Fatalf("%s: %v\nstdout: %s\nstderr: %s",
			what, res.err, truncate(res.stdout), res.stderr)
	}
	if len(res.stdout) == 0 {
		t.Errorf("%s: nothing written to stdout under -o json; a "+
			"caller cannot read back the service id, port or domain",
			what)
		return
	}
	var db map[string]any
	if err := json.Unmarshal(res.stdout, &db); err != nil {
		t.Errorf("%s: stdout is not a JSON object: %v\nbody: %s",
			what, err, truncate(res.stdout))
		return
	}
	if _, ok := db["id"]; !ok {
		t.Errorf("%s: rendered object carries no id: %v", what, db)
	}
}

// serviceState finds a service of the given type on a database and
// returns its state, plus whether it is present at all.
func serviceState(
	t *testing.T, dbID, svcType string,
) (state string, present bool) {
	t.Helper()
	res := runCLI(t, "byoc", "database", "service", "list", dbID)
	if res.err != nil {
		t.Logf("service list: %v\nstderr: %s", res.err, res.stderr)
		return "", false
	}
	var svcs []map[string]any
	if err := json.Unmarshal(res.stdout, &svcs); err != nil {
		t.Logf("service list: decode: %v\nbody: %s",
			err, truncate(res.stdout))
		return "", false
	}
	for _, svc := range svcs {
		if stringField(svc, "service_type") == svcType {
			return stringField(svc, "state"), true
		}
	}
	return "", false
}

// waitForService polls until the named service type reaches a terminal
// state. required decides whether a bad outcome fails the test or is
// only reported: the RAG service cannot come up without a real LLM
// key, so it is optional when none was supplied.
func waitForService(
	t *testing.T, dbID, svcType string, timeout time.Duration,
	required bool,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string

	for {
		state, present := serviceState(t, dbID, svcType)
		if !present {
			if required {
				t.Fatalf("service %s: not present on database %s",
					svcType, dbID)
			}
			t.Errorf("service %s: not present on database %s",
				svcType, dbID)
			return
		}
		if state != last {
			t.Logf("service %s: state %q", svcType, state)
			last = state
		}
		if done, good := terminalStatus(state); done {
			if good {
				return
			}
			if required {
				t.Fatalf("service %s: reached failed state %q",
					svcType, state)
			}
			t.Logf("service %s: state %q — not treated as a failure "+
				"because no real credentials were supplied for it",
				svcType, state)
			return
		}
		if time.Now().After(deadline) {
			msg := fmt.Sprintf("service %s: still %q after %s",
				svcType, last, timeout)
			if required {
				t.Fatal(msg)
			}
			t.Log(msg + " — not treated as a failure")
			return
		}
		time.Sleep(15 * time.Second)
	}
}

// sshPublicKey returns a syntactically valid ed25519 public key. It is
// a throwaway used only to exercise the ssh-key verbs; nothing holds
// the private half, so it grants no access to anything.
func sshPublicKey() string {
	return "ssh-ed25519 " +
		"AAAAC3NzaC1lZDI1NTE5AAAAIJ0Zk8yQ0z9pQ0kPqzMx4Z0Zk8yQ0z9pQ0kPqzMx4Z0Z " +
		"pgedge-cli-integration@example.invalid"
}
