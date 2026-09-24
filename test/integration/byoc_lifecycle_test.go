package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBYOCLifecycle builds a complete BYOC stack on real cloud
// infrastructure, exercises it, and tears the whole thing back down.
//
// The read-only sweep in byoc_read_test.go covers list and get. This
// covers what that cannot: the create, deploy and delete verbs, which
// were the ones most changed by the move to canonical product-scoped
// API paths. Removing the infrastructure is part of the test, not
// cleanup around it — an unusable delete verb is as much a defect as
// an unusable create.
//
// Build-up order (each step depends on the one before it):
//
//	backup store -> ssh key -> cluster -> database -> services -> backup
//
// Tear-down runs in exactly the reverse order, and runs even when a
// step above it failed.
func TestBYOCLifecycle(t *testing.T) {
	requireIntegration(t)
	cfg := requireLifecycle(t)

	td := newTeardown(t)
	// Deferred rather than t.Cleanup so the teardown summary is the
	// last thing the run reports. t.Fatalf unwinds through this.
	defer td.run()

	t.Logf("building in cloud account %s, region %s, instance type %s",
		cfg.cloudAccountID, cfg.region, cfg.instanceType)

	storeID := lifecycleBackupStore(t, cfg, td)
	lifecycleSSHKey(t, cfg, td)
	clusterID := lifecycleCluster(t, cfg, td, storeID)
	dbID := lifecycleDatabase(t, cfg, td, clusterID)
	lifecycleServices(t, cfg, td, dbID)
	lifecycleBackup(t, cfg, td, dbID)

	t.Log("build-up complete; tearing down")
}

// lifecycleBackupStore creates the backup store the cluster needs in
// order to host a database.
func lifecycleBackupStore(
	t *testing.T, cfg lifecycleCfg, td *teardown,
) string {
	t.Helper()

	if cfg.existingStoreID != "" {
		t.Logf("reusing backup store %s from "+
			"PGEDGE_INTEGRATION_BACKUP_STORE_ID; not creating one and "+
			"not removing it", cfg.existingStoreID)
		waitForStatus(t, "backup store "+cfg.existingStoreID,
			cfg.storeTimeout,
			"byoc", "backup-store", "get", cfg.existingStoreID)
		return cfg.existingStoreID
	}

	name := cfg.storeName()
	obj := mustObject(t, "byoc", "backup-store", "create",
		"--name", name,
		"--cloud-account-id", cfg.cloudAccountID,
		"--region", cfg.region)
	id := idOf(t, "backup-store create", obj)
	t.Logf("created backup store %s (%s)", name, id)

	td.add("backup store "+name+" ("+id+")", func() error {
		// Read the bucket names before the delete attempt: if the
		// store survives, the operator needs to know what to empty.
		buckets := storeBucketNames(t, id)

		res := runCLI(t, "byoc", "backup-store", "delete", id, "--force")
		if res.err != nil && !res.notFound() {
			if bucketNotEmpty(res) {
				// Expected: pgEdge deletes a store only when it is
				// empty and never deletes customer backup data, so the
				// database's pgbackrest data keeps the store alive.
				// Not a failure — the delete verb behaved correctly.
				t.Logf("EXPECTED: backup store %s (%s) still holds "+
					"backup data, so it was not deleted. pgEdge never "+
					"deletes customer backup data; removing the store "+
					"is a customer-owned two-step operation.\n"+
					"To finish it, delete every object VERSION in "+
					"s3://%s (region %s) — the bucket is versioned, so "+
					"`aws s3 rm --recursive` alone leaves noncurrent "+
					"versions behind, and DeleteObjects takes at most "+
					"1000 keys per call, so batch them. Then:\n"+
					"  pgedge --profile %s byoc backup-store delete "+
					"%s --force",
					name, id, firstOr(buckets, "<bucket>"),
					cfg.region, profile(), id)
				return errSkipRemoval
			}
			return errorFrom("delete backup store "+id, res)
		}
		return waitGone(t, "backup store "+id, cfg.deleteTimeout,
			"byoc", "backup-store", "get", id)
	})

	waitForStatus(t, "backup store "+id, cfg.storeTimeout,
		"byoc", "backup-store", "get", id)
	return id
}

