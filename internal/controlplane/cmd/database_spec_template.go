package cmd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// specValues carries interview answers into buildSpec. Zero/empty
// fields render as annotated commented stubs.
type specValues struct {
	databaseName string
	nodes        []nodeValue    // len 0 => template default n1..nN
	adminUser    string         // "" => "admin"
	port         string         // "" => commented
	backup       *backupValue   // nil => annotated stub
	services     []serviceValue // len 0 => corrected annotated stub
	restore      *restoreValue  // nil => corrected annotated stub
	// orchestrator is what 'database init' detected before building
	// this spec (see detectOrchestrator). Its zero value is the
	// undetected/fallback case, which is also what every caller that
	// pre-dates orchestrator detection (tests, buildSpec(specValues{}))
	// gets for free: the safe, annotated generic template.
	orchestrator detectionResult
}

// secretSentinel is the placeholder written in place of a required
// secret (e.g. an mcp provider API key). It is never a real
// credential. The writer emits it uncommented so 'database create'
// can detect an unfilled secret and refuse the spec (see
// checkUnfilledPlaceholders). Defined once here so writer and
// validator cannot drift.
const secretSentinel = "CHANGE-ME"

// serviceValue is one populated service block. mcp is the only service
// type the interview writes real config into; config/secretKey/
// initToken are only ever populated for mcp, per internal/controlplane/svccfg's
// mirror of Control Plane's per-service key sets. rag and postgrest
// have real, source-derived config shapes too (see
// serviceConfigGuidance), but their trees are too deep for a prompt
// loop, so an interviewed rag/postgrest service renders the same
// commented per-type guidance block as the blank template -- literally
// the same text, from the same source.
type serviceValue struct {
	serviceType string // mcp|postgrest|rag; "" => mcp
	serviceID   string
	connectAs   string
	hostIDs     []string // empty => [CHANGE-ME]
	version     string   // "" => latest
	config      map[string]string
	// secretKey is the provider-specific mcp credential key the
	// interview resolved from the chosen llm_provider ("" => none
	// needed): anthropic_api_key, openai_api_key, or ollama_url. CP
	// requires exactly the one matching the provider once
	// llm_enabled is true (mcp_service_config.go:157-178 @ v0.10.0),
	// so this replaces a generic "needs a key?" yes/no with the key
	// CP will actually look for.
	secretKey string
	// initToken emits mcp's init_token: CHANGE-ME sentinel when true.
	// Independent of llm_enabled -- init_token is CP's bootstrap-only
	// security field (mcp_service_config.go:109-116 @ v0.10.0),
	// rejected on update, accepted on create.
	initToken bool
}

// nodeValue is one node block. An empty name renders as nX (1-based);
// empty hostIDs render as the [CHANGE-ME] placeholder. The remaining
// fields are optional per-node overrides of the cluster-wide values;
// each renders only when set.
type nodeValue struct {
	name            string
	hostIDs         []string          // empty => [CHANGE-ME]
	port            string            // "" => not rendered
	postgresVersion string            // "" => not rendered
	postgresqlConf  map[string]string // len 0 => not rendered
	pgHbaConf       []string          // len 0 => not rendered
}

// backupValue is a populated backup_config; when nil on specValues,
// buildSpec emits the annotated backup stub instead.
type backupValue struct {
	repoType string            // s3|gcs|azure|posix|cifs
	fields   map[string]string // e.g. s3_bucket, s3_region, base_path
	schedule *scheduleValue    // nil => commented schedules stub
}

// scheduleValue is one backup schedule entry.
type scheduleValue struct {
	cron string
	typ  string // full|incr
	id   string
}

// restoreValue is a populated restore_config. When nil on specValues,
// buildSpec emits the corrected restore_config stub instead. Restore
// carries no required secret: repository credentials fall back to the
// instance credential chain (same as backup), so no secretSentinel is
// involved. Interactive interviews re-prompt blank required source_*
// fields (promptRequired), so a live wizard never leaves them empty;
// the CHANGE-ME rendering below (orChangeMe) is a defensive fallback for
// a restoreValue constructed directly with empty source_* fields, and
// checkUnfilledRestorePlaceholders refuses the result.
type restoreValue struct {
	repoType       string            // s3|gcs|azure|posix|cifs; "" => s3
	repoFields     map[string]string // s3_bucket, s3_region, base_path…
	sourceDBID     string            // "" => CHANGE-ME (required)
	sourceDBName   string            // "" => CHANGE-ME (required)
	sourceNodeName string            // "" => CHANGE-ME (required)
	restoreOptions map[string]string // PITR {type,target}; empty => omit
}

// defaultNodeCount is used when specValues carries no nodes.
const defaultNodeCount = 3

