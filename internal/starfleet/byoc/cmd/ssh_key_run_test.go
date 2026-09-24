package cmd

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"golang.org/x/crypto/ssh"
)

const testSSHKeyID = "77777777-8888-9999-0000-aaaabbbbcccc"

const sshKeyBody = `{"id":"` + testSSHKeyID + `","name":"laptop",` +
	`"public_key":"ssh-ed25519 AAAA","created_at":"2024-03-15T10:30:00Z"}`

func TestSSHKeyListRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[`+sshKeyBody+`]`))
		if err := runAuthed(t, rt, out, url, "ssh-key", "list"); err != nil {
			t.Fatalf("ssh-key list: %v", err)
		}
		if !strings.Contains(out.String(), "laptop") {
			t.Errorf("missing key name: %q", out.String())
		}
	})

	t.Run("empty json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url, "ssh-key", "list"); err != nil {
			t.Fatalf("ssh-key list json: %v", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url, "ssh-key", "list"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestSSHKeyGetRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, sshKeyBody))
		if err := runAuthed(t, rt, out, url,
			"ssh-key", "get", testSSHKeyID); err != nil {
			t.Fatalf("ssh-key get: %v", err)
		}
		if !strings.Contains(out.String(), "laptop") {
			t.Errorf("missing name: %q", out.String())
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, sshKeyBody))
		if err := runAuthed(t, rt, out, url,
			"ssh-key", "get", "bad"); err == nil {
			t.Fatal("expected error on invalid id")
		}
	})
}

func TestSSHKeyCreateRun(t *testing.T) {
	args := []string{"ssh-key", "create", "--name", "laptop",
		"--public-key", testPublicKey(t)}

	t.Run("text success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, sshKeyBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("ssh-key create: %v", err)
		}
		if !strings.Contains(errb.String(), "created") {
			t.Errorf("missing created message: %q", errb.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, sshKeyBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("ssh-key create json: %v", err)
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

func TestSSHKeyDeleteRun(t *testing.T) {
	t.Run("force success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"ssh-key", "delete", testSSHKeyID, "--force"); err != nil {
			t.Fatalf("ssh-key delete: %v", err)
		}
		if !strings.Contains(errb.String(), "deleted") {
			t.Errorf("missing deleted message: %q", errb.String())
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"ssh-key", "delete", "bad", "--force"); err == nil {
			t.Fatal("expected error on invalid id")
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"ssh-key", "delete", testSSHKeyID); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})
}

// testPublicKey is a real ed25519 public key in authorized_keys form.
// ssh-key create parses --public-key before sending, so a
// placeholder like "ssh-ed25519 AAAA" no longer reaches the handler
// these fixtures exercise.
func testPublicKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap key: %v", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}

// fixturePublicKey is the same thing for the table literals that are
// built at package scope, where there is no *testing.T to fail on.
var fixturePublicKey = func() string {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err) // test fixture; a failure here is not a CLI path
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}()
