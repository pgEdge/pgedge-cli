package clitest

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

// goreleaserEnvOnly is goreleaser's own check on a publisher token
// (tmpl.envOnlyRe, v2.18.2). It evaluates the token without its
// template functions, so anything else fails only at publish time:
// v0.5.0-beta.2's cask died on envOrDefault after every build passed.
var goreleaserEnvOnly = regexp.MustCompile(`^{{\s*\.Env\.[^.\s}]+\s*}}$`)

func TestGoreleaserTokensAreSingleEnvReferences(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		t.Fatalf("decode .goreleaser.yaml: %v", err)
	}

	found := 0
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				key, val := n.Content[i], n.Content[i+1]
				if key.Value == "token" && val.Kind == yaml.ScalarNode {
					found++
					if !goreleaserEnvOnly.MatchString(val.Value) {
						t.Errorf(".goreleaser.yaml line %d: token %q must be "+
							"exactly {{ .Env.NAME }}", val.Line, val.Value)
					}
				}
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(&root)

	if found == 0 {
		t.Fatal("no token field found in .goreleaser.yaml; the walk " +
			"is not reading the config")
	}
}