// quoteYAML double-quotes s (escaping " and \) when a bare scalar
// would be misparsed by YAML, and returns it unchanged otherwise.
// Interview answers are free text and may legitimately contain
// YAML-significant characters (e.g. a database name "prod: east" or
// a base_path with a trailing colon); emitting those bare produces
// invalid or silently-wrong YAML.
func quoteYAML(s string) string {
	if needsYAMLQuote(s) {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return `"` + s + `"`
	}
	return s
}

// needsYAMLQuote reports whether s would be misparsed (or rejected)
// as a bare YAML scalar.
func needsYAMLQuote(s string) bool {
	if s == "" {
		return true
	}
	switch strings.ToLower(s) {
	case "null", "~", "true", "false":
		return true
	}
	if strings.TrimSpace(s) != s {
		return true
	}
	if strings.Contains(s, ": ") || strings.HasSuffix(s, ":") {
		return true
	}
	if strings.Contains(s, " #") {
		return true
	}
	if strings.ContainsAny(s, "\n\t") {
		return true
	}
	if strings.ContainsAny(s[:1], "!&*?|>%@`\"'#,[]{}") {
		return true
	}
	if (s[0] == '-' || s[0] == ':' || s[0] == '?') &&
		len(s) > 1 && s[1] == ' ' {
		return true
	}
	return false
}

// quoteYAMLFlow is quoteYAML for scalars rendered inside a YAML flow
// collection (e.g. host_ids: [a, b]). In flow context "[]{}," are
// structurally significant anywhere in the scalar, not just when
// leading, so an element such as host[1] must be quoted even though
// quoteYAML (block context) would leave it bare.
func quoteYAMLFlow(s string) string {
	if needsYAMLQuote(s) || strings.ContainsAny(s, "[]{},") {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return `"` + s + `"`
	}
	return s
}

// buildSpec assembles a complete DatabaseSpec YAML document. Populated
// sections come from v; everything not (yet) interviewed renders as an
// inert, fully annotated commented stub so the output stays valid and
// create-consumable while documenting every advanced field.
func buildSpec(v specValues) string {
	var b strings.Builder
	writeHeader(&b, v)
	writeIdentity(&b, v)
	writeDatabaseUsers(&b, v)
	writeNodes(&b, v)
	writeBackupConfig(&b, v)
	writeRestoreConfig(&b, v)
	writeServices(&b, v)
	writePostgresqlConf(&b)
	writePgHbaConf(&b)
	writeNodeOverrides(&b)
	return b.String()
}

// writeHeader emits the edit-note preamble and doc pointer, plus --
// when 'database init' detected a single-orchestrator Control Plane --
// a provenance note naming what it found and why the template below is
// shaped the way it is. The undetected/fallback case (v.orchestrator's
// zero value) adds nothing here; its guidance lives inline on the
// port/patroni_port fields themselves (writeIdentity).
func writeHeader(b *strings.Builder, v specValues) {
	b.WriteString(`# pgEdge Control Plane database spec.
# Edit this file, then create the database with:
#   pgedge controlplane database create <id> -f this-file.yaml
#
# Only database_name and nodes are required. Advanced fields below are
# commented out; uncomment and edit the ones you need. Full field
# reference (DatabaseSpec):
#   https://github.com/pgEdge/control-plane -> DatabaseSpec
`)
	switch v.orchestrator.orchestrator {
	case "systemd":
		fmt.Fprintf(b, `#
# Generated for a systemd Control Plane (detected at
# %s: %d host(s), orchestrator %q).
# systemd requires port and patroni_port on every node. The values
# below are distinct per node so that nodes sharing a host cannot
# collide — adjust them to your environment.
`, v.orchestrator.baseURL, v.orchestrator.hostCount,
			v.orchestrator.orchestrator)
	case "swarm":
		fmt.Fprintf(b, `#
# Generated for a Docker Swarm Control Plane (detected at
# %s).
`, v.orchestrator.baseURL)
	}
}

// writeIdentity emits database_name plus port / postgres_version.
// port is populated when set on v (an interviewed value always wins);
// under a detected systemd Control Plane it ALSO seeds the per-node
// allocation in planSystemdPorts, so node 1 carries the same value
// rather than the top-level answer being shadowed by an allocated
// default. Otherwise its commented guidance depends on what 'database init'
// detected: a systemd Control Plane leaves it as today's plain stub
// (the real values live per-node, see writeNodes); a swarm one adds an
// inline note that the field is optional there; the undetected/
// fallback case -- including every caller that predates detection --
// gets the prominent REQUIRED-on-systemd guidance for both port and
// patroni_port, since that is the one case where 'database init'
// cannot tell the user what their cluster needs.
func writeIdentity(b *strings.Builder, v specValues) {
	name := v.databaseName
	if name == "" {
		name = "my-database"
	}
	fmt.Fprintf(b, "database_name: %s\n", quoteYAML(name))
	b.WriteString("# postgres_version: \"17.6\"\n")
	switch {
	case v.port != "":
		fmt.Fprintf(b, "port: %s\n", v.port)
	case v.orchestrator.orchestrator == "swarm":
		b.WriteString(
			"# port: 5432   # optional on swarm; omit to leave the " +
				"database unexposed\n")
	case v.orchestrator.orchestrator == "systemd":
		b.WriteString("# port: 5432\n")
	default:
		b.WriteString(
			"# port: 5432          # REQUIRED on systemd, optional " +
				"on swarm\n")
		b.WriteString(
			"# patroni_port: 8008  # REQUIRED on systemd, ignored " +
				"on Docker Swarm\n")
		b.WriteString(
			"#   On systemd set BOTH on every node. Nodes sharing " +
				"a host need\n")
		b.WriteString(
			"#   distinct values (ports are unique per host).\n")
	}
}

