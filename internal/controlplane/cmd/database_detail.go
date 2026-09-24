package cmd

import (
	"fmt"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// The spec-side sections of `database get` in text mode. Each renders
// nothing at all when the database carries nothing for it, so a
// minimal database prints only the summary and its runtime sections.
//
// Credentials never reach any of these tables. The generated spec
// types carry a database user's `password`, a service's `config` (which
// holds LLM provider API keys), a repository's `s3_key`,
// `s3_key_secret`, `azure_key` and `gcs_key`, and free-form
// `custom_options`. None is read here; `-o json` and `-o yaml` carry
// whatever the Control Plane returned, which for the key fields is
// nothing (the API strips them from every response).

var (
	fieldValueColumns = []string{"FIELD", "VALUE"}

	nodeSpecColumns = []string{"NODE", "HOSTS", "PG", "PORT",
		"PATRONI PORT", "CPUS", "MEMORY", "SOURCE"}
	userColumns        = []string{"USERNAME", "OWNER", "ROLES", "ATTRIBUTES"}
	serviceSpecColumns = []string{"SERVICE", "TYPE", "VERSION", "HOSTS",
		"PORT", "CONNECT AS"}
	repositoryColumns = []string{"REPOSITORY", "TYPE", "LOCATION",
		"RETENTION", "RETENTION TYPE"}
	scheduleColumns = []string{"SCHEDULE", "TYPE", "CRON"}
	upgradeColumns  = []string{"IMAGE", "PG", "SPOCK"}
)

// fieldRow is one FIELD/VALUE line of a detail block.
type fieldRow struct{ field, value string }

func (r fieldRow) Columns() []string { return []string{r.field, r.value} }

type cellsRow []string

func (r cellsRow) Columns() []string { return r }

// printSection writes a titled table, or nothing when there are no rows.
func printSection(
	rt *module.Runtime, title string, columns []string, rows []output.Row,
) error {
	if len(rows) == 0 {
		return nil
	}
	fmt.Fprintf(rt.Output.Out, "\n%s\n", title)
	return rt.Output.Print(rows, columns)
}

func int64Or(p *int64, fallback string) string {
	if p == nil {
		return fallback
	}
	return fmt.Sprintf("%d", *p)
}

func stringsOr(p *[]string, fallback string) string {
	if p == nil || len(*p) == 0 {
		return fallback
	}
	return joinStrings(*p)
}

// printSpecSections renders the spec subtree and the fields of the
// database itself that the summary row leaves out.
func printSpecSections(rt *module.Runtime, d *api.Database3) error {
	spec := d.Spec
	if spec == nil {
		return nil
	}
	details := []output.Row{
		fieldRow{"TENANT", derefOr(d.TenantId, "-")},
		fieldRow{"DATABASE NAME", spec.DatabaseName},
		fieldRow{"POSTGRES", derefOr(spec.PostgresVersion, "-")},
		fieldRow{"SPOCK", derefOr(spec.SpockVersion, "-")},
		fieldRow{"CPUS", derefOr(spec.Cpus, "-")},
		fieldRow{"MEMORY", derefOr(spec.Memory, "-")},
		fieldRow{"PORT", int64Or(spec.Port, "-")},
		fieldRow{"PATRONI PORT", int64Or(spec.PatroniPort, "-")},
	}
	if err := printSection(rt, "Details", fieldValueColumns,
		details); err != nil {
		return err
	}

	nodes := make([]output.Row, 0, len(spec.Nodes))
	for _, n := range spec.Nodes {
		nodes = append(nodes, cellsRow{
			n.Name, joinStrings(n.HostIds),
			derefOr(n.PostgresVersion, "-"),
			int64Or(n.Port, "-"), int64Or(n.PatroniPort, "-"),
			derefOr(n.Cpus, "-"), derefOr(n.Memory, "-"),
			derefOr(n.SourceNode, "-"),
		})
	}
	if err := printSection(rt, "Nodes", nodeSpecColumns, nodes); err != nil {
		return err
	}

	if spec.DatabaseUsers != nil {
		users := make([]output.Row, 0, len(*spec.DatabaseUsers))
		for _, u := range *spec.DatabaseUsers {
			owner := "no"
			if u.DbOwner != nil && *u.DbOwner {
				owner = "yes"
			}
			users = append(users, cellsRow{
				u.Username, owner,
				stringsOr(u.Roles, "-"), stringsOr(u.Attributes, "-"),
			})
		}
		if err := printSection(rt, "Users", userColumns, users); err != nil {
			return err
		}
	}

	if spec.Services != nil {
		svcs := make([]output.Row, 0, len(*spec.Services))
		for _, s := range *spec.Services {
			svcs = append(svcs, cellsRow{
				s.ServiceId, string(s.ServiceType), s.Version,
				joinStrings(s.HostIds), int64Or(s.Port, "-"), s.ConnectAs,
			})
		}
		if err := printSection(rt, "Configured services",
			serviceSpecColumns, svcs); err != nil {
			return err
		}
	}

	if bc := spec.BackupConfig; bc != nil {
		repos := make([]output.Row, 0, len(bc.Repositories))
		for _, r := range bc.Repositories {
			repos = append(repos, cellsRow{
				derefOr(r.Id, "-"), string(r.Type),
				repositoryLocation(string(r.Type), r.S3Bucket, r.S3Region,
					r.GcsBucket, r.AzureAccount, r.AzureContainer,
					r.BasePath),
				int64Or(r.RetentionFull, "-"),
				retentionType(r.RetentionFullType),
			})
		}
		if err := printSection(rt, "Backup repositories",
			repositoryColumns, repos); err != nil {
			return err
		}
		if bc.Schedules != nil {
			scheds := make([]output.Row, 0, len(*bc.Schedules))
			for _, s := range *bc.Schedules {
				scheds = append(scheds, cellsRow{
					s.Id, string(s.Type), s.CronExpression})
			}
			if err := printSection(rt, "Backup schedules",
				scheduleColumns, scheds); err != nil {
				return err
			}
		}
	}

	if rc := spec.RestoreConfig; rc != nil {
		repo := rc.Repository
		restore := []output.Row{
			fieldRow{"SOURCE DATABASE", rc.SourceDatabaseId},
			fieldRow{"SOURCE DATABASE NAME", rc.SourceDatabaseName},
			fieldRow{"SOURCE NODE", rc.SourceNodeName},
			fieldRow{"REPOSITORY", derefOr(repo.Id, "-")},
			fieldRow{"REPOSITORY TYPE", string(repo.Type)},
			fieldRow{"LOCATION", repositoryLocation(string(repo.Type),
				repo.S3Bucket, repo.S3Region, repo.GcsBucket,
				repo.AzureAccount, repo.AzureContainer, repo.BasePath)},
		}
		if err := printSection(rt, "Restore", fieldValueColumns,
			restore); err != nil {
			return err
		}
	}
	return nil
}

// repositoryLocation names where a backup repository lives, by type:
// the bucket (and region) for s3, the bucket for gcs, account/container
// for azure, and the base path for posix and cifs. A base path on a
// cloud type is appended. Keys are not parameters here on purpose.
func repositoryLocation(
	typ string, s3Bucket, s3Region, gcsBucket, azureAccount,
	azureContainer, basePath *string,
) string {
	var loc string
	switch typ {
	case "s3":
		loc = output.DerefString(s3Bucket)
		if r := output.DerefString(s3Region); r != "" && loc != "" {
			loc += " (" + r + ")"
		}
	case "gcs":
		loc = output.DerefString(gcsBucket)
	case "azure":
		acct, ctr := output.DerefString(azureAccount),
			output.DerefString(azureContainer)
		switch {
		case acct != "" && ctr != "":
			loc = acct + "/" + ctr
		default:
			loc = acct + ctr
		}
	}
	if bp := output.DerefString(basePath); bp != "" {
		if loc == "" {
			loc = bp
		} else {
			loc += " " + bp
		}
	}
	if loc == "" {
		return "-"
	}
	return loc
}

func retentionType(t *api.BackupRepositorySpecRetentionFullType) string {
	if t == nil {
		return "-"
	}
	return string(*t)
}

// printAvailableUpgrades renders the upgrade candidates the Control
// Plane returns when asked for them with include=available_upgrades.
// Absent means not asked; an empty list means asked and none.
func printAvailableUpgrades(rt *module.Runtime, d *api.Database3) error {
	if d.AvailableUpgrades == nil {
		return nil
	}
	if len(*d.AvailableUpgrades) == 0 {
		fmt.Fprintln(rt.Stderr, "No upgrades available.")
		return nil
	}
	rows := make([]output.Row, 0, len(*d.AvailableUpgrades))
	for _, u := range *d.AvailableUpgrades {
		rows = append(rows, cellsRow{u.Image, u.PostgresVersion,
			u.SpockVersion})
	}
	return printSection(rt, "Available upgrades", upgradeColumns, rows)
}

// serviceStatusCells renders a running service's status fields, "-"
// for each the Control Plane has not reported.
func serviceStatusCells(st *api.ServiceInstanceStatus) (
	ready, health, image, addresses, ports string,
) {
	ready, health, image, addresses, ports = "-", "-", "-", "-", "-"
	if st == nil {
		return
	}
	if st.ServiceReady != nil {
		ready = output.BoolYesNo(*st.ServiceReady)
	}
	if st.HealthCheck != nil {
		health = string(st.HealthCheck.Status)
	}
	image = derefOr(st.ImageVersion, "-")
	addresses = stringsOr(st.Addresses, "-")
	if st.Ports != nil && len(*st.Ports) > 0 {
		parts := make([]string, 0, len(*st.Ports))
		for _, p := range *st.Ports {
			// The host port is what a caller can dial; the container
			// port is shown only when nothing is published.
			port := p.HostPort
			if port == nil {
				port = p.ContainerPort
			}
			parts = append(parts, p.Name+":"+int64Or(port, "-"))
		}
		ports = strings.Join(parts, ", ")
	}
	return
}
