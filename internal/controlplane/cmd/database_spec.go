package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// postgresVersionRE is the Control Plane's required 'major.minor' form.
// A bare major like "16" gets an opaque 400 from the Control Plane, so
// the CLI refuses it first with a clear message.
var postgresVersionRE = regexp.MustCompile(`^\d+\.\d+$`)

// nodeVersion lets validatePostgresVersions name the offending node
// without depending on either generated spec type (create uses
// DatabaseSpec2, update DatabaseSpec5).
type nodeVersion struct {
	name    string
	version *string
}

// validatePostgresVersions rejects a cluster-wide or per-node
// postgres_version that is set but not 'major.minor'. A nil or empty
// version is left for the Control Plane to default.
func validatePostgresVersions(cluster *string, nodes []nodeVersion) error {
	if err := checkPGVersion(cluster, "postgres_version"); err != nil {
		return err
	}
	for _, n := range nodes {
		label := fmt.Sprintf("node %q postgres_version", n.name)
		if err := checkPGVersion(n.version, label); err != nil {
			return err
		}
	}
	return nil
}

func checkPGVersion(v *string, label string) error {
	if v == nil || *v == "" {
		return nil
	}
	if !postgresVersionRE.MatchString(*v) {
		return &ExitError{
			msg: fmt.Sprintf(
				"%s %q is not in major.minor format (e.g. \"16.14\"); "+
					"set a full Postgres version",
				label, *v),
			code: ExitUsage,
		}
	}
	return nil
}

// loadSpecFile reads path ("-" = rt.Stdin) as YAML or JSON into dst.
func loadSpecFile(rt *module.Runtime, path string, dst any) error {
	raw, err := readSpecFile(rt, path)
	if err != nil {
		return err
	}
	return loadSpecBytes(raw, dst)
}

// loadSpecFileUnwrapping is loadSpecFile for create -f and update -f.
// It also accepts a whole `database get -o yaml` document and decodes
// its spec: subtree, which is the round-trip the help text advertises.
// A bare spec has no top-level spec key, so one marks a wrapper.
//
// Not the shared path: restore -f decodes a RestoreDatabaseRequest,
// and unwrapping there would silently change what it accepts.
func loadSpecFileUnwrapping(
	rt *module.Runtime, path string, dst any,
) error {
	raw, err := readSpecFile(rt, path)
	if err != nil {
		return err
	}
	return decodeSpecInto(raw, dst, true)
}

// readSpecFile is shared so the two loaders cannot disagree about "-".
func readSpecFile(rt *module.Runtime, path string) ([]byte, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(rt.Stdin)
	} else {
		raw, err = os.ReadFile(path) //nolint:gosec // G304: user-provided spec path is the feature
	}
	if err != nil {
		return nil, &ExitError{
			msg:  fmt.Sprintf("read spec file: %v", err),
			code: ExitUsage,
		}
	}
	return raw, nil
}

// loadSpecBytes decodes YAML or JSON bytes into dst. The generated
// types carry only json tags, so YAML is normalized to JSON first: a
// direct yaml.Unmarshal ignores json tags and silently drops snake_case
// keys like database_name. Shared by loadSpecFile (restore -f) and
// emitSpecJSON (init -i).
func loadSpecBytes(raw []byte, dst any) error {
	return decodeSpecInto(raw, dst, false)
}

// decodeSpecInto is the one decode path. unwrap takes the spec:
// subtree; only loadSpecFileUnwrapping sets it.
//
// Unknown fields are rejected so a file whose keys match nothing, such
// as a typo'd key, fails locally naming the file instead of being sent
// as an all-zero spec. The Control Plane rejects that body too (verified
// live: 400 missing_field), so this is about the message, not damage.
func decodeSpecInto(raw []byte, dst any, unwrap bool) error {
	var doc interface{}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return &ExitError{
			msg:  fmt.Sprintf("parse spec file: %v", err),
			code: ExitUsage,
		}
	}
	if unwrap {
		if m, ok := doc.(map[string]interface{}); ok {
			if inner, found := m["spec"]; found {
				doc = inner
			}
		}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return &ExitError{
			msg:  fmt.Sprintf("normalize spec: %v", err),
			code: ExitUsage,
		}
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return &ExitError{
			msg:  fmt.Sprintf("parse spec file: %s", specDecodeError(err)),
			code: ExitUsage,
		}
	}
	return nil
}

// specDecodeError drops encoding/json's `json: unknown field` wording,
// which names a format a YAML author never used, and keeps the field.
func specDecodeError(err error) string {
	msg := err.Error()
	const prefix = `json: unknown field `
	if field, ok := strings.CutPrefix(msg, prefix); ok {
		// Names both generators: this path decodes database specs
		// and restore specs alike.
		return fmt.Sprintf(
			"unrecognized field %s. Check the spelling against the "+
				"template that generated this spec: 'database init' "+
				"for a database spec, 'database restore template' "+
				"for a restore spec",
			field)
	}
	return strings.TrimPrefix(msg, "json: ")
}