// writeDatabaseUsers emits a single admin user block.
func writeDatabaseUsers(b *strings.Builder, v specValues) {
	user := v.adminUser
	if user == "" {
		user = "admin"
	}
	b.WriteString("database_users:\n")
	fmt.Fprintf(b, "  - username: %s\n", quoteYAML(user))
	fmt.Fprintf(b, "    password: %s\n", secretSentinel)
	b.WriteString("    db_owner: true\n")
	b.WriteString("    attributes: [LOGIN, SUPERUSER]\n")
}

// writeNodes emits one block per node. With no interviewed nodes it
// falls back to n1..nN (N = len or defaultNodeCount) with CHANGE-ME
// host placeholders. When v.orchestrator detected systemd, each node
// additionally gets an uncommented port/patroni_port pair (see
// planSystemdPorts) -- systemd requires both, and the per-host
// uniqueness rule (validateUniquePorts on the Control Plane) means a
// single shared value would collide the moment every node's still-
// placeholder host_ids are filled in with one host.
func writeNodes(b *strings.Builder, v specValues) {
	count := len(v.nodes)
	if count == 0 {
		count = defaultNodeCount
	}
	b.WriteString("nodes:\n")
	systemd := v.orchestrator.orchestrator == "systemd"
	var ports []systemdNodePorts
	if systemd {
		ports = planSystemdPorts(v, count)
	}
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("n%d", i+1)
		hosts := "[" + secretSentinel + "]"
		hasOverride := i < len(v.nodes)
		var override nodeValue
		if hasOverride {
			override = v.nodes[i]
			if override.name != "" {
				name = override.name
			}
			if len(override.hostIDs) > 0 {
				quoted := make([]string, len(override.hostIDs))
				for j, h := range override.hostIDs {
					quoted[j] = quoteYAMLFlow(h)
				}
				hosts = "[" + strings.Join(quoted, ", ") + "]"
			}
		}
		fmt.Fprintf(b, "  - name: %s\n", quoteYAML(name))
		fmt.Fprintf(b, "    host_ids: %s\n", hosts)
		if systemd {
			writeSystemdNodePorts(b, ports[i])
		}
		if hasOverride {
			writeNodeOverrideFields(b, override)
		}
	}
}

// systemdNodePorts is one node's resolved port/patroni_port pair under
// a detected systemd Control Plane. A zero port means the node already
// carries an explicit interviewed override, which
// writeNodeOverrideFields renders instead — so writeSystemdNodePorts
// must not emit a second, conflicting `port:` key for it.
type systemdNodePorts struct {
	port        int
	patroniPort int
}

// Bases for the systemd per-node port allocation. The port base is only
// a default: an interviewed cluster-wide port (specValues.port) seeds
// the sequence instead, so the answer the user actually gave is what
// node 1 gets rather than being silently shadowed by 5432.
const (
	systemdPortBase        = 5432
	systemdPatroniPortBase = 8008
)

// planSystemdPorts resolves the port/patroni_port pair for each of
// count nodes under a detected systemd Control Plane, which requires
// both on every node.
//
// The allocation is contiguous from a base and, critically, DISTINCT
// across every value the spec ends up carrying: the Control Plane's
// validateUniquePorts keys port and patroni_port uniqueness per host,
// and `init` cannot know the node→host mapping (every host_ids is still
// a CHANGE-ME placeholder), so values that are distinct in every
// topology are the only collision-free choice. Two things make that
// non-trivial, and both are handled here rather than in the writer:
//
//   - an interviewed cluster-wide port (v.port) seeds the port
//     sequence, so `init -i` answering 6000 yields 6000, 6001, 6002…
//     instead of the interviewed value being written at the top level
//     and then shadowed by an allocated 5432 on every node;
//   - an interviewed PER-NODE port (v.nodes[i].port) is honoured as-is
//     and its value is claimed up front, so the allocation walks past
//     it. Without that, answering 5432 for node 2 would collide with
//     node 1's allocated 5432 while the template's own header promised
//     distinct values.
//
// The patroni sequence starts at its own base but shares the claimed
// set, so it can never land on a port already allocated above — which
// an interviewed port near 8008 would otherwise make possible.
func planSystemdPorts(v specValues, count int) []systemdNodePorts {
	claimed := map[int]bool{}
	explicit := make([]bool, count)
	for i := 0; i < count && i < len(v.nodes); i++ {
		if v.nodes[i].port == "" {
			continue
		}
		explicit[i] = true
		if p, err := strconv.Atoi(v.nodes[i].port); err == nil {
			claimed[p] = true
		}
	}
	cursor := systemdPortBase
	if p, err := strconv.Atoi(v.port); err == nil && p > 0 {
		cursor = p
	}
	next := func() int {
		for claimed[cursor] {
			cursor++
		}
		p := cursor
		claimed[p] = true
		cursor++
		return p
	}
	out := make([]systemdNodePorts, count)
	for i := range out {
		if !explicit[i] {
			out[i].port = next()
		}
	}
	cursor = systemdPatroniPortBase
	for i := range out {
		out[i].patroniPort = next()
	}
	return out
}

