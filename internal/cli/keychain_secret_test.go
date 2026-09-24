package cli

import (
	"errors"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/keychain"
)

func TestHasStarfleetSecretCountsTheKeychain(t *testing.T) {
	rt, _, _ := profileRuntime(t, "json")
	path := rt.Config.SavePath()
	withEntry := &keychain.Fake{}
	_ = withEntry.Set(keychain.Account("prod", path), "s")
	otherFile := &keychain.Fake{}
	_ = otherFile.Set(keychain.Account("prod", "/elsewhere.yaml"), "s")

	tests := []struct {
		name    string
		profile string
		kc      keychain.Store
		want    bool
	}{
		{"file secret", "dev", nil, true},
		{"keychain entry", "prod", withEntry, true},
		{"entry for another config file", "prod", otherFile, false},
		{"no keychain wired", "prod", nil, false},
		{"unreachable keychain", "prod",
			&keychain.Fake{Err: errors.New("locked")}, false},
		{"no client id", "ghost", withEntry, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt.Keychain = tt.kc
			got := hasStarfleetSecret(rt, tt.profile,
				rt.Config.StarfleetProfile(tt.profile))
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfigSavePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	def, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if got := (&config.Config{}).SavePath(); got != def {
		t.Errorf("unsaved config SavePath = %q, want %q", got, def)
	}
	c := &config.Config{}
	p := t.TempDir() + "/c.yaml"
	if err := c.SaveTo(p); err != nil {
		t.Fatal(err)
	}
	if c.SavePath() != p {
		t.Errorf("SavePath = %q, want %q", c.SavePath(), p)
	}
}
