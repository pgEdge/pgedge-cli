package cli

import (
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
)

// versionInfo is the structured version payload for json/yaml. Modules
// is omitted when empty, so a launcher with no modules prints no key.
type versionInfo struct {
	Version   string          `json:"version" yaml:"version"`
	Commit    string          `json:"commit" yaml:"commit"`
	BuildDate string          `json:"build_date" yaml:"build_date"`
	Modules   []moduleVersion `json:"modules,omitempty" yaml:"modules,omitempty"`
}

// moduleVersion is one module's line in the version payload.
type moduleVersion struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
}

// NewVersionCmd reports the launcher version plus each module's
// version. rt supplies the output renderer; modules is the set to
// list (nil renders the launcher alone). rt is nil only in unit tests
// that exercise flag registration without running the command.
func NewVersionCmd(rt *module.Runtime, modules []module.ModuleInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the pgedge CLI version",
		Long: `Print the version of the unified pgedge binary and each
module it carries, with the launcher's build commit and date. Honors
the global -o flag: text (default), json, or yaml.

Example:
  pgedge version
  pgedge version -o json`,
		Annotations: map[string]string{
			AnnotationProfileExempt: "never reads rt.Config or rt.Profile",
		},
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := versionInfo{
				Version:   Version,
				Commit:    Commit,
				BuildDate: BuildDate,
			}
			for _, m := range modules {
				info.Modules = append(info.Modules,
					moduleVersion{Name: m.Name, Version: m.Version})
			}
			switch rt.Output.Format {
			case "json", "yaml":
				return rt.Output.Print(info, nil)
			default:
				out := cmd.OutOrStdout()
				// Line 1: bare version (grep-stable). Then metadata,
				// then a modules block when any are present.
				fmt.Fprintln(out, info.Version)
				fmt.Fprintf(out, "commit:     %s\n", info.Commit)
				fmt.Fprintf(out, "build date: %s\n", info.BuildDate)
				if len(info.Modules) > 0 {
					fmt.Fprintln(out, "modules:")
					for _, m := range info.Modules {
						fmt.Fprintf(out, "  %-12s %s\n", m.Name, m.Version)
					}
				}
				return nil
			}
		},
	}
}