// writeSystemdNodePorts emits one node's port/patroni_port pair as
// planned by planSystemdPorts. A zero port is skipped: the node has an
// explicit interviewed override that writeNodeOverrideFields renders
// immediately after this call. patroni_port has no interview-collected
// override, so it always renders here.
func writeSystemdNodePorts(b *strings.Builder, p systemdNodePorts) {
	if p.port != 0 {
		fmt.Fprintf(b, "    port: %d\n", p.port)
	}
	fmt.Fprintf(b, "    patroni_port: %d\n", p.patroniPort)
}

// writeNodeOverrideFields emits any populated per-node overrides inline
// under a node block (4-space indent, matching name/host_ids). Each
// field renders only when set, so a node with no overrides is
// unchanged. postgresql_conf/pg_hba_conf values use quoteYAML; port is
// emitted bare, mirroring writeIdentity; postgres_version is always
// double-quoted (like writeIdentity's "17.6") because a bare
// major.minor value such as 16.4 would parse as a YAML float and fail
// to decode into the string field.
func writeNodeOverrideFields(b *strings.Builder, n nodeValue) {
	if n.port != "" {
		fmt.Fprintf(b, "    port: %s\n", n.port)
	}
	if n.postgresVersion != "" {
		fmt.Fprintf(b, "    postgres_version: %q\n", n.postgresVersion)
	}
	if len(n.postgresqlConf) > 0 {
		b.WriteString("    postgresql_conf:\n")
		keys := make([]string, 0, len(n.postgresqlConf))
		for k := range n.postgresqlConf {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(b, "      %s: %s\n", k,
				quoteYAML(n.postgresqlConf[k]))
		}
	}
	if len(n.pgHbaConf) > 0 {
		b.WriteString("    pg_hba_conf:\n")
		for _, rule := range n.pgHbaConf {
			fmt.Fprintf(b, "      - %s\n", quoteYAML(rule))
		}
	}
}

// writeBackupConfig emits a populated backup_config when v.backup is
// set, otherwise a fully annotated stub covering every repo type.
func writeBackupConfig(b *strings.Builder, v specValues) {
	if v.backup != nil {
		writePopulatedBackup(b, v.backup)
		return
	}
	b.WriteString(`# backup_config:
#   repositories:
#     # type is one of: s3, gcs, azure, posix, cifs.
#     - type: s3
#       id: repo1
#       # --- s3 fields ---
#       s3_bucket: my-bucket
#       s3_region: us-east-1
#       s3_endpoint: s3.amazonaws.com
#       # s3_key / s3_key_secret: omit to use the instance's
#       # credential chain (IAM role / workload identity).
#       s3_key: CHANGE-ME
#       s3_key_secret: CHANGE-ME
#       # --- gcs fields ---
#       gcs_bucket: my-bucket
#       gcs_endpoint: storage.googleapis.com
#       # gcs_key: omit to use the instance's credential chain.
#       gcs_key: CHANGE-ME
#       # --- azure fields ---
#       azure_account: myaccount
#       azure_container: my-container
#       azure_endpoint: blob.core.windows.net
#       # azure_key: omit to use the instance's credential chain.
#       azure_key: CHANGE-ME
#       # --- posix / cifs fields ---
#       base_path: /var/lib/pgbackrest
#       # custom_options: extra pgBackRest options, key: value.
#       custom_options: {}
#       # retention_full_type is one of: count, time.
#       retention_full: 2
#       retention_full_type: count
#   # schedules: one entry per backup cadence.
#   schedules:
#     # type is one of: full, incr.
#     - id: daily-full
#       cron_expression: "0 1 * * *"
#       type: full
#     - id: hourly-incr
#       cron_expression: "0 * * * *"
#       type: incr
`)
}

