package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/spf13/cobra"
)

func TestNewDatabasePostgRESTCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewDatabasePostgRESTCmd(rt)

	if cmd.Use != "postgrest" {
		t.Errorf("Use = %q, want \"postgrest\"", cmd.Use)
	}

	want := map[string]bool{"deploy": false, "update": false}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("postgrest command missing subcommand %q", name)
		}
	}
}

// newPostgRESTFlagCmd builds a bare command with the PostgREST flags
// bound, then marks the named flags as set — the state
// applyPostgRESTFlags reads to decide what to overlay.
func newPostgRESTFlagCmd(
	t *testing.T, set map[string]string,
) (*cobra.Command, *postgrestServiceOpts) {
	t.Helper()
	opts := &postgrestServiceOpts{}
	cmd := &cobra.Command{Use: "x"}
	bindPostgRESTFlags(cmd, opts)
	for name, value := range set {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatalf("set --%s=%s: %v", name, value, err)
		}
	}
	return cmd, opts
}

func TestApplyPostgRESTFlagsValidation(t *testing.T) {
	tests := []struct {
		name    string
		set     map[string]string
		wantErr string
	}{
		{
			name: "deploy minimum",
			set: map[string]string{
				"db-schemas": "public", "db-anon-role": "web_anon",
			},
		},
		{
			name: "db-pool below range",
			set: map[string]string{
				"db-schemas": "public", "db-anon-role": "web_anon",
				"db-pool": "0",
			},
			wantErr: "--db-pool must be between 1 and 30",
		},
		{
			name: "db-pool above range",
			set: map[string]string{
				"db-schemas": "public", "db-anon-role": "web_anon",
				"db-pool": "31",
			},
			wantErr: "--db-pool must be between 1 and 30",
		},
		{
			name: "db-pool at bounds",
			set: map[string]string{
				"db-schemas": "public", "db-anon-role": "web_anon",
				"db-pool": "30",
			},
		},
		{
			name: "max-rows below range",
			set: map[string]string{
				"db-schemas": "public", "db-anon-role": "web_anon",
				"max-rows": "0",
			},
			wantErr: "--max-rows must be between 1 and 10000",
		},
		{
			name: "max-rows above range",
			set: map[string]string{
				"db-schemas": "public", "db-anon-role": "web_anon",
				"max-rows": "10001",
			},
			wantErr: "--max-rows must be between 1 and 10000",
		},
		{
			name: "jwt-secret too short",
			set: map[string]string{
				"db-schemas": "public", "db-anon-role": "web_anon",
				"jwt-secret": strings.Repeat("a", 31),
			},
			wantErr: "--jwt-secret must be at least 32 characters",
		},
		{
			name: "jwt-secret at minimum",
			set: map[string]string{
				"db-schemas": "public", "db-anon-role": "web_anon",
				"jwt-secret": strings.Repeat("a", 32),
			},
		},
		{
			name:    "no config and no flags",
			set:     map[string]string{},
			wantErr: "--db-schemas and --db-anon-role are required",
		},
		{
			name:    "only schemas supplied",
			set:     map[string]string{"db-schemas": "public"},
			wantErr: "--db-anon-role is required",
		},
		{
			name:    "only anon role supplied",
			set:     map[string]string{"db-anon-role": "web_anon"},
			wantErr: "--db-schemas is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, opts := newPostgRESTFlagCmd(t, tt.set)
			cfg := api.PostgRESTServiceConfig{}
			// The two run in this order in applyPostgRESTService, with
			// the client built between them. Driving both here keeps
			// each case measuring the refusal a deploy really makes,
			// rather than whichever half happens to hold the check
			// today.
			err := validatePostgRESTFlags(cmd, opts, intentDeploy)
			if err == nil {
				err = applyPostgRESTFlags(cmd, opts, &cfg)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q",
					err.Error(), tt.wantErr)
			}
		})
	}
}

