// Package projectlink reads and writes .pgedge/link.yaml, the file
// that ties a project folder to one database. It holds identifiers
// only, never a credential or a profile name, so it can be committed.
package projectlink

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pgEdge/pgedge-cli/internal/atomicfile"

	"github.com/google/uuid"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

// Dir and File name the link inside a project folder. `.pgedge` is
// shared with other pgEdge tools, so the CLI owns only File within it.
const (
	Dir  = ".pgedge"
	File = "link.yaml"
)

// ModuleManaged is the one Module value v1 writes.
const ModuleManaged = "starfleet/managed"

// Link is the file's content.
type Link struct {
	Module     string `yaml:"module"`
	DatabaseID string `yaml:"database_id"`
	BranchID   string `yaml:"branch_id,omitempty"`
}

// Found is a link and where it was read from.
type Found struct {
	Link
	// Path is the link file; Root is the project folder holding Dir.
	Path string
	Root string
}

// CommandPaths maps a link's Module to the command path, under root,
// of that module's `env pull`. Root `pgedge env pull` dispatches on it.
var CommandPaths = map[string][]string{
	ModuleManaged: {"starfleet", "managed", "database", "env", "pull"},
}

// Validate refuses a link naming an unknown module or a malformed ID,
// so a hand-edited file fails here rather than as an API 400.
func (l Link) Validate() error {
	if _, ok := CommandPaths[l.Module]; !ok {
		return fmt.Errorf("unknown module %q", l.Module)
	}
	if _, err := uuid.Parse(l.DatabaseID); err != nil {
		return fmt.Errorf("invalid database_id %q: %v", l.DatabaseID, err)
	}
	if l.BranchID != "" {
		if _, err := uuid.Parse(l.BranchID); err != nil {
			return fmt.Errorf("invalid branch_id %q: %v", l.BranchID, err)
		}
	}
	return nil
}

// Find walks up from start to the nearest folder holding Dir/File. It
// stops after the first folder holding a .git entry, and never
// examines home itself: ~/.pgedge is the shared config root, not a
// project. A nil Found with a nil error means no link applies.
func Find(start, home string) (*Found, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return nil, err
	}
	if home != "" {
		if home, err = filepath.Abs(home); err != nil {
			return nil, err
		}
	}
	for {
		if dir == home {
			return nil, nil
		}
		path := filepath.Join(dir, Dir, File)
		f, err := read(path)
		if err != nil || f != nil {
			return f, err
		}
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return nil, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
}

// IsHome reports whether dir is the user's home folder.
func IsHome(dir string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	a, errA := os.Stat(dir)
	b, errB := os.Stat(home)
	return errA == nil && errB == nil && os.SameFile(a, b)
}

// Read returns the link in dir itself, without walking up.
func Read(dir string) (*Found, error) {
	return read(filepath.Join(dir, Dir, File))
}

func read(path string) (*Found, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path is Dir/File under a walked folder
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var l Link
	if err := yaml.Unmarshal(raw, &l); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := l.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &Found{Link: l, Path: path, Root: filepath.Dir(filepath.Dir(path))}, nil
}

// Write stores l in dir/Dir/File through a temporary file in the same
// folder, so an interrupted write never leaves a truncated link.
func Write(dir string, l Link) (string, error) {
	if err := l.Validate(); err != nil {
		return "", err
	}
	raw, err := yaml.Marshal(l)
	if err != nil {
		return "", err
	}
	linkDir := filepath.Join(dir, Dir)
	if err := os.MkdirAll(linkDir, 0o750); err != nil {
		return "", err
	}
	path := filepath.Join(linkDir, File)
	if err := atomicfile.Write(path, raw, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Remove deletes the link in dir, and Dir with it when nothing else is
// left there. It reports whether a link existed.
func Remove(dir string) (bool, error) {
	path := filepath.Join(dir, Dir, File)
	if err := os.Remove(path); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	// Fails harmlessly when another tool keeps files in Dir.
	_ = os.Remove(filepath.Join(dir, Dir))
	return true, nil
}

// AddEnvPullFlags declares env pull's flags. Root `pgedge env pull`
// and each module's own verb both call it, and the root copies every
// flag it was given onto the module's command, so the two forms
// cannot drift apart.
func AddEnvPullFlags(flags *pflag.FlagSet) {
	flags.String("file", "",
		"File to write (default .env beside .pgedge/, or in the current folder when an ID is given)")
	flags.String("var", "DATABASE_URL", "Variable name to set")
	flags.String("user-type", "",
		"Role whose credentials to use: admin, app or app_read_only (default app)")
}