// writePopulatedBackup renders an interviewed backup_config block. It
// never emits a real credential: interviewBackupFields (database_init.go)
// never asks for s3_key/s3_key_secret, gcs_key, or azure_key, and this
// function only documents them as a commented note.
func writePopulatedBackup(b *strings.Builder, bk *backupValue) {
	b.WriteString("backup_config:\n")
	b.WriteString("  repositories:\n")
	fmt.Fprintf(b, "    - type: %s\n", bk.repoType)
	b.WriteString("      id: repo1\n")
	keys := make([]string, 0, len(bk.fields))
	for k := range bk.fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "      %s: %s\n", k, quoteYAML(bk.fields[k]))
	}
	b.WriteString("      # credentials: omit to use the instance " +
		"credential chain, or set\n")
	b.WriteString("      # s3_key/s3_key_secret, gcs_key, or " +
		"azure_key here.\n")
	if bk.schedule != nil {
		b.WriteString("  schedules:\n")
		fmt.Fprintf(b, "    - id: %s\n", bk.schedule.id)
		fmt.Fprintf(b, "      cron_expression: \"%s\"\n", bk.schedule.cron)
		fmt.Fprintf(b, "      type: %s\n", bk.schedule.typ)
	}
}

// writeRestoreConfig emits a populated restore_config when v.restore is
// set, otherwise the corrected annotated stub.
func writeRestoreConfig(b *strings.Builder, v specValues) {
	if v.restore != nil {
		writePopulatedRestore(b, v.restore)
		return
	}
	writeRestoreConfigStub(b)
}

// writeRestoreConfigStub emits the fully-commented restore_config
// guidance block (nested repository, the required source_* fields, and
// PITR under restore_options). Shared by the DatabaseSpec template and
// the standalone restore template so the guidance cannot drift.
func writeRestoreConfigStub(b *strings.Builder) {
	b.WriteString(`# restore_config: create this database by restoring an existing
#   backup repository instead of starting empty. repository,
#   source_database_id, source_database_name, and source_node_name are
#   all required. See DatabaseSpec.restore_config.
#   repository:
#     # type is one of: s3, gcs, azure, posix, cifs. The repository
#     # takes the same fields as a backup_config repository; see
#     # DatabaseSpec.backup_config.repositories for the full per-type
#     # field list.
#     type: s3
#     id: repo1
#     s3_bucket: my-bucket
#     s3_region: us-east-1
#     # credentials: omit to use the instance credential chain, or set
#     # s3_key/s3_key_secret, gcs_key, or azure_key here.
#   source_database_id: source-db
#   source_database_name: northwind
#   source_node_name: n1
#   # restore_options: optional point-in-time recovery target. Omit to
#   #   restore to the latest point. type is one of: time, lsn.
#   restore_options:
#     type: time
#     target: "2026-01-01 00:00:00+00"
`)
}

// writePopulatedRestore renders an interviewed restore_config block. It
// never emits a real credential: interviewRepoFields never asks for
// s3_key/s3_key_secret, gcs_key, or azure_key, and this function only
// documents them as a commented note. Blank required source_* fields
// render as the CHANGE-ME placeholder.
func writePopulatedRestore(b *strings.Builder, r *restoreValue) {
	repoType := r.repoType
	if repoType == "" {
		repoType = "s3"
	}
	b.WriteString("restore_config:\n")
	b.WriteString("  repository:\n")
	fmt.Fprintf(b, "    type: %s\n", repoType)
	b.WriteString("    id: repo1\n")
	keys := make([]string, 0, len(r.repoFields))
	for k := range r.repoFields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "    %s: %s\n", k, quoteYAML(r.repoFields[k]))
	}
	b.WriteString("    # credentials: omit to use the instance " +
		"credential chain, or set\n")
	b.WriteString("    # s3_key/s3_key_secret, gcs_key, or azure_key " +
		"here.\n")
	fmt.Fprintf(b, "  source_database_id: %s\n",
		quoteYAML(orChangeMe(r.sourceDBID)))
	fmt.Fprintf(b, "  source_database_name: %s\n",
		quoteYAML(orChangeMe(r.sourceDBName)))
	fmt.Fprintf(b, "  source_node_name: %s\n",
		quoteYAML(orChangeMe(r.sourceNodeName)))
	if len(r.restoreOptions) > 0 {
		b.WriteString("  restore_options:\n")
		okeys := make([]string, 0, len(r.restoreOptions))
		for k := range r.restoreOptions {
			okeys = append(okeys, k)
		}
		sort.Strings(okeys)
		for _, k := range okeys {
			fmt.Fprintf(b, "    %s: %s\n", k,
				quoteYAML(r.restoreOptions[k]))
		}
	}
}

// orChangeMe returns the CHANGE-ME placeholder for a blank required
// value so the emitted block stays valid, round-trippable YAML that
// clearly flags what the user must fill in (mirrors the [CHANGE-ME]
// host convention in writeNodes). It is not a secret;
// checkUnfilledRestorePlaceholders refuses it on create, update and
// restore.
func orChangeMe(s string) string {
	if s == "" {
		return "CHANGE-ME"
	}
	return s
}

