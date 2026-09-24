package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newDatabaseInitCmd builds `pgedge controlplane database init`, the template
// spec generator. It writes to stdout so it can be redirected to a
// file or piped into 'database create -f -'. With -i/--interactive it
// interviews the user for scaffold values instead of emitting a blank
// template.
func newDatabaseInitCmd(rt *module.Runtime) *cobra.Command {
	var nodes int
	var interactive bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate a starter database spec",
		Long: `init prints a commented starter DatabaseSpec to stdout.
Redirect it to a file, edit it, then apply with 'database create -f'.
Use --nodes to set how many nodes the template includes. Use
--interactive to be interviewed for the scaffold values instead.

init tries to detect the target Control Plane's orchestrator (the
same resolved connection every controlplane verb uses, under one short overall
deadline) and shapes the template to match: a systemd Control Plane
gets port and patroni_port set on every node with distinct values, a
Docker Swarm one gets no per-node ports. When no Control Plane answers --
including the common case of none configured -- init stays usable
offline and falls back to a generic, annotated template, naming what
it found (or didn't) on stderr. Detection never fails the command.

Example:
  pgedge controlplane database init > spec.yaml
  pgedge controlplane database init --nodes 3 | pgedge controlplane database create db -f -
  pgedge controlplane database init -i > spec.yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if nodes < 1 {
				return &ExitError{
					msg:  "--nodes must be at least 1",
					code: ExitUsage,
				}
			}
			jsonOut := rt.Output.Format == "json"
			if jsonOut && !interactive {
				return &ExitError{
					msg: "JSON output requires --interactive (-i); " +
						"non-interactive init emits YAML only. Use " +
						"'init -i -o json' for JSON, or drop -o json " +
						"for the YAML template.",
					code: ExitUsage,
				}
			}
			if interactive {
				// The TTY guard comes before detection so a non-TTY
				// 'init -i' fails on the pure usage error immediately
				// instead of paying for a network probe it will
				// discard. Every path that goes on to emit a template
				// detects first (below).
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return &ExitError{
						msg:  "interactive mode requires a terminal",
						code: ExitUsage,
					}
				}
				det := detectOrchestrator(rt, cmd)
				spec, err := runInterview(rt.Stdin, rt.Stderr, nodes, det)
				if err != nil {
					return err
				}
				if jsonOut {
					if err := emitSpecJSON(rt.Stdout, spec); err != nil {
						return err
					}
				} else {
					fmt.Fprint(rt.Stdout, spec)
				}
				if jsonOut {
					fmt.Fprintf(rt.Stderr,
						"\nWrote JSON spec to stdout. Pipe it into:\n"+
							"  pgedge controlplane database create <id> -f -\n")
				} else {
					fmt.Fprintf(rt.Stderr,
						"\nWrote spec. Create the database with:\n"+
							"  pgedge controlplane database create <id> -f <file>\n")
				}
				fmt.Fprintln(rt.Stderr, orchestratorNote(det))
				return nil
			}
			// No interview runs here: emit the pure annotated
			// template with the requested node count.
			det := detectOrchestrator(rt, cmd)
			v := specValues{
				nodes:        make([]nodeValue, nodes),
				orchestrator: det,
			}
			fmt.Fprint(rt.Stdout, buildSpec(v))
			fmt.Fprintln(rt.Stderr, orchestratorNote(det))
			return nil
		},
	}
	cmd.Flags().IntVar(&nodes, "nodes", 3,
		"Number of nodes to include in the template")
	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false,
		"Interview for spec values instead of a blank template")
	// Deliberately NOT cli.MarkMutating: init prints a template to
	// stdout and issues no write at all. Its only network traffic is the
	// GET-based orchestrator detection. It was marked mutating in the
	// first version of dry-run purely because the verb is called "init",
	// which made --dry-run an accepted flag that did nothing — the exact
	// thing TestNoReadOnlyLeafCarriesDryRun calls worse than an unknown
	// flag. Recorded in nonMutatingExceptions.

	return cmd
}

// emitSpecJSON renders a populated spec YAML (from the interview) as
// indented JSON on out, for piping into 'database create -f -'. It
// decodes through the same loader create -f uses (loadSpecBytes ->
// api.DatabaseSpec2), so the JSON is guaranteed create-consumable and
// the template's commented stubs — being YAML comments — are dropped.
// A failure here means buildSpec produced something the loader can't
// read, an internal inconsistency, so it surfaces at ExitGeneral.
func emitSpecJSON(out io.Writer, specYAML string) error {
	var spec api.DatabaseSpec2
	if err := loadSpecBytes([]byte(specYAML), &spec); err != nil {
		return &ExitError{
			msg:  fmt.Sprintf("render spec as JSON: %v", err),
			code: ExitGeneral,
		}
	}
	b, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return &ExitError{
			msg:  fmt.Sprintf("render spec as JSON: %v", err),
			code: ExitGeneral,
		}
	}
	fmt.Fprintln(out, string(b))
	return nil
}

// runInterview walks the user through the scaffold fields — database
// name, node count and per-node name/hosts, admin username, and port —
// and returns the resulting spec YAML via buildSpec. nodes seeds the
// default node count prompt. det carries the orchestrator detection
// 'database init' already ran (offline-safe, see detectOrchestrator),
// so the interviewed spec gets the same systemd/swarm-aware template
// shape as the non-interactive path.
func runInterview(
	in io.Reader, errOut io.Writer, nodes int, det detectionResult,
) (string, error) {
	r := bufio.NewReader(in)
	var v specValues
	v.orchestrator = det
	name, err := promptString(r, errOut, "Database name", "my-database")
	if err != nil {
		return "", err
	}
	v.databaseName = name
	count, err := promptInt(r, errOut, "Number of nodes", nodes)
	if err != nil {
		return "", err
	}
	if count < 1 {
		count = 1
	}
	for i := 1; i <= count; i++ {
		nn, err := promptString(r, errOut,
			fmt.Sprintf("Node %d name", i), fmt.Sprintf("n%d", i))
		if err != nil {
			return "", err
		}
		hosts, err := promptString(r, errOut,
			fmt.Sprintf("Node %s host id(s), comma-separated", nn),
			"")
		if err != nil {
			return "", err
		}
		v.nodes = append(v.nodes, nodeValue{
			name: nn, hostIDs: splitCSV(hosts)})
	}
	admin, err := promptString(r, errOut, "Admin username", "admin")
	if err != nil {
		return "", err
	}
	v.adminUser = admin
	port, err := promptOptionalInt(r, errOut,
		"Postgres port (blank = random)")
	if err != nil {
		return "", err
	}
	v.port = port
	wantBackup, err := promptYesNo(r, errOut,
		"Configure automated backups?", false)
	if err != nil {
		return "", err
	}
	if wantBackup {
		bt, err := promptEnum(r, errOut, "Backup repository type",
			[]string{"s3", "gcs", "azure", "posix", "cifs"}, "s3")
		if err != nil {
			return "", err
		}
		b := &backupValue{repoType: bt, fields: map[string]string{}}
		if err := interviewBackupFields(r, errOut, b); err != nil {
			return "", err
		}
		ret, err := promptOptionalInt(r, errOut,
			"Full-backup retention count (blank = none)")
		if err != nil {
			return "", err
		}
		if ret != "" {
			b.fields["retention_full"] = ret
			b.fields["retention_full_type"] = "count"
		}
		wantSched, err := promptYesNo(r, errOut,
			"Add a backup schedule?", false)
		if err != nil {
			return "", err
		}
		if wantSched {
			cron, err := promptString(r, errOut,
				"Cron expression", "0 2 * * *")
			if err != nil {
				return "", err
			}
			styp, err := promptEnum(r, errOut, "Backup type",
				[]string{"full", "incr"}, "full")
			if err != nil {
				return "", err
			}
			b.schedule = &scheduleValue{
				cron: cron, typ: styp, id: "schedule-1"}
		}
		v.backup = b
	}
	wantServices, err := promptYesNo(r, errOut,
		"Configure services?", false)
	if err != nil {
		return "", err
	}
	if wantServices {
		if err := interviewServices(r, errOut, &v, admin); err != nil {
			return "", err
		}
	}
	wantRestore, err := promptYesNo(r, errOut,
		"Restore from an existing backup?", false)
	if err != nil {
		return "", err
	}
	if wantRestore {
		if err := interviewRestore(r, errOut, &v); err != nil {
			return "", err
		}
	}
	wantNodeOverrides, err := promptYesNo(r, errOut,
		"Customize individual nodes?", false)
	if err != nil {
		return "", err
	}
	if wantNodeOverrides {
		if err := interviewNodeOverrides(r, errOut, &v); err != nil {
			return "", err
		}
	}
	warnUnfilledSecrets(errOut, v.services)
	return buildSpec(v), nil
}

// interviewNodeOverrides loops the already-collected nodes and, per
// node the user opts to customize, collects optional overrides of the
// cluster-wide values: Postgres port and version, plus small opt-in
// loops for postgresql.conf settings and pg_hba.conf rules. It mutates
// v.nodes in place. None of these fields is a secret.
func interviewNodeOverrides(
	r *bufio.Reader, errOut io.Writer, v *specValues,
) error {
	for i := range v.nodes {
		n := &v.nodes[i]
		want, err := promptYesNo(r, errOut,
			fmt.Sprintf("Override defaults for node %s?", n.name), false)
		if err != nil {
			return err
		}
		if !want {
			continue
		}
		port, err := promptOptionalInt(r, errOut,
			"Postgres port (blank = cluster default)")
		if err != nil {
			return err
		}
		n.port = port
		ver, err := promptString(r, errOut,
			"Postgres version (blank = cluster default)", "")
		if err != nil {
			return err
		}
		n.postgresVersion = ver
		conf := map[string]string{}
		for {
			more, err := promptYesNo(r, errOut,
				"Add a postgresql.conf setting?", false)
			if err != nil {
				return err
			}
			if !more {
				break
			}
			key, err := promptString(r, errOut, "Setting name", "")
			if err != nil {
				return err
			}
			if key == "" {
				continue
			}
			val, err := promptString(r, errOut, "Value", "")
			if err != nil {
				return err
			}
			if val == "" {
				continue
			}
			conf[key] = val
		}
		if len(conf) > 0 {
			n.postgresqlConf = conf
		}
		var rules []string
		for {
			more, err := promptYesNo(r, errOut,
				"Add a pg_hba.conf rule?", false)
			if err != nil {
				return err
			}
			if !more {
				break
			}
			rule, err := promptString(r, errOut, "Rule line", "")
			if err != nil {
				return err
			}
			if rule != "" {
				rules = append(rules, rule)
			}
		}
		if len(rules) > 0 {
			n.pgHbaConf = rules
		}
	}
	return nil
}

// interviewServices loops one service at a time until the user
// declines "Add another service?". Each service collects the five
// required ServiceSpec2 fields; mcp services additionally offer a
// guided LLM config. adminUser seeds the connect_as default.
func interviewServices(
	r *bufio.Reader, errOut io.Writer, v *specValues, adminUser string,
) error {
	for i := 1; ; i++ {
		styp, err := promptEnum(r, errOut, "Service type",
			[]string{"mcp", "postgrest", "rag"}, "mcp")
		if err != nil {
			return err
		}
		s := serviceValue{serviceType: styp}
		id, err := promptString(r, errOut, "Service id",
			fmt.Sprintf("%s-%d", styp, i))
		if err != nil {
			return err
		}
		s.serviceID = id
		ca, err := promptString(r, errOut,
			"Connect-as database user", adminUser)
		if err != nil {
			return err
		}
		s.connectAs = ca
		hosts, err := promptString(r, errOut,
			"Service host id(s), comma-separated", "")
		if err != nil {
			return err
		}
		s.hostIDs = splitCSV(hosts)
		ver, err := promptString(r, errOut, "Service version", "latest")
		if err != nil {
			return err
		}
		s.version = ver
		if styp == "mcp" {
			if err := interviewMCPConfig(r, errOut, &s); err != nil {
				return err
			}
		}
		v.services = append(v.services, s)
		more, err := promptYesNo(r, errOut, "Add another service?", false)
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
}

// interviewMCPConfig asks the mcp config keys Control Plane actually
// validates (internal/controlplane/svccfg mirrors the full set). LLM settings
// are opt-in and independent of the init_token prompt that always
// follows:
//
//   - Opting into LLM settings sets llm_enabled: true and asks the
//     provider as an enum (CP's own three: anthropic, openai, ollama)
//     rather than free text, then requires a model name -- CP rejects
//     llm_provider/llm_model unless llm_enabled is true, and requires
//     both once it is (mcp_service_config.go:200-205,:157-178 @
//     v0.10.0).
//   - The provider API key/URL is still never prompted for (a
//     secret). Instead of a generic "needs a key?" yes/no,
//     secretKey resolves to the credential key CP will actually look
//     for: anthropic_api_key, openai_api_key, or ollama_url -- each
//     required once its provider is chosen, none optional.
//   - init_token is independent of llm_enabled (it is CP's
//     bootstrap-only security field, mcp_service_config.go:109-116),
//     so it is asked regardless of whether LLM settings were
//     configured.
func interviewMCPConfig(
	r *bufio.Reader, errOut io.Writer, s *serviceValue,
) error {
	want, err := promptYesNo(r, errOut,
		"Configure MCP LLM settings?", false)
	if err != nil {
		return err
	}
	if want {
		s.config = map[string]string{"llm_enabled": "true"}
		provider, perr := promptEnum(r, errOut, "LLM provider",
			[]string{"anthropic", "openai", "ollama"}, "anthropic")
		if perr != nil {
			return perr
		}
		s.config["llm_provider"] = provider
		model, merr := promptRequired(r, errOut, "LLM model")
		if merr != nil {
			return merr
		}
		s.config["llm_model"] = model
		switch provider {
		case "anthropic":
			s.secretKey = "anthropic_api_key"
		case "openai":
			s.secretKey = "openai_api_key"
		case "ollama":
			s.secretKey = "ollama_url"
		}
	}
	initToken, err := promptYesNo(r, errOut,
		"Set an MCP init token? (create-time only)", false)
	if err != nil {
		return err
	}
	s.initToken = initToken
	return nil
}

// unfilledSecret names one CHANGE-ME sentinel warnUnfilledSecrets must
// flag: which service, its type, and the config key holding it.
type unfilledSecret struct {
	serviceID, serviceType, key string
}

// warnUnfilledSecrets prints an unmissable stderr summary naming every
// service config sentinel that must be filled in before create. It
// runs after the spec is built so the reminder groups with the
// interview output. No-op when nothing needs a secret.
func warnUnfilledSecrets(errOut io.Writer, services []serviceValue) {
	var need []unfilledSecret
	for _, s := range services {
		if s.secretKey != "" {
			need = append(need,
				unfilledSecret{s.serviceID, s.serviceType, s.secretKey})
		}
		if s.initToken {
			need = append(need,
				unfilledSecret{s.serviceID, s.serviceType, "init_token"})
		}
	}
	if len(need) == 0 {
		return
	}
	fmt.Fprintf(errOut,
		"\n⚠ %d secret(s) need to be set before this spec "+
			"will work:\n", len(need))
	for _, n := range need {
		fmt.Fprintf(errOut,
			"    - %s (%s): replace config.%s (currently %s)\n",
			n.serviceID, n.serviceType, n.key, secretSentinel)
	}
	fmt.Fprint(errOut,
		"  create will refuse the spec until you set it.\n")
}

// interviewRestoreConfig walks the restore_config fields and returns a
// populated restoreValue: repository type and its fields (shared via
// interviewRepoFields), the three required source_* identifiers, and an
// optional point-in-time recovery target. Repository credentials are
// never asked (credential-chain fallback), so no secret is collected.
func interviewRestoreConfig(
	r *bufio.Reader, errOut io.Writer,
) (*restoreValue, error) {
	rtyp, err := promptEnum(r, errOut, "Restore repository type",
		[]string{"s3", "gcs", "azure", "posix", "cifs"}, "s3")
	if err != nil {
		return nil, err
	}
	rv := &restoreValue{repoType: rtyp, repoFields: map[string]string{}}
	if err := interviewRepoFields(r, errOut, rtyp, rv.repoFields); err != nil {
		return nil, err
	}
	id, err := promptRequired(r, errOut, "Source database id")
	if err != nil {
		return nil, err
	}
	rv.sourceDBID = id
	name, err := promptRequired(r, errOut, "Source database name")
	if err != nil {
		return nil, err
	}
	rv.sourceDBName = name
	node, err := promptString(r, errOut, "Source node name", "n1")
	if err != nil {
		return nil, err
	}
	rv.sourceNodeName = node
	wantPITR, err := promptYesNo(r, errOut,
		"Set a recovery target (point-in-time)?", false)
	if err != nil {
		return nil, err
	}
	if wantPITR {
		rv.restoreOptions = map[string]string{}
		kind, err := promptEnum(r, errOut, "Recovery target kind",
			[]string{"time", "lsn"}, "time")
		if err != nil {
			return nil, err
		}
		rv.restoreOptions["type"] = kind
		target, err := promptString(r, errOut,
			"Recovery target (blank = omit)", "")
		if err != nil {
			return nil, err
		}
		if target != "" {
			rv.restoreOptions["target"] = target
		}
	}
	return rv, nil
}

// interviewRestore populates the DatabaseSpec restore_config section by
// delegating to the shared interviewRestoreConfig.
func interviewRestore(
	r *bufio.Reader, errOut io.Writer, v *specValues,
) error {
	rv, err := interviewRestoreConfig(r, errOut)
	if err != nil {
		return err
	}
	v.restore = rv
	return nil
}

// interviewRepoFields asks only the fields relevant to repoType and
// stores each answer under its json key in fields. Credentials
// (s3_key/s3_key_secret, gcs_key, azure_key) are intentionally never
// asked here — populated backup/restore blocks document them as a
// commented note instead so a generated spec never bakes in a secret.
// Shared by the guided backups and guided restore interviews so their
// repository prompts cannot drift.
func interviewRepoFields(
	r *bufio.Reader, errOut io.Writer,
	repoType string, fields map[string]string,
) error {
	ask := func(label, key string, required bool) error {
		var val string
		var err error
		if required {
			val, err = promptRequired(r, errOut, label)
		} else {
			val, err = promptString(r, errOut, label, "")
		}
		if err != nil {
			return err
		}
		if val != "" {
			fields[key] = val
		}
		return nil
	}
	switch repoType {
	case "s3":
		if err := ask("S3 bucket", "s3_bucket", true); err != nil {
			return err
		}
		if err := ask("S3 region", "s3_region", true); err != nil {
			return err
		}
		return ask("S3 endpoint (blank = AWS default)", "s3_endpoint", false)
	case "gcs":
		if err := ask("GCS bucket", "gcs_bucket", true); err != nil {
			return err
		}
		return ask("GCS endpoint (blank = default)", "gcs_endpoint", false)
	case "azure":
		if err := ask("Azure account", "azure_account", true); err != nil {
			return err
		}
		if err := ask("Azure container", "azure_container", true); err != nil {
			return err
		}
		return ask("Azure endpoint (blank = default)", "azure_endpoint", false)
	default: // posix, cifs
		return ask("Base path", "base_path", true)
	}
}

// interviewBackupFields asks the repository fields for a backup_config
// repository. It delegates to the shared interviewRepoFields so backup
// and restore repository prompts cannot drift.
func interviewBackupFields(
	r *bufio.Reader, errOut io.Writer, b *backupValue,
) error {
	return interviewRepoFields(r, errOut, b.repoType, b.fields)
}

// splitCSV turns "a, b" into ["a","b"], dropping blanks. Empty input
// yields nil so buildSpec emits the [CHANGE-ME] placeholder.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
