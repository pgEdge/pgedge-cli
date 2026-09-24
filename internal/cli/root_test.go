package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "help shows use line",
			args: []string{"--help"},
			want: []string{"pgedge", "Usage:"},
		},
		{
			name: "global flags registered",
			args: []string{"--help"},
			want: []string{"--config", "--profile", "--debug",
				"--verbose", "--output", "--no-color"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCmd(nil)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(tt.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			for _, w := range tt.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output missing %q", w)
				}
			}
		})
	}
}

func TestRootCommandBareInvocationShowsHelp(t *testing.T) {
	// With no subcommand, root's RunE falls through to cmd.Help(),
	// the same as --help but via the default RunE path.
	cmd := NewRootCmd(nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Errorf("bare invocation output missing usage, got %q", out.String())
	}
}