// serviceStubTypes is the order the blank template's service examples
// are emitted in, and the set of types serviceConfigGuidance covers.
// It matches the service_type enum the cobra tree accepts.
var serviceStubTypes = []string{"mcp", "rag", "postgrest"}

// writeServices emits populated service blocks when v.services is set,
// otherwise a corrected annotated stub covering all three service
// types (valid enum, required service_id, and
// each type's own real config shape instead of one mcp-flavoured
// guess). The per-type config guidance comes from the single
// serviceConfigGuidance source, so it cannot drift from what a
// populated service block renders. See internal/controlplane/svccfg for the key
// sets this block is gated against
// (TestInitTemplateNamesOnlyKnownServiceKeys and
// TestInitTemplateDocumentsEveryRequiredKey in internal/clitest).
func writeServices(b *strings.Builder, v specValues) {
	if len(v.services) == 0 {
		b.WriteString(`# services: extra services to run alongside Postgres.
#   service_type is one of: mcp, postgrest, rag. Each block below
#   shows that type's own config shape -- copy the one you need.
`)
		for _, styp := range serviceStubTypes {
			b.WriteString("#\n")
			fmt.Fprintf(b, "#   - service_type: %s\n", styp)
			fmt.Fprintf(b, "#     service_id: %s-1\n", styp)
			b.WriteString("#     connect_as: admin\n")
			b.WriteString("#     host_ids: [CHANGE-ME]\n")
			b.WriteString("#     version: latest\n")
			writeServiceConfigGuidance(b, styp, "#     ", "")
		}
		return
	}
	b.WriteString("services:\n")
	for _, s := range v.services {
		writeService(b, s)
	}
}

// writeService renders one populated service block. All five required
// ServiceSpec2 fields are always emitted so the block decodes into a
// complete service.
func writeService(b *strings.Builder, s serviceValue) {
	styp := s.serviceType
	if styp == "" {
		styp = "mcp"
	}
	// Normalize so writeServiceConfig gates on the resolved type, not
	// the raw (possibly empty) one -- otherwise an empty serviceType
	// renders as mcp yet skips the config/secret block, silently
	// defeating create's fail-fast guard.
	s.serviceType = styp
	version := s.version
	if version == "" {
		version = "latest"
	}
	hosts := "[CHANGE-ME]"
	if len(s.hostIDs) > 0 {
		quoted := make([]string, len(s.hostIDs))
		for i, h := range s.hostIDs {
			quoted[i] = quoteYAMLFlow(h)
		}
		hosts = "[" + strings.Join(quoted, ", ") + "]"
	}
	fmt.Fprintf(b, "  - service_type: %s\n", quoteYAML(styp))
	fmt.Fprintf(b, "    service_id: %s\n", quoteYAML(s.serviceID))
	fmt.Fprintf(b, "    connect_as: %s\n", quoteYAML(s.connectAs))
	fmt.Fprintf(b, "    host_ids: %s\n", hosts)
	fmt.Fprintf(b, "    version: %s\n", quoteYAML(version))
	writeServiceConfig(b, s)
}

// writeServiceConfig emits a populated mcp config block (plus the
// uncommented secret sentinel(s) for whatever the interview resolved
// -- the provider-specific credential key, init_token, or both) or,
// for every other case (mcp with nothing populated, or any
// rag/postgrest service, populated or not), the commented per-type
// config guidance. It never emits a real credential: writeServiceConfig
// only ever writes secretSentinel, never a value interviewMCPConfig
// collected from the user.
func writeServiceConfig(b *strings.Builder, s serviceValue) {
	if s.serviceType == "mcp" &&
		(len(s.config) > 0 || s.secretKey != "" || s.initToken) {
		b.WriteString("    config:\n")
		keys := make([]string, 0, len(s.config))
		for k := range s.config {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(b, "      %s: %s\n", k,
				renderServiceConfigValue(s.config[k]))
		}
		if s.secretKey != "" {
			fmt.Fprintf(b, "      %s: %s\n", s.secretKey, secretSentinel)
		}
		if s.initToken {
			fmt.Fprintf(b, "      init_token: %s\n", secretSentinel)
		}
		if s.secretKey != "" || s.initToken {
			b.WriteString("    # REQUIRED BEFORE CREATE -- replace the " +
				"CHANGE-ME value(s) above;\n")
			b.WriteString("    # create will refuse this spec until you do.\n")
		}
		return
	}
	writeServiceConfigGuidance(b, s.serviceType, "    ", "# ")
}

