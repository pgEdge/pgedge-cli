package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestBackupCreateRun(t *testing.T) {
	args := []string{"backup", "create", "--database-id", testDatabaseID,
		"--provider", "pgbackrest", "--type", "full", "--name", "nightly",
		"--target-nodes", "n1"}

	t.Run("success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("backup create: %v", err)
		}
		if !strings.Contains(errb.String(), "Backup initiated") {
			t.Errorf("missing initiated message: %q", errb.String())
		}
	})

	t.Run("invalid database id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url, "backup", "create",
			"--database-id", "bad", "--provider", "pgbackrest"); err == nil {
			t.Fatal("expected error on invalid database id")
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url, args...); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}
