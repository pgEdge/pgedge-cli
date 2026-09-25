package dotenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const uri = "postgresql://app:p%24ss@db.example:5432/app?sslmode=require"

func TestSetMerges(t *testing.T) {
	tests := []struct {
		name, before, after string
	}{
		{"empty file", "", "DATABASE_URL=" + uri + "\n"},
		{"appends after other keys",
			"# app settings\nPORT=3000\n\nSECRET='x y'\n",
			"# app settings\nPORT=3000\n\nSECRET='x y'\nDATABASE_URL=" + uri + "\n"},
		{"no trailing newline",
			"PORT=3000",
			"PORT=3000\nDATABASE_URL=" + uri + "\n"},
		{"replaces in place",
			"A=1\nDATABASE_URL=old\nB=2\n",
			"A=1\nDATABASE_URL=" + uri + "\nB=2\n"},
		{"replaces the last of duplicates",
			"DATABASE_URL=first\nA=1\nDATABASE_URL=second\n",
			"DATABASE_URL=first\nA=1\nDATABASE_URL=" + uri + "\n"},
		{"keeps export",
			"export DATABASE_URL=\"old\"\n",
			"export DATABASE_URL=" + uri + "\n"},
		{"spaces around the equals sign",
			"  DATABASE_URL = old\n",
			"DATABASE_URL=" + uri + "\n"},
		{"a longer name is not a match",
			"DATABASE_URL_OLD=x\n",
			"DATABASE_URL_OLD=x\nDATABASE_URL=" + uri + "\n"},
		{"a comment is not a match",
			"# DATABASE_URL=x\n",
			"# DATABASE_URL=x\nDATABASE_URL=" + uri + "\n"},
		{"CRLF file stays CRLF",
			"A=1\r\nDATABASE_URL=old\r\n",
			"A=1\r\nDATABASE_URL=" + uri + "\r\n"},
		{"CRLF file appends CRLF",
			"A=1\r\n",
			"A=1\r\nDATABASE_URL=" + uri + "\r\n"},
		{"last line replaced without its newline",
			"A=1\nDATABASE_URL=old",
			"A=1\nDATABASE_URL=" + uri + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(tt.before), 0o640); err != nil {
				t.Fatal(err)
			}
			if err := Set(path, "DATABASE_URL", uri); err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, _ := os.ReadFile(path)
			if string(got) != tt.after {
				t.Errorf("file:\n got %q\nwant %q", got, tt.after)
			}
			if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
				t.Errorf("mode = %v, want the file's own 0640", info.Mode().Perm())
			}
		})
	}
}

func TestSetCreatesAPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := Set(path, "DATABASE_URL", uri); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSetFollowsASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.env")
	if err := os.WriteFile(target, []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".env")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Set(link, "DATABASE_URL", uri); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Lstat(link); info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a file")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "A=1\nDATABASE_URL="+uri+"\n" {
		t.Errorf("target = %q", got)
	}
}

func TestSetRefuses(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name, path, key, value, want string
	}{
		{"bad name", filepath.Join(dir, ".env"), "1BAD", uri, "invalid variable name"},
		{"dollar", filepath.Join(dir, ".env"), "DATABASE_URL", "a$b", "cannot be written unquoted"},
		{"hash", filepath.Join(dir, ".env"), "DATABASE_URL", "a#b", "cannot be written unquoted"},
		{"space", filepath.Join(dir, ".env"), "DATABASE_URL", "a b", "cannot be written unquoted"},
		{"quote", filepath.Join(dir, ".env"), "DATABASE_URL", "a'b", "cannot be written unquoted"},
		{"empty", filepath.Join(dir, ".env"), "DATABASE_URL", "", "cannot be written unquoted"},
		{"a directory", dir, "DATABASE_URL", uri, "not a regular file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Set(tt.path, tt.key, tt.value)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Set() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Error("a refused Set wrote the file")
	}
}