// renderServiceConfigValue renders one mcp config value collected by
// the interview. Unlike quoteYAML (used elsewhere for free-text
// interview answers), the boolean literal "true" written by
// interviewMCPConfig for llm_enabled must stay UNQUOTED so it decodes
// as a bool rather than the string "true" -- CP's mcp parser
// type-asserts config values (optionalBool et al. in
// mcp_service_config.go) and rejects a quoted boolean as the wrong
// type. Every other value (provider names, model names) is free text
// and goes through quoteYAML as usual.
func renderServiceConfigValue(v string) string {
	switch v {
	case "true", "false":
		return v
	}
	return quoteYAML(v)
}

// serviceConfigGuidance is the ONE copy of the per-service-type config
// guidance the template emits, keyed by service_type. It is rendered at
// two different indents -- inside the blank template's fully-commented
// example (writeServices) and under a live, populated service block
// (writeServiceConfig) -- by writeServiceConfigGuidance, so the two
// surfaces cannot drift. Separately maintained copies had diverged
// (the populated mcp copy showed 5 of the blank copy's 18 keys), with
// only the blank copy gated; single-sourcing puts both under
// TestInitTemplateNamesOnlyKnownServiceKeys and
// TestInitTemplateDocumentsEveryRequiredKey.
//
// Format: a leading "# " marks a line that is already a comment in
// either rendering (the verdict on config: {} and the inline asides);
// every other line is YAML, indented relative to the service block's
// config key. Lines are kept to 73 columns so that the 6-column prefix
// either rendering adds keeps the emitted template inside 79 columns.
var serviceConfigGuidance = map[string]string{
	"mcp": `# config: {} is valid for mcp -- every key below is optional.
config:
  llm_enabled: true             # gates the whole LLM block below
  # --- only when llm_enabled: true ---
  llm_provider: anthropic       # REQUIRED: anthropic|openai|ollama
  llm_model: claude-sonnet-4-5  # REQUIRED
  anthropic_api_key: CHANGE-ME  # required for provider anthropic
  # openai -> openai_api_key (required for provider openai)
  # ollama -> ollama_url      (required for provider ollama)
  llm_temperature: 0.2          # 0.0-2.0
  llm_max_tokens: 4096          # > 0
  # --- end llm_enabled block ---
  init_token: CHANGE-ME         # create-time only; update rejects it
  allow_writes: false           # allow write queries; default false
  pool_max_conns: 10            # > 0
  embedding_provider: voyage    # voyage|openai|ollama
  embedding_model: voyage-3     # required with embedding_provider
  embedding_api_key: CHANGE-ME  # unless embedding_provider is ollama
  # --- only when kb_enabled: true ---
  kb_enabled: true              # gates the kb_* block below
  kb_embedding_provider: voyage # REQUIRED: voyage|openai (no ollama)
  kb_embedding_model: voyage-3  # REQUIRED
  kb_embedding_api_key: CHANGE-ME
  kb_database_host_path: /var/lib/pgedge/kb   # absolute, clean path
  # --- end kb_enabled block ---
  disable_query_database: false # 1 of 7 disable_* toggles; default false
`,
	"rag": `# config: {} is a guaranteed 400 for rag -- pipelines is REQUIRED.
config:
  pipelines:                    # REQUIRED, >= 1
    - name: docs                # ^[a-z0-9_][a-z0-9_-]*$, unique
      tables:                   # REQUIRED, >= 1
        - table: documents
          text_column: body
          vector_column: embedding
      embedding_llm:            # REQUIRED
        provider: voyage        # anthropic|openai|voyage|ollama
        model: voyage-3
        api_key: CHANGE-ME      # required unless provider is ollama
      rag_llm:                  # REQUIRED
        provider: anthropic     # anthropic|openai|ollama (no voyage)
        model: claude-sonnet-4-5
        api_key: CHANGE-ME      # required unless provider is ollama
      token_budget: 8000        # optional, > 0
      top_n: 10                 # optional, > 0
      search:
        hybrid_enabled: true
        vector_weight: 0.7      # 0.0-1.0
  defaults:
    token_budget: 8000
    top_n: 10
`,
	"postgrest": `# config: {} is a guaranteed 400: db_anon_role is REQUIRED.
config:
  db_anon_role: web_anon        # REQUIRED, see the note below
  # db_anon_role must already exist in the cluster -- roles are
  # cluster-wide, not per-database: CREATE ROLE <name> NOLOGIN;
  db_schemas: public            # default public
  db_pool: 10                   # 1-30
  max_rows: 1000                # 1-10000
  jwt_secret: CHANGE-ME         # >= 32 characters
  jwt_aud: pgedge
  jwt_role_claim_key: ".role"
  server_cors_allowed_origins: "https://app.example.com"
`,
}