// requireSpecContent rejects a spec that decoded to nothing useful.
// It takes the two required fields because DatabaseSpec2 and
// DatabaseSpec5 share no interface. The point is naming the file: the
// server's rejection quotes Go type names
// ("[]*server.DatabaseNodeSpecRequestBodyRequestBody(nil)") instead.
func requireSpecContent(path, databaseName string, nodeCount int) error {
	if databaseName != "" && nodeCount > 0 {
		return nil
	}
	return &ExitError{
		msg: fmt.Sprintf(
			"%s does not contain a database spec: database_name and "+
				"nodes are both required. If this file came from "+
				"'database get -o yaml', check it still has its spec: "+
				"section",
			specSource(path)),
		code: ExitUsage,
	}
}

// requireRestoreContent is requireSpecContent for restore -f.
// `database restore template` emits a file that is entirely comments,
// so an unedited template would otherwise be sent as an empty restore.
func requireRestoreContent(path string, cfg api.RestoreConfigSpec) error {
	if cfg.SourceDatabaseId != "" || cfg.SourceDatabaseName != "" ||
		cfg.SourceNodeName != "" || cfg.Repository.Type != "" {
		return nil
	}
	return &ExitError{
		msg: fmt.Sprintf(
			"%s does not contain a restore spec: restore_config is "+
				"required. 'database restore template' emits a "+
				"commented outline — uncomment and fill it in before "+
				"applying it",
			specSource(path)),
		code: ExitUsage,
	}
}

func specSource(path string) string {
	if path == "-" {
		return "the spec read from stdin"
	}
	return path
}

// checkUnfilledPlaceholders rejects a spec still carrying
// secretSentinel, naming the field, rather than round-tripping to a
// server error that names nothing useful. The sentinel is never a real
// credential, so naming its field leaks nothing.
//
// It covers every field `database init` writes a placeholder into:
// node and service host_ids, user passwords, the four repository
// credentials, restore_config, and service config at any depth.
// Fields are checked in file order so the one error reported is the
// one a reader editing top-down reaches first.
//
// It takes pieces rather than a spec so create (DatabaseSpec2) and
// update (DatabaseSpec5) share one scan instead of two that drift.
func checkUnfilledPlaceholders(
	nodes []nodeHosts, users *[]api.DatabaseUserSpec,
	repos []api.BackupRepositorySpec, restore *api.RestoreConfigSpec,
	services []serviceConfig, verb string,
) error {
	for i, n := range nodes {
		for j, host := range n.hostIDs {
			if host == secretSentinel {
				return unfilledError(fmt.Sprintf(
					"nodes[%d].host_ids[%d]", i, j),
					"set the host ID before "+verb)
			}
		}
	}
	if users != nil {
		for i, u := range *users {
			if u.Password != nil && *u.Password == secretSentinel {
				return unfilledError(fmt.Sprintf(
					"database_users[%d].password", i),
					"set the password before "+verb)
			}
		}
	}
	for i, r := range repos {
		if field, ok := repoSentinelHit(r); ok {
			return unfilledError(fmt.Sprintf(
				"backup_config.repositories[%d].%s", i, field),
				"set the credential, or remove the field to use the "+
					"instance credential chain, before "+verb)
		}
	}
	if err := checkUnfilledRestorePlaceholders(restore, verb); err != nil {
		return err
	}
	for _, s := range services {
		for j, host := range s.hostIDs {
			if host == secretSentinel {
				return unfilledError(fmt.Sprintf(
					"service %q host_ids[%d]", s.id, j),
					"set the host ID before "+verb)
			}
		}
		if s.config == nil {
			continue
		}
		// Sorted so a config with two placeholders names the same one
		// on every run: Go randomizes map iteration.
		for _, k := range sortedConfigKeys(*s.config) {
			if path, ok := configSentinelHit(k, (*s.config)[k]); ok {
				return unfilledError(
					fmt.Sprintf("service %q config.%s", s.id, path),
					"set the secret before "+verb)
			}
		}
	}
	return nil
}

