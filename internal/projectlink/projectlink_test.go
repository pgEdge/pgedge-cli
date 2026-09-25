package projectlink

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

const (
	dbID     = "e5f6a7b8-c9d0-1234-efab-567890123456"
	branchID = "0a1b2c3d-4e5f-6789-abcd-ef0123456789"
)

func mustWriteLink(t *testing.T, dir string, l Link) {
	t.Helper()
	if _, err := Write(dir, l); err != nil {
		t.Fatalf("Write(%s): %v", dir, err)
	}
}

func TestFind(t *testing.T) {
	managed := Link{Module: ModuleManaged, DatabaseID: dbID}

	tests := []struct {
		name     string
		setup    func(t *testing.T, root string) (start, home string)
		wantRoot string // relative to root; "" means no link found
	}{
		{"link in the start folder", func(t *testing.T, root string) (string, string) {
			mustWriteLink(t, root, managed)
			return root, ""
		}, "."},
		{"nearest link above wins", func(t *testing.T, root string) (string, string) {
			mustWriteLink(t, root, managed)
			mid := filepath.Join(root, "a")
			mustWriteLink(t, mid, Link{Module: ModuleManaged, DatabaseID: dbID, BranchID: branchID})
			start := filepath.Join(mid, "b", "c")
			if err := os.MkdirAll(start, 0o750); err != nil {
				t.Fatal(err)
			}
			return start, ""
		}, "a"},
		{"stops at the first .git", func(t *testing.T, root string) (string, string) {
			mustWriteLink(t, root, managed)
			repo := filepath.Join(root, "repo")
			if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o750); err != nil {
				t.Fatal(err)
			}
			return repo, ""
		}, ""},
		{"a link beside .git is found", func(t *testing.T, root string) (string, string) {
			if err := os.MkdirAll(filepath.Join(root, ".git"), 0o750); err != nil {
				t.Fatal(err)
			}
			mustWriteLink(t, root, managed)
			return root, ""
		}, "."},
		{"never reads home's .pgedge", func(t *testing.T, root string) (string, string) {
			mustWriteLink(t, root, managed)
			start := filepath.Join(root, "project")
			if err := os.MkdirAll(start, 0o750); err != nil {
				t.Fatal(err)
			}
			return start, root
		}, ""},
		{"nothing anywhere", func(t *testing.T, root string) (string, string) {
			return root, root
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			start, home := tt.setup(t, root)
			got, err := Find(start, home)
			if err != nil {
				t.Fatalf("Find: %v", err)
			}
			if tt.wantRoot == "" {
				if got != nil {
					t.Fatalf("Find found %s, want none", got.Path)
				}
				return
			}
			if got == nil {
				t.Fatal("Find found nothing")
			}
			want := filepath.Join(root, tt.wantRoot)
			if got.Root != want || got.Path != filepath.Join(want, Dir, File) {
				t.Errorf("Find = root %s path %s, want root %s", got.Root, got.Path, want)
			}
		})
	}
}

func TestReadRefusesABadLink(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"not yaml", "module: [", "yaml"},
		{"unknown module", "module: starfleet/byoc\ndatabase_id: " + dbID + "\n", `unknown module "starfleet/byoc"`},
		{"bad database id", "module: starfleet/managed\ndatabase_id: nope\n", `invalid database_id "nope"`},
		{"bad branch id", "module: starfleet/managed\ndatabase_id: " + dbID + "\nbranch_id: nope\n", `invalid branch_id "nope"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, Dir), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, Dir, File), []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Read(dir)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Read() = %v, want an error containing %q", err, tt.want)
			}
			if _, err := Find(dir, ""); err == nil {
				t.Error("Find() accepted the bad link")
			}
		})
	}
}

func TestWriteRoundTripsAndOmitsAnEmptyBranch(t *testing.T) {
	dir := t.TempDir()
	path, err := Write(dir, Link{Module: ModuleManaged, DatabaseID: dbID})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if want := "module: starfleet/managed\ndatabase_id: " + dbID + "\n"; string(raw) != want {
		t.Errorf("file = %q, want %q", raw, want)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644 so the file can be committed", info.Mode().Perm())
	}

	l := Link{Module: ModuleManaged, DatabaseID: dbID, BranchID: branchID}
	mustWriteLink(t, dir, l)
	got, err := Read(dir)
	if err != nil || got == nil || got.Link != l {
		t.Errorf("Read() = %+v, %v; want %+v", got, err, l)
	}

	if _, err := Write(dir, Link{Module: "x", DatabaseID: dbID}); err == nil {
		t.Error("Write accepted an unknown module")
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	if removed, err := Remove(dir); removed || err != nil {
		t.Errorf("Remove with no link = %v, %v; want false, nil", removed, err)
	}

	mustWriteLink(t, dir, Link{Module: ModuleManaged, DatabaseID: dbID})
	if removed, err := Remove(dir); !removed || err != nil {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(dir, Dir)); !os.IsNotExist(err) {
		t.Errorf("empty %s was left behind: %v", Dir, err)
	}

	mustWriteLink(t, dir, Link{Module: ModuleManaged, DatabaseID: dbID})
	other := filepath.Join(dir, Dir, "other-tool.yaml")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("another tool's file in %s was removed: %v", Dir, err)
	}
}

func TestAddEnvPullFlags(t *testing.T) {
	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	AddEnvPullFlags(fs)
	for name, def := range map[string]string{"file": "", "var": "DATABASE_URL", "user-type": "", "branch": ""} {
		f := fs.Lookup(name)
		if f == nil || f.DefValue != def {
			t.Errorf("flag --%s = %+v, want default %q", name, f, def)
		}
	}
}

func TestEveryModuleHasACommandPath(t *testing.T) {
	for module, path := range CommandPaths {
		if len(path) == 0 || path[len(path)-1] != "pull" {
			t.Errorf("CommandPaths[%q] = %v, want a path ending in pull", module, path)
		}
	}
}

func TestIsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if !IsHome(home) {
		t.Error("IsHome(home) = false")
	}
	if !IsHome(home + "/.") {
		t.Error("IsHome did not see through a non-canonical spelling")
	}
	if IsHome(t.TempDir()) {
		t.Error("IsHome(another folder) = true")
	}
}

func TestFindStopsAtASymlinkedHome(t *testing.T) {
	root := t.TempDir()
	realHome := filepath.Join(root, "real-home")
	if err := os.MkdirAll(filepath.Join(realHome, "project"), 0o750); err != nil {
		t.Fatal(err)
	}
	mustWriteLink(t, root, Link{Module: ModuleManaged, DatabaseID: dbID})
	homeLink := filepath.Join(root, "home-link")
	if err := os.Symlink(realHome, homeLink); err != nil {
		t.Fatal(err)
	}
	// The working directory arrives resolved, $HOME as the symlink.
	got, err := Find(filepath.Join(realHome, "project"), homeLink)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("Find walked past home to %s", got.Path)
	}
}
