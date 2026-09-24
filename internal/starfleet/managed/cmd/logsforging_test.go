package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestForgedLogFieldsCannotAddARecord drives `database logs` end to
// end with a hostile level and message.
//
// This is the stdout half of log forging, and stdout is what makes it
// worse than the stderr sites: a caller parses this stream. managed
// prints time, level and message on ONE line, so a newline in either
// server-controlled field produces a second line with no timestamp and
// no level — which reads as a continuation of a real record rather than
// as injected text.
//
// The assertion counts LINES rather than looking for the escaped
// forms alone. A fix that escaped only the message would still let the
// level forge a record, and a line count catches that where a
// substring check would not.
func TestForgedLogFieldsCannotAddARecord(t *testing.T) {
	// A level and a message that each try to close their own record
	// and open a plausible replacement.
	const forgedLevel = "log\n2026-08-17T17:36:00Z  FATAL  " +
		"database corrupted, halting"
	const forgedMessage = "checkpoint complete\n" +
		"2026-08-17T17:36:01Z  log    all clear\x1b[2K"

	body, err := json.Marshal(map[string]any{
		"logs": []map[string]any{{
			"level":   forgedLevel,
			"message": forgedMessage,
			"time":    1786988160069,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, string(body)))
	if err := runAuthed(t, rt, out, url, "database", "logs",
		testDatabaseID); err != nil {
		t.Fatalf("database logs: %v", err)
	}

	got := out.String()
	// One record in, one line out. Trailing newline aside, anything
	// more is a record the server fabricated.
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("one log record produced %d lines; the server forged "+
			"%d of them:\n%q", len(lines), len(lines)-1, got)
	}
	for _, bad := range []struct{ name, s string }{
		{"newline inside the level", "log\n"},
		{"newline inside the message", "complete\n2026"},
		{"ANSI escape", "\x1b"},
	} {
		if strings.Contains(got, bad.s) {
			t.Errorf("%s survived onto stdout:\n%q", bad.name, got)
		}
	}
	// Escaped, not dropped: losing the text would pass the checks
	// above while destroying the operator's only evidence.
	if !strings.Contains(got, `\n`) {
		t.Errorf("no escaped newline in output, so the value was "+
			"dropped rather than escaped:\n%q", got)
	}
	if !strings.Contains(got, "database corrupted, halting") {
		t.Errorf("the level's text was lost entirely:\n%q", got)
	}
}

// TestLogRecordTimeIsCLIGenerated records why the time column needs no
// escaping of its own, since the code escapes it anyway and a reader
// would otherwise assume the server controls it.
//
// databaseLogRecordsFrom only ever sets Time from an epoch float64
// through time.Format, or leaves the "-" placeholder. A string in the
// "time" key is IGNORED — which is the property under test, because it
// is what makes the column CLI-generated.
func TestLogRecordTimeIsCLIGenerated(t *testing.T) {
	recs := databaseLogRecordsFrom([]map[string]interface{}{
		{"time": "2026-08-17T17:36:00Z\nforged", "level": "log",
			"message": "m"},
		{"time": float64(1786988160069), "level": "log",
			"message": "m"},
	})
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].Time != logRecordMissing {
		t.Errorf("a string time was accepted: %q; the column is "+
			"only CLI-generated because it is not", recs[0].Time)
	}
	if recs[1].Time != "2026-08-17T17:36:00Z" {
		t.Errorf("epoch rendered as %q", recs[1].Time)
	}
}
