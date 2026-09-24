package cmd

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// readLineErr keeps io.EOF, unlike readLine, so a looping caller
// (promptRequired) stops at end of input instead of spinning.
func readLineErr(
	in *bufio.Reader, errOut io.Writer, label, def string,
) (string, error) {
	if def != "" {
		fmt.Fprintf(errOut, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(errOut, "%s: ", label)
	}
	line, err := in.ReadString('\n')
	return strings.TrimSpace(line), err
}

// readLine treats io.EOF as an empty line so a short scripted input can
// drive a longer interview through defaults.
func readLine(
	in *bufio.Reader, errOut io.Writer, label, def string,
) (string, error) {
	s, err := readLineErr(in, errOut, label, def)
	if err != nil && err != io.EOF {
		return "", err
	}
	return s, nil
}

// promptRequired re-prompts until it gets a value, so the interview
// never emits a blank required field.
func promptRequired(
	in *bufio.Reader, errOut io.Writer, label string,
) (string, error) {
	for {
		s, err := readLineErr(in, errOut, label, "")
		if s != "" {
			return s, nil
		}
		if err != nil {
			return "", err
		}
		fmt.Fprintln(errOut, "  a value is required")
	}
}

func promptString(
	in *bufio.Reader, errOut io.Writer, label, def string,
) (string, error) {
	s, err := readLine(in, errOut, label, def)
	if err != nil {
		return "", err
	}
	if s == "" {
		return def, nil
	}
	return s, nil
}

func promptInt(
	in *bufio.Reader, errOut io.Writer, label string, def int,
) (int, error) {
	for {
		s, err := readLine(in, errOut, label, strconv.Itoa(def))
		if err != nil {
			return 0, err
		}
		if s == "" {
			return def, nil
		}
		n, cerr := strconv.Atoi(s)
		if cerr != nil {
			fmt.Fprintf(errOut, "  not a number: %q\n", s)
			continue
		}
		return n, nil
	}
}

// promptOptionalInt differs from promptInt in that a blank line means
// "omit the field", not "use a default".
func promptOptionalInt(
	in *bufio.Reader, errOut io.Writer, label string,
) (string, error) {
	for {
		s, err := readLine(in, errOut, label, "")
		if err != nil {
			return "", err
		}
		if s == "" {
			return "", nil
		}
		n, cerr := strconv.Atoi(s)
		if cerr != nil {
			fmt.Fprintf(errOut, "  not a number: %q\n", s)
			continue
		}
		return strconv.Itoa(n), nil
	}
}

func promptYesNo(
	in *bufio.Reader, errOut io.Writer, label string, def bool,
) (bool, error) {
	d := "y/N"
	if def {
		d = "Y/n"
	}
	s, err := readLine(in, errOut, label, d)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(s) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func promptEnum(
	in *bufio.Reader, errOut io.Writer, label string,
	choices []string, def string,
) (string, error) {
	for {
		s, err := promptString(in, errOut,
			fmt.Sprintf("%s (%s)", label, strings.Join(choices, "/")),
			def)
		if err != nil {
			return "", err
		}
		for _, c := range choices {
			if s == c {
				return s, nil
			}
		}
		fmt.Fprintf(errOut, "  choose one of: %s\n",
			strings.Join(choices, ", "))
	}
}