// firstOr returns the first element, or a fallback for an empty slice.
func firstOr(items []string, fallback string) string {
	if len(items) == 0 {
		return fallback
	}
	return items[0]
}

// lifecycleSSHKey registers a throwaway public key and checks it can be
// read back.
func lifecycleSSHKey(t *testing.T, cfg lifecycleCfg, td *teardown) {
	t.Helper()
	name := cfg.sshKeyName()

	obj := mustObject(t, "byoc", "ssh-key", "create",
		"--name", name, "--public-key", sshPublicKey())
	id := idOf(t, "ssh-key create", obj)
	t.Logf("created ssh key %s (%s)", name, id)

	td.add("ssh key "+name+" ("+id+")", func() error {
		return deleteAndWait(t, "ssh key "+id, cfg.deleteTimeout,
			[]string{"byoc", "ssh-key", "delete", id, "--force"},
			[]string{"byoc", "ssh-key", "get", id})
	})

	got := mustObject(t, "byoc", "ssh-key", "get", id)
	assertNonEmpty(t, "ssh-key get", got, []string{"id", "name"})
	if n := stringField(got, "name"); n != name {
		t.Errorf("ssh-key get: name = %q, want %q", n, name)
	}
}

// lifecycleCluster creates the two-node cluster the database runs on.
// This is the slowest step: it provisions real cloud instances.
func lifecycleCluster(
	t *testing.T, cfg lifecycleCfg, td *teardown, storeID string,
) string {
	t.Helper()
	name := cfg.clusterName()

	nodeSpec := func(nodeName string) string {
		return strings.Join([]string{
			"name=" + nodeName,
			"instance-type=" + cfg.instanceType,
			"volume-size=" + cfg.volumeSize,
		}, ",")
	}

	// Created without --wait deliberately: --wait defaults to a
	// 600-second timeout that a real cluster build can outrun, and
	// polling here gives a much better failure report.
	obj := mustObject(t, "byoc", "cluster", "create",
		"--name", name,
		"--cloud-account-id", cfg.cloudAccountID,
		"--regions", cfg.region,
		"--node-location", "public",
		"--backup-store-id", storeID,
		"--node", nodeSpec("n1"),
		"--node", nodeSpec("n2"),
		"--firewall-rule", "name=postgres,port=5432,sources=0.0.0.0/0")
	id := idOf(t, "cluster create", obj)
	// Provisioning is the long leg. A dev run has been observed
	// reaching available in under four minutes, but the budget stays
	// generous because that is not a promise about any environment.
	t.Logf("created cluster %s (%s); waiting up to %s for provisioning",
		name, id, cfg.clusterTimeout)

	td.add("cluster "+name+" ("+id+")", func() error {
		return deleteAndWait(t, "cluster "+id, cfg.deleteTimeout,
			[]string{"byoc", "cluster", "delete", id, "--force"},
			[]string{"byoc", "cluster", "get", id})
	})

	cluster := waitForStatus(t, "cluster "+id, cfg.clusterTimeout,
		"byoc", "cluster", "get", id)
	assertNonEmpty(t, "cluster get", cluster,
		[]string{"id", "name", "status", "nodes"})

	nodes, _ := cluster["nodes"].([]any)
	if len(nodes) != 2 {
		t.Errorf("cluster get: %d nodes, want 2", len(nodes))
	}

	// node list is the only verb that reads cluster hosts; it is
	// covered read-only elsewhere but never against a cluster this
	// suite created.
	hosts := listOf(t, "byoc", "node", "list", id)
	if len(hosts) != 2 {
		t.Errorf("node list: %d nodes, want 2", len(hosts))
	}
	return id
}