func TestApplyPostgRESTFlagsOverlay(t *testing.T) {
	t.Run("sets every optional field", func(t *testing.T) {
		cmd, opts := newPostgRESTFlagCmd(t, map[string]string{
			"db-schemas":         "public,api",
			"db-anon-role":       "web_anon",
			"db-pool":            "20",
			"max-rows":           "500",
			"cors-origins":       "https://a.example",
			"jwt-secret":         strings.Repeat("s", 40),
			"jwt-audience":       "aud",
			"jwt-role-claim-key": ".role",
		})
		cfg := api.PostgRESTServiceConfig{}
		if err := applyPostgRESTFlags(cmd, opts, &cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.DbSchemas != "public,api" {
			t.Errorf("DbSchemas = %q", cfg.DbSchemas)
		}
		if cfg.DbAnonRole != "web_anon" {
			t.Errorf("DbAnonRole = %q", cfg.DbAnonRole)
		}
		if cfg.DbPool == nil || *cfg.DbPool != 20 {
			t.Errorf("DbPool = %v, want 20", cfg.DbPool)
		}
		if cfg.MaxRows == nil || *cfg.MaxRows != 500 {
			t.Errorf("MaxRows = %v, want 500", cfg.MaxRows)
		}
		if cfg.CorsOrigins == nil || *cfg.CorsOrigins != "https://a.example" {
			t.Errorf("CorsOrigins = %v", cfg.CorsOrigins)
		}
		if cfg.JwtSecret == nil {
			t.Error("JwtSecret not set")
		}
		if cfg.JwtAudience == nil || *cfg.JwtAudience != "aud" {
			t.Errorf("JwtAudience = %v", cfg.JwtAudience)
		}
		if cfg.JwtRoleClaimKey == nil || *cfg.JwtRoleClaimKey != ".role" {
			t.Errorf("JwtRoleClaimKey = %v", cfg.JwtRoleClaimKey)
		}
	})

	t.Run("unset flags preserve deployed config", func(t *testing.T) {
		pool, rows := 25, 250
		origins := "https://kept.example"
		aud := "kept-aud"
		cfg := api.PostgRESTServiceConfig{
			DbSchemas:   "kept",
			DbAnonRole:  "kept_role",
			DbPool:      &pool,
			MaxRows:     &rows,
			CorsOrigins: &origins,
			JwtAudience: &aud,
		}

		// An update that touches only --max-rows must leave everything
		// else exactly as the deployed service had it.
		cmd, opts := newPostgRESTFlagCmd(t,
			map[string]string{"max-rows": "999"})
		if err := applyPostgRESTFlags(cmd, opts, &cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.MaxRows == nil || *cfg.MaxRows != 999 {
			t.Errorf("MaxRows = %v, want 999", cfg.MaxRows)
		}
		if cfg.DbSchemas != "kept" || cfg.DbAnonRole != "kept_role" {
			t.Errorf("required fields clobbered: %q / %q",
				cfg.DbSchemas, cfg.DbAnonRole)
		}
		if cfg.DbPool == nil || *cfg.DbPool != 25 {
			t.Errorf("DbPool = %v, want preserved 25", cfg.DbPool)
		}
		if cfg.CorsOrigins == nil || *cfg.CorsOrigins != origins {
			t.Errorf("CorsOrigins = %v, want preserved", cfg.CorsOrigins)
		}
		if cfg.JwtAudience == nil || *cfg.JwtAudience != aud {
			t.Errorf("JwtAudience = %v, want preserved", cfg.JwtAudience)
		}
	})

	t.Run("explicit empty string clears an optional field", func(t *testing.T) {
		origins := "https://old.example"
		cfg := api.PostgRESTServiceConfig{
			DbSchemas: "public", DbAnonRole: "web_anon",
			CorsOrigins: &origins,
		}
		cmd, opts := newPostgRESTFlagCmd(t,
			map[string]string{"cors-origins": ""})
		if err := applyPostgRESTFlags(cmd, opts, &cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.CorsOrigins == nil || *cfg.CorsOrigins != "" {
			t.Errorf("CorsOrigins = %v, want empty string", cfg.CorsOrigins)
		}
	})
}

func TestExistingPostgRESTConfig(t *testing.T) {
	withServices := func(svcs ...api.Service) *api.Database {
		return &api.Database{Services: &svcs}
	}

	t.Run("no services", func(t *testing.T) {
		got := existingPostgRESTConfig(&api.Database{})
		if got.DbSchemas != "" {
			t.Errorf("DbSchemas = %q, want empty", got.DbSchemas)
		}
	})

	t.Run("no postgrest service", func(t *testing.T) {
		db := withServices(api.Service{
			ServiceId:   "svc-1",
			ServiceType: api.ServiceServiceTypeMcp,
		})
		got := existingPostgRESTConfig(db)
		if got.DbSchemas != "" {
			t.Errorf("DbSchemas = %q, want empty", got.DbSchemas)
		}
	})

	t.Run("postgrest service with nil config", func(t *testing.T) {
		db := withServices(api.Service{
			ServiceId:   "svc-1",
			ServiceType: api.ServiceServiceTypePostgrest,
		})
		got := existingPostgRESTConfig(db)
		if got.DbSchemas != "" {
			t.Errorf("DbSchemas = %q, want empty", got.DbSchemas)
		}
	})

	t.Run("postgrest service with config", func(t *testing.T) {
		db := withServices(
			api.Service{
				ServiceId:   "svc-1",
				ServiceType: api.ServiceServiceTypeMcp,
			},
			api.Service{
				ServiceId:   "svc-2",
				ServiceType: api.ServiceServiceTypePostgrest,
				PostgrestConfig: &api.PostgRESTServiceConfig{
					DbSchemas:  "public,api",
					DbAnonRole: "web_anon",
				},
			},
		)
		got := existingPostgRESTConfig(db)
		if got.DbSchemas != "public,api" {
			t.Errorf("DbSchemas = %q, want \"public,api\"", got.DbSchemas)
		}
		if got.DbAnonRole != "web_anon" {
			t.Errorf("DbAnonRole = %q, want \"web_anon\"", got.DbAnonRole)
		}
	})

	t.Run("returns a copy, not the deployed pointer", func(t *testing.T) {
		deployed := &api.PostgRESTServiceConfig{DbSchemas: "public"}
		db := withServices(api.Service{
			ServiceId:       "svc-1",
			ServiceType:     api.ServiceServiceTypePostgrest,
			PostgrestConfig: deployed,
		})
		got := existingPostgRESTConfig(db)
		got.DbSchemas = "mutated"
		if deployed.DbSchemas != "public" {
			t.Errorf("mutating the copy changed the source: %q",
				deployed.DbSchemas)
		}
	})
}

func TestJoinFlagsAndPluralIs(t *testing.T) {
	tests := []struct {
		flags []string
		want  string
		verb  string
	}{
		{[]string{"--a"}, "--a", "is"},
		{[]string{"--a", "--b"}, "--a and --b", "are"},
		{[]string{"--a", "--b", "--c"}, "--a, --b and --c", "are"},
	}
	for _, tt := range tests {
		if got := joinFlags(tt.flags); got != tt.want {
			t.Errorf("joinFlags(%v) = %q, want %q", tt.flags, got, tt.want)
		}
		if got := pluralIs(len(tt.flags)); got != tt.verb {
			t.Errorf("pluralIs(%d) = %q, want %q",
				len(tt.flags), got, tt.verb)
		}
	}
}
