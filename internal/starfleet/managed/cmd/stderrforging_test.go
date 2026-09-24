package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// forgingValue is one server string carrying every shape that forges a
// line: a newline (a whole fabricated line), a tab (a fabricated COLUMN
// in the two-space-separated step format), and an ANSI CSI (which can
// repaint or erase what the CLI already wrote). One value rather than
// three so a fix that handles only the newline still fails here.
//
// The tail is what a forged line would look like if it got through:
// text an operator or an agent would read as the CLI's own report of a
// finished, healthy task.
const forgingValue = "boom\n2026-08-05T20:49:59Z  All steps: " +
	"succeeded (100%)\tSTATUS\tHEALTHY\x1b[2K"

// assertNoRawControls fails if any byte that can forge a line survived
// into what the user sees. It checks the RENDERED text rather than
// calling Sanitize again, so it cannot pass by agreeing with a broken
// implementation of the thing it is testing.
func assertNoRawControls(t *testing.T, what, got string) {
	t.Helper()
	for _, bad := range []struct {
		name string
		s    string
	}{
		{"newline inside the value", "boom\n"},
		{"raw tab", "\t"},
		{"ANSI escape", "\x1b"},
	} {
		if strings.Contains(got, bad.s) {
			t.Errorf("%s: %s survived into output:\n%q",
				what, bad.name, got)
		}
	}
	// The escaped forms must be present, or the value was dropped
	// rather than escaped -- which would pass the checks above while
	// losing the operator's only evidence.
	for _, want := range []string{`\n`, `\t`} {
		if !strings.Contains(got, want) {
			t.Errorf("%s: expected the escaped form %s in:\n%q",
				what, want, got)
		}
	}
}

// TestForgedTaskStepCannotAddALine drives the streaming path a
// mutating verb and `task wait --follow` share. printNewMessages is
// the one writer for both, so a hostile step text reaching stderr
// unescaped forges a line under every following verb at once.
func TestForgedTaskStepCannotAddALine(t *testing.T) {
	rt, _, errb := testsupport.NewRuntime(t, "", "text")

	msgs := []api.Message{{
		Level: "info",
		Text:  forgingValue,
		Time:  "2026-08-05T20:49:50Z",
	}}
	if got := printNewMessages(rt, msgs, 0); got != 1 {
		t.Fatalf("cursor = %d, want 1", got)
	}

	got := errb.String()
	assertNoRawControls(t, "printNewMessages", got)

	// One message must be one line. This is the assertion the defect
	// actually breaks: the forged newline makes it two.
	lines := strings.Count(strings.TrimSuffix(got, "\n"), "\n") + 1
	if lines != 1 {
		t.Errorf("one message rendered %d lines, want 1:\n%q",
			lines, got)
	}
}

// TestForgedTaskErrorCannotAddALine drives the other half: the task's
// own `error` field, which is interpolated into the exit error rather
// than printed by printNewMessages. Neither managed nor byoc enumerates
// that field in its generated client, so nothing constrains its shape.
//
// BOTH routes, because the interpolation is written twice -- task.go
// serves `task wait` and wait.go serves every mutating verb's --wait,
// and each has its own copy of the line. A mutation proved that: with
// only the `task wait` case here, putting the defect back in wait.go
// left this test passing, since the mutation landed where the test
// never runs. TestFollowStepLinesAreByteIdentical exists for the same
// two-copy reason.
func TestForgedTaskErrorCannotAddALine(t *testing.T) {
	// task wait takes no --wait: it IS the wait.
	routes := map[string][]string{
		"task wait (task.go)": {"task", "wait",
			"e9562e00-c8f8-438b-860a-b5eb427f88d8"},
		"mutating verb --wait (wait.go)": {"database", "create",
			"--name", "d", "--region", "us-east-2", "--size", "small",
			"--wait"},
	}

	for name, base := range routes {
		t.Run(name, func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				withCatalog(followFlowHandler("failed", forgingValue)))

			args := append(append([]string{}, base...),
				"--wait-interval", "1", "--wait-timeout", "30")
			err := runAuthed(t, rt, out, url, args...)
			if err == nil {
				t.Fatal("a failed task must not exit 0")
			}

			// The error text is what the user sees on stderr, whether
			// the runtime prints it or the caller does.
			assertNoRawControls(t, "task error", err.Error())
			if s := errb.String(); strings.Contains(s, "\x1b") {
				t.Errorf("ANSI escape reached stderr:\n%q", s)
			}
		})
	}
}

// TestForgedInstanceNameCannotForgeTheTieNote reproduces the original
// probe. The reviewer who found the issue set instance_name to a value
// carrying a newline and a tab and got two fabricated metric rows on
// stdout and two fabricated lines on stderr.
//
// This is the stderr half, and it is the site the first draft of the
// stderr work MISSED: tieNote builds its line with fmt.Sprintf and
// reaches stderr through fmt.Fprint, so neither the mechanical
// Fprintf pass nor TestStderrInterpolationsAreSanitized saw it. The
// note is a composed message whose own trailing newline is
// deliberate, which is exactly why the printer cannot be the
// checkpoint and the builder has to be.
func TestForgedInstanceNameCannotForgeTheTieNote(t *testing.T) {
	cols := []string{"cpu", "instance_name", "time"}
	// In BOTH rows: tieNote renders whichever row newestUsableSample
	// chose, and this test is about the escaping rather than about
	// which row wins.
	s := series(cols,
		[]interface{}{1.0, forgingValue, 5000.0},
		[]interface{}{2.0, forgingValue, 5000.0},
	)

	_, note := newestUsableSample(s)
	if note == "" {
		t.Fatal("two rows at one timestamp must produce a tie note")
	}
	assertNoRawControls(t, "tieNote", note)

	// The note is ONE line and ends with exactly one newline of its
	// own. This is the assertion the defect breaks: the forged newline
	// splits it into two, and the second reads as the CLI's own
	// report.
	if n := strings.Count(note, "\n"); n != 1 {
		t.Errorf("tie note holds %d newlines, want 1 (its own "+
			"terminator):\n%q", n, note)
	}
}

// TestForgedTaskFieldsCannotForgeADetailLine covers the detail block
// printTaskDetail writes to STDOUT, under a table the renderer has
// already sanitized. stdout is the stream a caller parses, so a forged
// line there is worse than one on stderr — it looks exactly like the
// sanitized output above it.
//
// The gate could not see these: the writer is aliased (`out :=
// rt.Output.Out`), which took eight interpolations out of its
// population until it learned to track the alias.
func TestForgedTaskFieldsCannotForgeADetailLine(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")

	status := "failed"
	errText := "the real cause"
	task := api.Task{
		Id:        "e9562e00-c8f8-438b-860a-b5eb427f88d8",
		Name:      "create-managed",
		Status:    status,
		CreatedAt: forgingValue,
		UpdatedAt: "2026-08-05T00:00:01Z",
		Error:     &errText,
		Messages: []api.Message{{
			Level: "info",
			Text:  forgingValue,
			Time:  "2026-08-05T20:49:50Z",
		}},
	}
	if err := printTaskDetail(rt, &task); err != nil {
		t.Fatalf("printTaskDetail: %v", err)
	}

	got := out.String()
	assertNoRawControls(t, "printTaskDetail (stdout)", got)

	// The block is Created / Updated / Step, one line each. A forged
	// newline in Created or in the step text adds a line that reads as
	// one of them.
	for _, field := range []string{"Created", "Updated", "Step"} {
		if n := strings.Count(got, field); n != 1 {
			t.Errorf("%q appears %d times, want 1:\n%q",
				field, n, got)
		}
	}
}
