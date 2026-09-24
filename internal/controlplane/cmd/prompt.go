package cmd

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// readLineErr prints the label (with its default, if any) and returns
// the trimmed input plus the underlying read error. Unlike readLine it
// does NOT swallow io.EOF, so callers that loop (promptRequired) can
// stop at end-of-input instead of spinning on repeated empty reads.
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

// readLine prints the label with its default and returns the trimmed
// input, or "" if the user accepted the default with an empty line.
// io.EOF is treated as an empty line so a short scripted input can
// drive a longer interview via defaults.
func readLine(
	in *bufio.Reader, errOut io.Writer, label, def string,
) (string, error) {
	s, err := readLineErr(in, errOut, label, def)
	if err != nil && err != io.EOF {
		return "", err
	}
	return s, nil
}

// promptRequired prints the label and re-prompts until the user enters
// a non-empty value. For fields the API requires, so the interview
// never emits a spec with a blank or placeholder required field. On
// end-of-input (or a read error) with no value it returns that error
// rather than looping forever.
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

// promptOptionalInt prints the label and returns the trimmed answer:
// "" when the user enters a blank line (the field is omitted), the
// canonical integer string when a valid integer is entered, and it
// re-prompts on any non-integer input. Unlike promptInt, a blank line
// means "omit" rather than "use a default" — suited to optional
// numeric fields such as port and retention count.
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