// lifecycleDatabase creates the database the services attach to.
func lifecycleDatabase(
	t *testing.T, cfg lifecycleCfg, td *teardown, clusterID string,
) string {
	t.Helper()
	// Postgres name rules reject hyphens, so databaseName drops them.
	name := cfg.databaseName()

	obj := mustObject(t, "byoc", "database", "create",
		"--name", name,
		"--cluster-id", clusterID,
		"--pg-version", cfg.pgVersion)
	id := idOf(t, "database create", obj)
	t.Logf("created database %s (%s)", name, id)

	td.add("database "+name+" ("+id+")", func() error {
		return deleteAndWait(t, "database "+id, cfg.deleteTimeout,
			[]string{"byoc", "database", "delete", id, "--force"},
			[]string{"byoc", "database", "get", id})
	})

	db := waitForStatus(t, "database "+id, cfg.databaseTimeout,
		"byoc", "database", "get", id)
	assertNonEmpty(t, "database get", db,
		[]string{"id", "name", "status", "cluster_id"})
	if got := stringField(db, "cluster_id"); got != clusterID {
		t.Errorf("database get: cluster_id = %q, want %q",
			got, clusterID)
	}
	return id
}

// lifecycleServices deploys all three service types the API supports
// and checks each one lands on the database.
func lifecycleServices(
	t *testing.T, cfg lifecycleCfg, td *teardown, dbID string,
) {
	t.Helper()

	// Two-node cluster, so the target node is named explicitly rather
	// than relying on single-node auto-selection.
	const targetNode = "n1"

	removeService := func(svcType string) func() error {
		return func() error {
			// A deploy that is still settling leaves the database in
			// "modifying", which the API refuses to edit.
			waitDatabaseModifiable(t, dbID, cfg.serviceTimeout)

			res := runCLI(t, "byoc", "database", "service", "remove",
				dbID, svcType, "--force")
			if res.err != nil && !res.notFound() {
				return errorFrom("remove "+svcType+" service", res)
			}
			if state, present := serviceState(t, dbID, svcType); present {
				t.Logf("%s service still listed after remove "+
					"(state %q); the database delete that follows "+
					"removes it either way", svcType, state)
			}
			return nil
		}
	}

	// --- MCP ---
	applyService(t, "mcp deploy",
		"byoc", "database", "mcp", "deploy", dbID,
		"--target-nodes", targetNode)
	td.addDependent("mcp service on database "+dbID, removeService("mcp"))
	t.Log("deployed MCP service")
	waitForService(t, dbID, "mcp", cfg.serviceTimeout, true)

	// --- RAG ---
	// The RAG service needs LLM credentials. Without a real key the
	// service is still registered — which is what the CLI verb is
	// responsible for — but it cannot be expected to come up, so the
	// state check is advisory in that case.
	// The API requires at least one pipeline, each with at least one
	// table and a name that is neither empty nor the reserved
	// "_default". It does not check that the table exists, so a
	// structurally valid pipeline is enough to exercise the verb on a
	// database with no user tables.
	pipelineConfig := filepath.Join(t.TempDir(), "pipelines.json")
	pipelines := `[{"name":"clitest","tables":[` +
		`{"table":"public.clitest_docs","text_column":"content",` +
		`"vector_column":"embedding"}]}]`
	if err := os.WriteFile(
		pipelineConfig, []byte(pipelines), 0o600); err != nil {
		t.Fatalf("write pipeline config: %v", err)
	}
	llmKey := cfg.llmAPIKey
	haveRealKey := llmKey != ""
	if !haveRealKey {
		llmKey = "sk-integration-placeholder-no-real-credential"
		t.Log("PGEDGE_INTEGRATION_LLM_API_KEY is unset: deploying RAG " +
			"with a placeholder key. The deploy verb is still tested; " +
			"the service reaching a running state is not required.")
	}

	applyService(t, "rag deploy",
		"byoc", "database", "rag", "deploy", dbID,
		"--target-nodes", targetNode,
		"--embedding-llm-provider", "openai",
		"--embedding-llm-model", "text-embedding-3-small",
		"--embedding-llm-api-key", llmKey,
		"--completion-llm-provider", "openai",
		"--completion-llm-model", "gpt-4o",
		"--completion-llm-api-key", llmKey,
		"--pipeline-config", pipelineConfig)
	td.addDependent("rag service on database "+dbID, removeService("rag"))
	t.Log("deployed RAG service")
	waitForService(t, dbID, "rag", cfg.serviceTimeout, haveRealKey)

	// --- PostgREST ---
	// app_read_only is one of the database's built-in roles, so it is
	// guaranteed to exist and is the right privilege level for
	// unauthenticated REST callers.
	applyService(t, "postgrest deploy",
		"byoc", "database", "postgrest", "deploy", dbID,
		"--target-nodes", targetNode,
		"--db-schemas", "public",
		"--db-anon-role", "app_read_only",
		"--max-rows", "1000")
	td.addDependent("postgrest service on database "+dbID,
		removeService("postgrest"))
	t.Log("deployed PostgREST service")
	waitForService(t, dbID, "postgrest", cfg.serviceTimeout, true)

	// An update on the deployed service must preserve the required
	// fields it did not pass.
	// Deliberately without --target-nodes: an update must preserve the
	// placement the deploy chose. Before that fix this failed with
	// "cluster has 2 nodes (n1, n2) — specify --target-nodes".
	waitDatabaseModifiable(t, dbID, cfg.serviceTimeout)
	applyService(t, "postgrest update",
		"byoc", "database", "postgrest", "update", dbID,
		"--max-rows", "500")
	t.Log("updated PostgREST service")

	svcs := listOf(t, "byoc", "database", "service", "list", dbID)
	found := map[string]bool{}
	for _, svc := range svcs {
		found[stringField(svc, "service_type")] = true
	}
	for _, want := range []string{"mcp", "rag", "postgrest"} {
		if !found[want] {
			t.Errorf("service list: %s service missing after deploy "+
				"(got %v)", want, found)
		}
	}

	// Every service the API reports must be readable individually.
	for _, svc := range svcs {
		svcID := stringField(svc, "service_id")
		if svcID == "" {
			t.Errorf("service list: entry without service_id: %v", svc)
			continue
		}
		got := mustObject(t, "byoc", "database", "service", "get",
			dbID, svcID)
		assertNonEmpty(t, "service get "+svcID, got,
			[]string{"service_id", "service_type"})
	}
}

