// Package dotenv sets one variable in a .env file, leaving every other
// line as it was.
package dotenv

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/atomicfile"
)

// safeValue is what Set writes unquoted. It excludes every character a
// dotenv loader treats specially: `$`, which Next.js and Vite expand;
// `#`, which starts a comment; quotes, backslash and whitespace.
var safeValue = regexp.MustCompile(`^[A-Za-z0-9._~%:/@?=&+,;\[\]-]+$`)

var validName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidName reports whether name can be a variable in a .env file.
func ValidName(name string) bool { return validName.MatchString(name) }

// Set writes name=value into path. The last line assigning name is
// replaced, since the last assignment is the one every loader keeps;
// with none, the line is appended. A new file is created at 0600, and
// an existing file keeps its mode. A symbolic link is refused: the file
// receives a live password, and a planted link would send it elsewhere.
func Set(path, name, value string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid variable name %q", name)
	}
	if !safeValue.MatchString(value) {
		return fmt.Errorf("the value for %s cannot be written unquoted", name)
	}

	mode := os.FileMode(0o600)
	var content []byte
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%s is a symbolic link; pass --file with the file it points to", path)
	case !info.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", path)
	default:
		mode = info.Mode().Perm()
		if content, err = os.ReadFile(path); err != nil { //nolint:gosec // G304: the caller's --file
			return err
		}
	}

	return atomicfile.Write(path, merge(content, name, value), mode)
}

func merge(content []byte, name, value string) []byte {
	eol := "\n"
	if bytes.Contains(content, []byte("\r\n")) {
		eol = "\r\n"
	}
	lines := strings.SplitAfter(string(content), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	last := -1
	for i, line := range lines {
		if assigns(line, name) {
			last = i
		}
	}
	if last >= 0 {
		line := lines[last]
		ending := line[len(strings.TrimRight(line, "\r\n")):]
		prefix := ""
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "export ") {
			prefix = "export "
		}
		lines[last] = prefix + name + "=" + value + ending
		if ending == "" {
			lines[last] += eol
		}
		return []byte(strings.Join(lines, ""))
	}

	var b strings.Builder
	b.WriteString(strings.Join(lines, ""))
	if len(lines) > 0 && !strings.HasSuffix(lines[len(lines)-1], "\n") {
		b.WriteString(eol)
	}
	b.WriteString(name + "=" + value + eol)
	return []byte(b.String())
}

// assigns reports whether line sets name, with or without `export`.
func assigns(line, name string) bool {
	s := strings.TrimLeft(line, " \t")
	s = strings.TrimPrefix(s, "export ")
	s = strings.TrimLeft(s, " \t")
	rest, ok := strings.CutPrefix(s, name)
	if !ok {
		return false
	}
	return strings.HasPrefix(strings.TrimLeft(rest, " \t"), "=")
}
