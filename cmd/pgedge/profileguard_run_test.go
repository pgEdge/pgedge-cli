package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/module"
)

// A command written with bare Run must fail a rejected --profile the
// way a RunE command does: with an error, so the process exits non-zero.
func TestProfileGuardFailsABareRun(t *testing.T) {
	cfg := &config.Config{}
	cfg.SetStarfleetProfile("alpha", &config.StarfleetProfile{})
	tests := []struct {
		profile string
		wantErr bool
		wantRan bool
	}{
		{"bogus", true, false},
		{"alpha", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.profile, func(t *testing.T) {
			rt := &module.Runtime{Config: cfg, Profile: tt.profile, ProfileExplicit: true}
			ran := false
			root := &cobra.Command{Use: "pgedge", SilenceUsage: true, SilenceErrors: true}
			root.AddCommand(&cobra.Command{Use: "bare", Run: func(*cobra.Command, []string) { ran = true }})
			wrapProfileGuard(root, rt)
			root.SetArgs([]string{"bare"})
			err := root.Execute()
			if (err != nil) != tt.wantErr || ran != tt.wantRan {
				t.Errorf("Execute() = %v, ran %v; want error %v, ran %v", err, ran, tt.wantErr, tt.wantRan)
			}
		})
	}
}