// checkUnfilledRestorePlaceholders is the restore_config half of the
// scan, and all of it that `restore -f` reaches.
func checkUnfilledRestorePlaceholders(
	cfg *api.RestoreConfigSpec, verb string,
) error {
	if cfg == nil {
		return nil
	}
	for _, f := range []struct{ name, val string }{
		{"source_database_id", cfg.SourceDatabaseId},
		{"source_database_name", cfg.SourceDatabaseName},
		{"source_node_name", cfg.SourceNodeName},
	} {
		if f.val == secretSentinel {
			return unfilledError("restore_config."+f.name,
				"set the source before "+verb)
		}
	}
	if field, ok := repoSentinelHit(api.BackupRepositorySpec{
		S3Key:       cfg.Repository.S3Key,
		S3KeySecret: cfg.Repository.S3KeySecret,
		GcsKey:      cfg.Repository.GcsKey,
		AzureKey:    cfg.Repository.AzureKey,
	}); ok {
		return unfilledError("restore_config.repository."+field,
			"set the credential, or remove the field to use the "+
				"instance credential chain, before "+verb)
	}
	return nil
}

// repoSentinelHit names the first repository credential still
// carrying secretSentinel. These four are the only repository fields
// the `database init` template writes a placeholder into.
func repoSentinelHit(r api.BackupRepositorySpec) (string, bool) {
	for _, f := range []struct {
		name string
		val  *string
	}{
		{"s3_key", r.S3Key},
		{"s3_key_secret", r.S3KeySecret},
		{"gcs_key", r.GcsKey},
		{"azure_key", r.AzureKey},
	} {
		if f.val != nil && *f.val == secretSentinel {
			return f.name, true
		}
	}
	return "", false
}

// sortedConfigKeys keeps the field a scan reports stable across runs.
func sortedConfigKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// nodeHosts and serviceConfig are the neutral shapes both generated
// spec types are adapted into for the placeholder scan.
type nodeHosts struct{ hostIDs []string }

type serviceConfig struct {
	id      string
	hostIDs []string
	config  *map[string]interface{}
}

// unfilledError gives every unedited placeholder one phrasing, so the
// class of error stays recognisable.
func unfilledError(field, remedy string) error {
	return &ExitError{
		msg: fmt.Sprintf("%s is still %q; %s",
			field, secretSentinel, remedy),
		code: ExitUsage,
	}
}

func createSpecPlaceholders(
	spec *api.DatabaseSpec2,
) ([]nodeHosts, []api.BackupRepositorySpec, []serviceConfig) {
	nodes := make([]nodeHosts, len(spec.Nodes))
	for i, n := range spec.Nodes {
		nodes[i] = nodeHosts{hostIDs: n.HostIds}
	}
	var repos []api.BackupRepositorySpec
	if spec.BackupConfig != nil {
		repos = spec.BackupConfig.Repositories
	}
	var services []serviceConfig
	if spec.Services != nil {
		services = make([]serviceConfig, 0, len(*spec.Services))
		for _, s := range *spec.Services {
			services = append(services, serviceConfig{
				id: s.ServiceId, hostIDs: s.HostIds, config: s.Config})
		}
	}
	return nodes, repos, services
}

func updateSpecPlaceholders(
	spec *api.DatabaseSpec5,
) ([]nodeHosts, []api.BackupRepositorySpec, []serviceConfig) {
	nodes := make([]nodeHosts, len(spec.Nodes))
	for i, n := range spec.Nodes {
		nodes[i] = nodeHosts{hostIDs: n.HostIds}
	}
	var repos []api.BackupRepositorySpec
	if spec.BackupConfig != nil {
		repos = spec.BackupConfig.Repositories
	}
	var services []serviceConfig
	if spec.Services != nil {
		services = make([]serviceConfig, 0, len(*spec.Services))
		for _, s := range *spec.Services {
			services = append(services, serviceConfig{
				id: s.ServiceId, hostIDs: s.HostIds, config: s.Config})
		}
	}
	return nodes, repos, services
}

// configSentinelHit returns the dotted-and-indexed path of a config
// value still carrying secretSentinel. It descends lists as well as
// maps because rag's credentials sit at
// config.pipelines[N].embedding_llm.api_key.
func configSentinelHit(key string, val interface{}) (string, bool) {
	switch v := val.(type) {
	case string:
		if v == secretSentinel {
			return key, true
		}
	case map[string]interface{}:
		for _, nk := range sortedConfigKeys(v) {
			if p, ok := configSentinelHit(key+"."+nk, v[nk]); ok {
				return p, true
			}
		}
	case []interface{}:
		for i, nv := range v {
			if p, ok := configSentinelHit(
				fmt.Sprintf("%s[%d]", key, i), nv); ok {
				return p, true
			}
		}
	}
	return "", false
}