// lifecycleBackup takes a backup of the database. It cannot read one
// back: the BYOC API's backup read, delete and download endpoints all
// lived on the retired bare /v1 surface and have no namespaced
// equivalent, so the CLI ships only `backup create`. What this exercises
// is therefore the whole of the CLI's backup surface.
func lifecycleBackup(
	t *testing.T, cfg lifecycleCfg, td *teardown, dbID string,
) {
	t.Helper()

	waitDatabaseModifiable(t, dbID, cfg.serviceTimeout)
	res := runCLI(t, "byoc", "backup", "create",
		"--database-id", dbID, "--provider", "pgbackrest")
	if res.err != nil {
		t.Fatalf("backup create: %v\nstderr: %s", res.err, res.stderr)
	}
	if len(res.stdout) != 0 {
		t.Errorf("backup create wrote to stdout (%s); the API answers "+
			"with an empty body, so stdout must stay empty in every "+
			"output mode", truncate(res.stdout))
	}

	// No assertion follows, and none can. The old `/v1/backups*`
	// endpoints were legacy Developer-edition surface that BYOC never
	// used; list, get and restore now live at `/managed/v1/backups*`
	// and there is no download operation. None of them is a byoc
	// endpoint, so there is no client-visible signal that a BYOC
	// backup materialised: no id in the response, no API task to poll
	// (the task is created in the Control Plane, not in the API's task
	// system), and no row the CLI can read.
	//
	// The managed backup listing now exists, so the read-back half of
	// this phase belongs in a managed lifecycle test rather than here —
	// it cannot read a BYOC backup either way. The removed verbs are
	// banked on the parked/byoc-backup-commands branch.
	t.Log("note: backup create is the CLI's entire backup surface; " +
		"there is no verb to read, download, delete or restore a " +
		"backup, so this phase asserts the request only")
}

// errorFrom renders a failed CLI result as an error for teardown
// reporting.
func errorFrom(what string, res cliResult) error {
	return &cliError{what: what, res: res}
}

type cliError struct {
	what string
	res  cliResult
}

func (e *cliError) Error() string {
	return e.what + ": " + e.res.err.Error() +
		"\nstderr: " + e.res.stderr
}
