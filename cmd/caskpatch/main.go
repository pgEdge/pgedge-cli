// Command caskpatch adds a postflight_steps block to the Homebrew cask
// goreleaser writes, so a brew-installed macOS binary has its
// quarantine attribute cleared. The binary is not notarized, and with
// the attribute set macOS refuses it as "damaged and cannot be opened".
//
//	go run ./cmd/caskpatch -in dist/homebrew/Casks/pgedge.rb -out dist/tap/Casks/pgedge.rb
//
// goreleaser's own hooks can only emit the deprecated postflight block,
// which makes brew install print a warning, and its custom_block lands
// at the top of the cask, where brew style rejects the stanza order.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// postflightSteps is the block the tap's cask was hand-fixed with and
// installed from before this command existed.
const postflightSteps = `
  postflight_steps do
    on_macos do
      run "/usr/bin/xattr",
          args:           ["-dr", "com.apple.quarantine", "pgedge"],
          chdir:          ".",
          writable_paths: ["pgedge"]
    end
  end
`

// flightStanzas are the hooks a cask can already carry. Any of them
// means goreleaser's output changed shape, so the patch refuses rather
// than guessing where the block goes.
var flightStanzas = []string{"preflight", "postflight", "uninstall_preflight", "uninstall_postflight"}

// heredocStart matches a Ruby heredoc opener such as <<~EOS, whose body
// runs to a line holding only EOS.
var heredocStart = regexp.MustCompile(`<<[~-]?([A-Z_]+)`)

func main() {
	in := flag.String("in", "dist/homebrew/Casks/pgedge.rb", "cask goreleaser wrote")
	out := flag.String("out", "dist/tap/Casks/pgedge.rb", "where to write the patched cask")
	flag.Parse()

	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "caskpatch:", err)
		os.Exit(1)
	}
}

func run(in, out string) error {
	raw, err := os.ReadFile(in) //nolint:gosec // G304: path is the caller's -in
	if err != nil {
		return err
	}
	patched, err := patch(string(raw))
	if err != nil {
		return err
	}
	return os.WriteFile(out, []byte(patched), 0o644) //nolint:gosec // G306: a cask is public
}

// patch inserts postflightSteps after the cask's last top-level
// artifact line (binary or a *_completion), which is where brew style's
// stanza order puts it. Top-level only, so a line inside an on_arm
// block or a caveats heredoc is never the insertion point.
func patch(cask string) (string, error) {
	lines := strings.SplitAfter(cask, "\n")
	last := -1
	heredocEnd := ""
	for i, line := range lines {
		if heredocEnd != "" {
			if strings.TrimSpace(line) == heredocEnd {
				heredocEnd = ""
			}
			continue
		}
		if m := heredocStart.FindStringSubmatch(line); m != nil {
			heredocEnd = m[1]
		}
		word := firstWord(line)
		for _, s := range flightStanzas {
			if word == s || word == s+"_steps" {
				return "", fmt.Errorf("cask already has a %s stanza on line %d", word, i+1)
			}
		}
		topLevel := strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ")
		if topLevel && (word == "binary" || strings.HasSuffix(word, "_completion")) {
			last = i
		}
	}
	if last < 0 {
		return "", errors.New("cask has no binary or completion line to follow")
	}
	head := strings.Join(lines[:last+1], "")
	return head + postflightSteps + strings.Join(lines[last+1:], ""), nil
}

func firstWord(line string) string {
	f := strings.Fields(line)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}