func newDatabaseCreateCmd(rt *module.Runtime) *cobra.Command {
	var specPath, tenantID string
	cmd := &cobra.Command{
		Use:   "create <database_id>",
		Short: "Create a database from a spec file",
		Long: `create submits a new database defined by a spec file
(-f spec.yaml, spec.json, or - for stdin). The operation is
asynchronous; use --wait or --follow to track it.

Example:
  pgedge controlplane database create storefront -f spec.yaml --wait`,
		Args: cobra.ExactArgs(1),
	}
	wf := addWaitFollowFlags(cmd)
	cmd.Flags().StringVarP(&specPath, "file", "f", "",
		"Spec file path, or - for stdin (required)")
	cmd.Flags().StringVar(&tenantID, "tenant-id", "",
		"Optional tenant ID override")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if specPath == "" {
			return &ExitError{
				msg: "a spec file is required: -f <path> " +
					"(or - for stdin)",
				code: ExitUsage,
			}
		}
		var spec api.DatabaseSpec2
		if err := loadSpecFileUnwrapping(
			rt, specPath, &spec); err != nil {
			return err
		}
		if err := requireSpecContent(
			specPath, spec.DatabaseName, len(spec.Nodes)); err != nil {
			return err
		}
		nodes := make([]nodeVersion, len(spec.Nodes))
		for i, n := range spec.Nodes {
			nodes[i] = nodeVersion{name: n.Name, version: n.PostgresVersion}
		}
		if err := validatePostgresVersions(
			spec.PostgresVersion, nodes); err != nil {
			return err
		}
		rt.DryRun.Pass("postgres versions well-formed")
		nodeHostIDs, repos, serviceCfgs := createSpecPlaceholders(&spec)
		if err := checkUnfilledPlaceholders(nodeHostIDs,
			spec.DatabaseUsers, repos, spec.RestoreConfig, serviceCfgs,
			"create"); err != nil {
			return err
		}
		rt.DryRun.Pass("spec carries no unfilled template placeholders")
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		// clientFromCmd above already surfaced any resolution error.
		conn, _ := resolveConnection(rt, cmd)
		warnSystemdPortsMissing(rt, client, conn,
			createSpecNodePorts(&spec))
		id := args[0]
		body := api.CreateDatabaseJSONRequestBody{Id: &id, Spec: spec}
		if tenantID != "" {
			body.TenantId = &tenantID
		}
		resp, err := client.CreateDatabaseWithResponse(
			context.Background(), body)
		if err != nil {
			return networkError("create database", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return nil
		}
		return announceAndWait(
			rt, client, wf, "Create", args[0],
			resp.JSON200.Task, resp.JSON200)
	}
	cli.MarkMutating(cmd)

	return cmd
}

func newDatabaseUpdateCmd(rt *module.Runtime) *cobra.Command {
	var specPath string
	cmd := &cobra.Command{
		Use:   "update <database_id>",
		Short: "Update a database from a spec file",
		Long: `update applies a new spec to an existing database
(-f spec.yaml, spec.json, or - for stdin). Round-trips with
'database get -o yaml'. Asynchronous; use --wait/--follow to track.

Example:
  pgedge controlplane database get storefront -o yaml > spec.yaml
  pgedge controlplane database update storefront -f spec.yaml --wait`,
		Args: cobra.ExactArgs(1),
	}
	wf := addWaitFollowFlags(cmd)
	cmd.Flags().StringVarP(&specPath, "file", "f", "",
		"Spec file path, or - for stdin (required)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if specPath == "" {
			return &ExitError{
				msg:  "a spec file is required: -f <path>",
				code: ExitUsage,
			}
		}
		var spec api.DatabaseSpec5
		if err := loadSpecFileUnwrapping(
			rt, specPath, &spec); err != nil {
			return err
		}
		if err := requireSpecContent(
			specPath, spec.DatabaseName, len(spec.Nodes)); err != nil {
			return err
		}
		nodes := make([]nodeVersion, len(spec.Nodes))
		for i, n := range spec.Nodes {
			nodes[i] = nodeVersion{name: n.Name, version: n.PostgresVersion}
		}
		if err := validatePostgresVersions(
			spec.PostgresVersion, nodes); err != nil {
			return err
		}
		rt.DryRun.Pass("postgres versions well-formed")
		nodeHostIDs, repos, serviceCfgs := updateSpecPlaceholders(&spec)
		if err := checkUnfilledPlaceholders(nodeHostIDs,
			spec.DatabaseUsers, repos, spec.RestoreConfig, serviceCfgs,
			"update"); err != nil {
			return err
		}
		rt.DryRun.Pass("spec carries no unfilled template placeholders")
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		// clientFromCmd above already surfaced any resolution error.
		conn, _ := resolveConnection(rt, cmd)
		warnSystemdPortsMissing(rt, client, conn,
			updateSpecNodePorts(&spec))
		body := api.UpdateDatabaseJSONRequestBody{Spec: spec}
		resp, err := client.UpdateDatabaseWithResponse(
			context.Background(), args[0],
			&api.UpdateDatabaseParams{}, body)
		if err != nil {
			return networkError("update database", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return nil
		}
		return announceAndWait(
			rt, client, wf, "Update", args[0],
			resp.JSON200.Task, resp.JSON200)
	}
	cli.MarkMutating(cmd)

	return cmd
}