// writeServiceConfigGuidance renders serviceConfigGuidance[serviceType]
// (falling back to mcp's, matching writeService's default for an empty
// service_type) with linePrefix in front of every line. hash is
// prepended to the YAML lines only -- lines that already start with '#'
// are comments in both renderings and must not be double-commented.
// The two call sites are:
//
//   - the blank template, where the whole service example already sits
//     inside a comment: linePrefix "#     ", hash "";
//   - a populated service block, which is live YAML the guidance must
//     not become part of: linePrefix "    ", hash "# ".
//
// Both prefixes are 6 columns wide, so the rendered guidance is
// column-identical either way and one 73-column budget covers both.
func writeServiceConfigGuidance(
	b *strings.Builder, serviceType, linePrefix, hash string,
) {
	guidance, ok := serviceConfigGuidance[serviceType]
	if !ok {
		guidance = serviceConfigGuidance["mcp"]
	}
	for _, line := range strings.Split(
		strings.TrimSuffix(guidance, "\n"), "\n") {
		if strings.HasPrefix(line, "#") {
			fmt.Fprintf(b, "%s%s\n", linePrefix, line)
			continue
		}
		fmt.Fprintf(b, "%s%s%s\n", linePrefix, hash, line)
	}
}

// writePostgresqlConf emits the postgresql_conf stub.
func writePostgresqlConf(b *strings.Builder) {
	b.WriteString(`# postgresql_conf: cluster-wide postgresql.conf settings
#   (key: value). See DatabaseSpec.postgresql_conf.
#   max_connections: 200
#   shared_buffers: 512MB
`)
}

// writePgHbaConf emits the pg_hba_conf stub.
func writePgHbaConf(b *strings.Builder) {
	b.WriteString(`# pg_hba_conf: extra pg_hba.conf rules, one per line.
#   See DatabaseSpec.pg_hba_conf.
#   - "host all all 10.0.0.0/8 scram-sha-256"
`)
}

// writeNodeOverrides documents per-node overrides on the nodes block.
// patroni_port is deliberately absent from the "not covered" list: it
// is no longer buried here -- a detected-systemd template renders it
// directly on every node (writeSystemdNodePorts), and the undetected
// fallback template calls it out prominently right next to port
// (writeIdentity), in both cases before a reader would reach this
// trailing note.
func writeNodeOverrides(b *strings.Builder) {
	b.WriteString(`# Per-node overrides: any node under nodes: may carry its own port,
#   postgres_version, postgresql_conf, or pg_hba_conf to override the
#   cluster-wide values above (init -i can fill these in). Advanced
#   per-node fields not covered by the interview: cpus, memory,
#   pg_ident_conf, source_node, and a per-node backup_config /
#   restore_config. See DatabaseSpec node fields.
`)
}

// writeRestoreHeader emits the edit-note preamble for a standalone
// restore request document consumed by 'database restore <id> -f'.
func writeRestoreHeader(b *strings.Builder) {
	b.WriteString(`# pgEdge Control Plane restore spec.
# Edit this file, then restore a database with:
#   pgedge controlplane database restore <id> -f this-file.yaml
#
# restore_config is required (repository plus the three source_*
# fields). target_nodes is optional; omit it to restore all nodes.
# Full field reference (RestoreDatabaseRequest):
#   https://github.com/pgEdge/control-plane -> RestoreDatabaseRequest
`)
}

// writeTargetNodes emits a populated target_nodes block sequence when
// nodes is non-empty; each element is quoted if a bare scalar would be
// misparsed. Empty nodes emits nothing so the API default (restore all
// nodes) applies.
func writeTargetNodes(b *strings.Builder, nodes []string) {
	if len(nodes) == 0 {
		return
	}
	b.WriteString("target_nodes:\n")
	for _, n := range nodes {
		fmt.Fprintf(b, "  - %s\n", quoteYAML(n))
	}
}

// writeTargetNodesStub emits the commented optional target_nodes
// guidance for the blank restore template.
func writeTargetNodesStub(b *strings.Builder) {
	b.WriteString(`# target_nodes: optional list of node names to restore. Omit to
#   restore all nodes.
# target_nodes:
#   - n1
#   - n2
`)
}

// buildRestoreSpec assembles a complete RestoreDatabaseRequest YAML
// document from an interviewed restore config and optional target
// nodes. The restore_config block is rendered by the shared
// writePopulatedRestore so it cannot drift from the init wizard's
// restore step.
func buildRestoreSpec(rv *restoreValue, targetNodes []string) string {
	var b strings.Builder
	writeRestoreHeader(&b)
	writePopulatedRestore(&b, rv)
	writeTargetNodes(&b, targetNodes)
	return b.String()
}

// buildRestoreTemplate assembles the blank annotated restore template:
// header plus the commented restore_config and target_nodes guidance
// blocks. Editing/uncommenting produces a restore -f-consumable spec.
func buildRestoreTemplate() string {
	var b strings.Builder
	writeRestoreHeader(&b)
	writeRestoreConfigStub(&b)
	writeTargetNodesStub(&b)
	return b.String()
}
