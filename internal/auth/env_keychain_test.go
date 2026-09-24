package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/keychain"
)

// clearCredentialEnv stops a developer's exported CI credential from
// deciding a test. testsupport.ClearEnv does the same, but importing
// it here would be a cycle.
func clearCredentialEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvClientID, "")
	t.Setenv(EnvClientSecret, "")
}

func TestEnvCredentials(t *testing.T) {
	tests := []struct {
		name, id, secret string
		want             *Credentials
		wantMissing      string
	}{
		{name: "neither"},
		{name: "both empty", id: "", secret: ""},
		{name: "both", id: "eid", secret: "es",
			want: &Credentials{"eid", "es"}},
		{name: "id only", id: "eid", wantMissing: EnvClientSecret},
		{name: "secret only", secret: "es", wantMissing: EnvClientID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvClientID, tt.id)
			t.Setenv(EnvClientSecret, tt.secret)
			got, err := EnvCredentials()
			if tt.wantMissing != "" {
				var partial *PartialFlagPairError
				if !errors.As(err, &partial) ||
					partial.Missing != tt.wantMissing {
					t.Fatalf("err = %v, want missing %s", err,
						tt.wantMissing)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (got == nil) != (tt.want == nil) ||
				(got != nil && *got != *tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveCredentialsEnvAndKeychain(t *testing.T) {
	const path = "/cfg/config.yaml"
	account := keychain.Account("dev", path)
	idOnly := &config.StarfleetProfile{ClientID: "cid"}
	full := &config.StarfleetProfile{ClientID: "cid", ClientSecret: "cs"}

	tests := []struct {
		name               string
		envID, envSecret   string
		flagID, flagSecret string
		profile            *config.StarfleetProfile
		kc                 keychain.Store
		wantSecret         string
		wantSource         string
		wantErr            error  // matched with errors.Is
		wantPartial        bool   // *PartialFlagPairError
		wantMsg            string // substring of an other error
	}{
		{name: "flags beat env", envID: "eid", envSecret: "es",
			flagID: "fid", flagSecret: "fs", profile: full,
			wantSecret: "fs", wantSource: SourceFlags},
		{name: "env beats profile", envID: "eid", envSecret: "es",
			profile: full, wantSecret: "es", wantSource: SourceEnv},
		{name: "env with no config at all", envID: "eid", envSecret: "es",
			wantSecret: "es", wantSource: SourceEnv},
		{name: "half env pair does not fall through", envID: "eid",
			profile: full, wantPartial: true},
		{name: "file secret wins over keychain", profile: full,
			kc: seeded(account, "kcs"), wantSecret: "cs",
			wantSource: SourceConfig},
		{name: "keychain secret", profile: idOnly,
			kc: seeded(account, "kcs"), wantSecret: "kcs",
			wantSource: SourceKeychain},
		{name: "keychain has no entry", profile: idOnly,
			kc: &keychain.Fake{}, wantErr: ErrNoCredentials},
		{name: "keychain unreachable", profile: idOnly,
			kc:      &keychain.Fake{Err: errors.New("locked")},
			wantMsg: "--insecure-storage"},
		{name: "no keychain wired", profile: idOnly,
			wantMsg: "OS keychain"},
		{name: "no client id", profile: &config.StarfleetProfile{},
			kc: seeded(account, "kcs"), wantErr: ErrNoCredentials},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvClientID, tt.envID)
			t.Setenv(EnvClientSecret, tt.envSecret)
			a := &Auth{Profile: "dev", Module: "starfleet",
				Keychain: tt.kc, ConfigPath: path}
			creds, source, err := a.ResolveCredentials(
				tt.profile, tt.flagID, tt.flagSecret)
			switch {
			case tt.wantPartial:
				var partial *PartialFlagPairError
				if !errors.As(err, &partial) {
					t.Fatalf("err = %v, want a partial pair", err)
				}
				return
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			case tt.wantMsg != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantMsg) ||
					errors.Is(err, ErrNoCredentials) {
					t.Fatalf("err = %v, want one naming %q", err,
						tt.wantMsg)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if creds.ClientSecret != tt.wantSecret || source != tt.wantSource {
				t.Errorf("got %q from %s, want %q from %s",
					creds.ClientSecret, source, tt.wantSecret,
					tt.wantSource)
			}
		})
	}
}

func seeded(account, secret string) *keychain.Fake {
	f := &keychain.Fake{}
	_ = f.Set(account, secret)
	return f
}
