package cmd

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
)

// series builds a MetricSeries from a column list and rows, so a test
// can vary ORDER and TIME independently. Every shipped fixture has
// strictly increasing timestamps and no duplicates, which is precisely
// why the tie defect shipped: nothing exercised a tie or an
// out-of-order row.
func series(columns []string, rows ...[]interface{}) api.MetricSeries {
	return api.MetricSeries{Columns: columns, Values: rows}
}

// The direct unit test newestUsableSample never had. It was covered
// only end to end, which made a positional-selection change an
// indirect assertion — and this change is exactly that.
func TestNewestUsableSample(t *testing.T) {
	cols := []string{"cpu", "instance_name", "time"}

	t.Run("newest by TIME, not by position", func(t *testing.T) {
		// The row order is reversed against the timestamps. The
		// position walk this replaced would have taken the 2000 row
		// because it is last; the newest sample is the 3000 one.
		s := series(cols,
			[]interface{}{1.0, "db-x-1-1", 3000.0},
			[]interface{}{2.0, "db-x-1-1", 2000.0},
		)
		got, note := newestUsableSample(s)
		if len(got) == 0 || got[2] != 3000.0 {
			t.Fatalf("got %v, want the row at time 3000 — ordering is "+
				"not published, so position cannot decide this", got)
		}
		if note != "" {
			t.Errorf("unexpected note %q for distinct timestamps", note)
		}
	})

	t.Run("ascending order still picks the last", func(t *testing.T) {
		s := series(cols,
			[]interface{}{1.0, "db-x-1-1", 1000.0},
			[]interface{}{2.0, "db-x-1-1", 2000.0},
		)
		got, _ := newestUsableSample(s)
		if len(got) == 0 || got[2] != 2000.0 {
			t.Fatalf("got %v, want the row at time 2000", got)
		}
	})

	t.Run("a tie is reported and one row is shown", func(t *testing.T) {
		// The instance-replacement case: one timestamp, two instances.
		s := series(cols,
			[]interface{}{1.0, "db-x-1-1", 5000.0},
			[]interface{}{2.0, "db-x-2-1", 5000.0},
		)
		got, note := newestUsableSample(s)
		if len(got) == 0 {
			t.Fatal("no sample chosen")
		}
		if !strings.Contains(note, "2 samples share time") {
			t.Errorf("note = %q, want the count of tied samples", note)
		}
		if !strings.Contains(note, "instance_name=") {
			t.Errorf("note = %q, want the instance it showed — that is "+
				"the fact a reader needs to know which one they got",
				note)
		}
		if !strings.Contains(note, "-o json") {
			t.Errorf("note = %q, names no way to see them all", note)
		}
		// It must not claim which instance is the incoming one. The
		// generation suffix is documented nowhere.
		for _, forbidden := range []string{
			"incoming", "retiring", "newer instance", "old instance",
		} {
			if strings.Contains(note, forbidden) {
				t.Errorf("note = %q claims %q; nothing published lets "+
					"it", note, forbidden)
			}
		}
	})

	t.Run("a tie whose newer row is INCOMPLETE is still reported",
		func(t *testing.T) {
			// THE CASE THE DEFECT ACTUALLY TAKES. The incoming
			// instance's scrape has nulls, so completeness — not the
			// tie-break — selects the retiring one. If that produced no
			// note, output would be byte-identical to before the fix in
			// the very mechanism of the tie defect.
			s := series(cols,
				[]interface{}{1.0, "db-x-1-1", 5000.0},
				[]interface{}{nil, "db-x-2-1", 5000.0},
			)
			got, note := newestUsableSample(s)
			if len(got) == 0 || got[0] != 1.0 {
				t.Fatalf("got %v, want the complete row", got)
			}
			if !strings.Contains(note, "2 samples share time") {
				t.Errorf("note = %q — a row EXISTED at this timestamp "+
					"and was skipped as incomplete, which is the "+
					"reported mechanism of the tie defect. Counting only "+
					"same-predicate rows makes this a non-tie and "+
					"prints nothing.", note)
			}
			if !strings.Contains(note, "1 other incomplete") {
				t.Errorf("note = %q, want the skipped count — two "+
					"whole samples and one whole plus one empty are "+
					"different situations for the reader. \"other\" "+
					"matters: the count must exclude the row shown.",
					note)
			}
		})

	t.Run("the count matches what -o json would show",
		func(t *testing.T) {
			// Three rows at one time, two complete. An earlier version
			// counted only same-predicate rows, so it said 2 while
			// pointing the reader at json, which shows 3.
			s := series(cols,
				[]interface{}{1.0, "db-x-1-1", 5000.0},
				[]interface{}{2.0, "db-x-2-1", 5000.0},
				[]interface{}{nil, "db-x-3-1", 5000.0},
			)
			_, note := newestUsableSample(s)
			if !strings.Contains(note, "3 samples share time") {
				t.Errorf("note = %q, want 3 — the number must agree "+
					"with the json it sends the reader to", note)
			}
		})

	t.Run("no time column falls back to position", func(t *testing.T) {
		// A missing column is the API changing shape, not the caller
		// erring, so this degrades rather than failing.
		s := series([]string{"cpu", "mem"},
			[]interface{}{1.0, 10.0},
			[]interface{}{2.0, 20.0},
		)
		got, note := newestUsableSample(s)
		if len(got) == 0 || got[0] != 2.0 {
			t.Fatalf("got %v, want the last row", got)
		}
		if note != "" {
			t.Errorf("note = %q with no time column to tie on", note)
		}
	})

	t.Run("a non-numeric time degrades rather than panicking",
		func(t *testing.T) {
			s := series(cols,
				[]interface{}{1.0, "db-x-1-1", "not-a-number"},
				[]interface{}{2.0, "db-x-1-1", "also-not"},
			)
			got, _ := newestUsableSample(s)
			if len(got) == 0 {
				t.Fatal("no sample chosen for an unparseable time")
			}
		})

	t.Run("ONE unreadable time does not veto the rest of the series",
		func(t *testing.T) {
			// The trailing row's time is a string. The comparator used
			// to answer "not later" whenever EITHER cell was
			// unreadable, and walking backwards made that row `best`
			// first — so nothing could ever displace it and the whole
			// series fell back to the position walk. That silently
			// restored the selection this exists to fix, on one bad
			// cell.
			s := series(cols,
				[]interface{}{1.0, "db-x-1-1", 1000.0},
				[]interface{}{2.0, "db-x-1-1", 3000.0},
				[]interface{}{3.0, "db-x-2-1", "oops"},
			)
			got, note := newestUsableSample(s)
			if len(got) == 0 || got[0] != 2.0 {
				t.Fatalf("got %v, want the row at time 3000 (cpu 2.0). "+
					"An unreadable cell in ANOTHER row must cost that "+
					"row its turn, not disable ordering for the rest — "+
					"taking cpu 3.0 here is the position walk again.",
					got)
			}
			if note != "" {
				t.Errorf("note = %q; only one row has a comparable "+
					"time equal to the chosen one", note)
			}
		})

	t.Run("the fallback pass orders by time around an unreadable cell",
		func(t *testing.T) {
			// Same veto, on the nothing-complete path: every row is
			// partial, and the last one's time is unreadable.
			s := series(cols,
				[]interface{}{nil, "db-x-1-1", 1000.0},
				[]interface{}{nil, "db-x-1-1", 3000.0},
				[]interface{}{nil, "db-x-2-1", "oops"},
			)
			got, _ := newestUsableSample(s)
			if len(got) == 0 || got[2] != 3000.0 {
				t.Fatalf("got %v, want the partial row at time 3000",
					got)
			}
		})

	t.Run("newest COMPLETE beats newer partial", func(t *testing.T) {
		s := series(cols,
			[]interface{}{1.0, "db-x-1-1", 1000.0},
			[]interface{}{nil, "db-x-1-1", 2000.0},
		)
		got, _ := newestUsableSample(s)
		if len(got) == 0 || got[2] != 1000.0 {
			t.Fatalf("got %v, want the complete older sample", got)
		}
	})

	t.Run("nothing complete falls back, by time", func(t *testing.T) {
		s := series(cols,
			[]interface{}{nil, "db-x-1-1", 3000.0},
			[]interface{}{nil, "db-x-1-1", 1000.0},
		)
		got, _ := newestUsableSample(s)
		if len(got) == 0 || got[2] != 3000.0 {
			t.Fatalf("got %v, want the newest partial by time", got)
		}
	})

	t.Run("a tie among PARTIAL rows says the shown one is partial",
		func(t *testing.T) {
			// This subtest asserted only the COUNT, so it was blind to
			// the parenthetical — which said "(2 incomplete, so not
			// shown)" while showing one of the two. Review found that:
			// the sentence told the reader their sample was whole when
			// it was not, which inverts the completeness rule.
			s := series(cols,
				[]interface{}{nil, "db-x-1-1", 5000.0},
				[]interface{}{nil, "db-x-2-1", 5000.0},
			)
			_, note := newestUsableSample(s)
			if !strings.Contains(note, "2 samples share time") {
				t.Errorf("note = %q; the fallback path ties too, and a "+
					"reader mid-replacement is likelier to be on it",
					note)
			}
			if !strings.Contains(note, "the one shown is partial") {
				t.Errorf("note = %q — nothing is complete here, so the "+
					"rendered sample HAS blanks in it and the note is "+
					"the only place that can say so", note)
			}
			if strings.Contains(note, "other incomplete") {
				t.Errorf("note = %q counts the row it is showing among "+
					"the ones it is not showing", note)
			}
		})

	t.Run("the skipped count excludes the row shown",
		func(t *testing.T) {
			// Three at one timestamp: one complete (chosen), two not.
			s := series(cols,
				[]interface{}{1.0, "db-x-1-1", 5000.0},
				[]interface{}{nil, "db-x-2-1", 5000.0},
				[]interface{}{nil, "db-x-3-1", 5000.0},
			)
			got, note := newestUsableSample(s)
			if len(got) == 0 || got[0] != 1.0 {
				t.Fatalf("got %v, want the complete row", got)
			}
			if !strings.Contains(note, "3 samples share time") {
				t.Errorf("note = %q, want all three counted", note)
			}
			if !strings.Contains(note, "2 other incomplete") {
				t.Errorf("note = %q, want 2 — the chosen row is "+
					"complete and must not be counted among the "+
					"skipped ones", note)
			}
		})

	t.Run("a non-numeric time is neither counted nor tied on",
		func(t *testing.T) {
			// tieNote skips a row whose time cell is not a number, so
			// the count can be smaller than what -o json shows. The
			// reference says "every row whose time is a number" for
			// exactly this reason.
			s := series(cols,
				[]interface{}{1.0, "db-x-1-1", 5000.0},
				[]interface{}{2.0, "db-x-2-1", "5000"},
			)
			_, note := newestUsableSample(s)
			if note != "" {
				t.Errorf("note = %q; the second row's time is a "+
					"string, so nothing shares a comparable timestamp "+
					"and there is no tie to report", note)
			}
		})

	t.Run("empty series", func(t *testing.T) {
		got, note := newestUsableSample(series(cols))
		if got != nil || note != "" {
			t.Errorf("got (%v, %q), want (nil, \"\")", got, note)
		}
	})
}

// The note's label must name the row the TABLE rendered. Review mutated
// `sampleCell(s, idx, chosen)` to `..., 0` — a clean build, a clean
// vet, and the whole package green — which makes the note attribute one
// instance's metrics to another. That is worse than no note, and the
// only assertions were that the note contained "instance_name=" at all.
func TestTieNoteNamesTheRenderedInstance(t *testing.T) {
	cols := []string{"cpu", "instance_name", "time"}
	s := series(cols,
		[]interface{}{1.0, "db-x-1-1", 5000.0},
		[]interface{}{2.0, "db-x-2-1", 5000.0},
	)
	row, note := newestUsableSample(s)
	if len(row) == 0 {
		t.Fatal("no sample chosen")
	}
	rendered, ok := row[1].(string)
	if !ok {
		t.Fatalf("instance cell is %T, not a string", row[1])
	}
	want := "instance_name=" + rendered
	if !strings.Contains(note, want) {
		t.Errorf("note = %q, want %q.\nThe note must name the row the "+
			"table rendered; naming the other one attributes this "+
			"instance's metrics to that one, which is worse than "+
			"printing no note at all.", note, want)
	}
}

// Important: WHICH row the non-numeric fallback chooses, not merely
// that it chooses one. The doc comment claims it "degrades to the
// position walk", and review mutated the comparator to return true
// unconditionally — which degrades to the FIRST row instead, with
// every test green because the only assertion was len(got) != 0.
func TestNonNumericTimeFallsBackToThePositionWalk(t *testing.T) {
	cols := []string{"cpu", "instance_name", "time"}
	s := series(cols,
		[]interface{}{1.0, "db-x-1-1", "not-a-number"},
		[]interface{}{2.0, "db-x-1-1", "also-not"},
	)
	got, note := newestUsableSample(s)
	if len(got) == 0 {
		t.Fatal("no sample chosen for an unparseable time")
	}
	if got[0] != 2.0 {
		t.Errorf("got %v, want the LAST row (cpu 2.0). With no "+
			"comparable time the documented behaviour is the position "+
			"walk, which takes the last row — not the first.", got)
	}
	if note != "" {
		t.Errorf("note = %q; with no comparable time there is no tie "+
			"to report", note)
	}
}

// The comparator itself, which had no direct test at all — review's
// finding. Every combination of readable and unreadable, in both
// directions, because the ASYMMETRY is the whole behaviour: an
// unreadable cell must lose, and losing must not be contagious.
func TestSampleTimeOutranks(t *testing.T) {
	cols := []string{"time"}
	s := series(cols,
		[]interface{}{1000.0},  // 0: readable, older
		[]interface{}{3000.0},  // 1: readable, newer
		[]interface{}{"oops"},  // 2: unreadable
		[]interface{}{"worse"}, // 3: unreadable
	)
	cases := []struct {
		name string
		i, j int
		want bool
	}{
		{"a later time outranks an earlier one", 1, 0, true},
		{"an earlier time does not", 0, 1, false},
		{"an equal time does not, so a tie keeps j", 0, 0, false},
		{"a readable time outranks an unreadable one", 0, 2, true},
		{"an unreadable time never outranks a readable one", 2, 1, false},
		{"neither readable keeps j, which is the position walk",
			2, 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sampleTimeOutranks(s, 0, tc.i, tc.j); got != tc.want {
				t.Errorf("sampleTimeOutranks(i=%d, j=%d) = %v, want %v",
					tc.i, tc.j, got, tc.want)
			}
		})
	}
}

// Both panic guards, which were the only uncovered branches in the new
// code. CLAUDE.md forbids panic in production code, and each guard is
// the single thing standing between a short row and an index panic.
func TestShortRowsDoNotPanic(t *testing.T) {
	t.Run("time column beyond the row", func(t *testing.T) {
		// sampleTime's timeIdx >= len(row) guard. Without it this
		// panics: index out of range [2] with length 1.
		s := series([]string{"cpu", "instance_name", "time"},
			[]interface{}{1.0},
			[]interface{}{2.0, "db-x-1-1", 5000.0},
		)
		got, _ := newestUsableSample(s)
		if len(got) != 3 {
			t.Errorf("got %v, want the complete row", got)
		}
	})

	t.Run("instance column beyond the chosen row", func(t *testing.T) {
		// sampleCell's guard, reachable when instance_name sits AFTER
		// time and the chosen row is short. The rows tie on time, so
		// tieNote runs and reaches for a cell that is not there.
		s := series([]string{"cpu", "time", "instance_name"},
			[]interface{}{1.0, 5000.0},
			[]interface{}{2.0, 5000.0},
		)
		got, note := newestUsableSample(s)
		if len(got) == 0 {
			t.Fatal("no sample chosen")
		}
		// The note is still due -- two rows share the time -- but it
		// cannot name an instance it does not have.
		if !strings.Contains(note, "samples share time") {
			t.Errorf("note = %q, want the tie", note)
		}
		if strings.Contains(note, "instance_name=") {
			t.Errorf("note = %q names an instance the chosen row does "+
				"not carry", note)
		}
	})
}

// tieNoteFixture is one series whose note the reference quotes.
type tieNoteFixture struct {
	name string
	s    api.MetricSeries
}

// tieNoteQuoteFixtures is the ONE table both quote gates read.
//
// They kept a copy each, and the copies are what review flagged: the
// stale-quote gate's idea of "producible" comes from ITS list, so a
// shape added only there widens what the reference may quote without
// the verbatim gate ever requiring it — and a shape added only to the
// verbatim gate's list makes the stale-quote gate report the very
// quote its sibling just demanded. Neither is possible from one table.
//
// Every fixture here MUST tie, and every note it produces MUST be
// quoted in the reference: that is the coupling. Adding a row is
// therefore a decision to quote another note, which is the right price
// for widening the set.
func tieNoteQuoteFixtures() []tieNoteFixture {
	cols := []string{
		"cpu_seconds_total", "instance_name", "memory_used_bytes", "time",
	}
	return []tieNoteFixture{
		{"both complete", series(cols,
			[]interface{}{270.734698, "db-1-1", 931725312.0, 1787168130000.0},
			[]interface{}{283.303779, "db-2-1", 930353152.0, 1787168130000.0},
		)},
		{"one incomplete", series(cols,
			[]interface{}{270.734698, "db-1-1", 931725312.0, 1787168130000.0},
			[]interface{}{nil, "db-2-1", nil, 1787168130000.0},
		)},
		// The THIRD fence, and it was ungated: review reworded the
		// clause and the whole package stayed green while the reference
		// quoted a line the CLI never prints.
		{"none complete", series(cols,
			[]interface{}{nil, "db-1-1", nil, 1787168130000.0},
			[]interface{}{nil, "db-2-1", nil, 1787168130000.0},
		)},
	}
}

// producedTieNotes runs every fixture and returns the notes it printed,
// trailing newline trimmed. A fixture that stops tying, or produces an
// empty note, is fatal: both gates compare against this slice, so a
// silent shrink here would leave them passing without comparing
// anything.
func producedTieNotes(t *testing.T) []string {
	t.Helper()
	fixtures := tieNoteQuoteFixtures()
	out := make([]string, 0, len(fixtures))
	for _, f := range fixtures {
		_, note := newestUsableSample(f.s)
		note = strings.TrimSuffix(note, "\n")
		if note == "" {
			t.Fatalf("fixture %q produced no note; it no longer ties, "+
				"and both quote gates would then compare against a "+
				"short list", f.name)
		}
		out = append(out, note)
	}
	return out
}

// managedReference reads the module reference both gates check: the
// llms.txt index plus every resource page under ../llms/, since the
// reference split moved the notes' sample output into database.txt.
func managedReference(t *testing.T) string {
	t.Helper()
	return managedReferenceText(t)
}

// referenceLabel names the document that QUOTES the notes, for error
// messages. It is no longer one file — see managedReferenceText — so
// this is a label, not a path.
//
// An earlier version of the verbatim gate joined ../llms.txt with
// docs/changelog.md and used Contains over the pair, which required
// only ONE of them to carry each note. Review gutted llms.txt, moved
// the strings into the changelog, and the gate went green — while the
// document agents actually read quoted nothing. The changelog is
// append-only, so a single historical entry would have satisfied it
// forever.
//
// The changelog is not read by either gate now. It DESCRIBES the
// strings rather than quoting them, and it also quotes, deliberately,
// one note the code no longer produces — the sentence a later fix
// removed. Scanning it for stale quotes would report that history as a
// defect. One place quotes; that place is pinned here.
const referenceLabel = "the module reference (../llms.txt + ../llms/**)"

// The reference QUOTES this note as sample output, and review found the
// quoted text did not match what the code prints — wrapped across two
// lines, and naming the instance the tie-break would not have chosen.
// A quoted sample nothing runs is the defect class this repo keeps
// finding, so run it.
//
// This is not two copies agreeing: the reference is quoting literal
// output, so the only way to keep it true is to produce the output and
// look. If the wording changes deliberately, this test names the file
// to update.
func TestReferenceQuotesTheNoteVerbatim(t *testing.T) {
	ref := managedReference(t)
	for _, f := range tieNoteQuoteFixtures() {
		t.Run(f.name, func(t *testing.T) {
			_, note := newestUsableSample(f.s)
			note = strings.TrimSuffix(note, "\n")
			if note == "" {
				t.Fatal("no note produced; the fixture no longer ties")
			}
			if !strings.Contains(ref, note) {
				t.Errorf("%s does not quote this note verbatim.\n"+
					"produced: %q\nThe reference quotes it as sample "+
					"output, so a mismatch teaches an agent a line the "+
					"CLI never prints. Update the quoted block, and "+
					"keep it on ONE line — a hard wrap in the fence is "+
					"itself a mismatch.", referenceLabel, note)
			}
		})
	}
}

// tieNoteMarker is the fragment every note carries, and the hook the
// stale-quote scan looks for. It deliberately excludes the count, so
// changing the number cannot hide a quote from the scan.
//
// The cost is that prose cannot use this phrasing descriptively
// without producing the string it describes. That is accepted: no
// scanned document has such a sentence, and a stale quote has shipped
// twice.
const tieNoteMarker = "samples share time"

// scanDoc is one document the stale-quote scan reads: a label for
// error messages, and how to load its text. The reference is loaded by
// walking index+pages (managedReferenceText); the skill stays a single
// file.
type scanDoc struct {
	label string
	text  func(t *testing.T) string
}

// readFileOrFail loads path as a scanDoc's text function, failing the
// test rather than returning an error a caller could ignore.
func readFileOrFail(path string) func(t *testing.T) string {
	return func(t *testing.T) string {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
}

// staleNoteScanDocs is every document the stale-quote scan reads.
//
// The verbatim gate reads the reference ALONE, and deliberately:
// joining a second document let review satisfy it from the changelog
// while the reference itself quoted nothing. This direction is the
// opposite — a stale quote misleads wherever it sits, so reading more
// documents only strengthens it, and the skill is the other file an
// agent believes. Moving a fabricated note into SKILL.md was the next
// bypass of the reference-only scan, so it is closed here rather than
// left to be found.
//
// docs/changelog.md is excluded on purpose. It quotes, as history, a
// note a later fix REMOVED — the one that said "(2 incomplete, so
// not shown)" while showing one of the two — so scanning it would
// report a corrected entry as a defect. The changelog records what
// changed; the reference states what the code does now.
var staleNoteScanDocs = []scanDoc{
	{referenceLabel, managedReferenceText},
	{"../../../../skills/pgedge-managed/SKILL.md",
		readFileOrFail("../../../../skills/pgedge-managed/SKILL.md")},
}

// noteSpan is a half-open byte range within one line.
type noteSpan struct{ lo, hi int }

// noteSpans returns the range of every occurrence of every produced
// note in line. Overlapping starts are all recorded — the scan asks
// only whether a marker hit lies inside one.
func noteSpans(line string, produced []string) []noteSpan {
	var out []noteSpan
	for _, n := range produced {
		for off := 0; off < len(line); {
			k := strings.Index(line[off:], n)
			if k < 0 {
				break
			}
			lo := off + k
			out = append(out, noteSpan{lo, lo + len(n)})
			off = lo + 1
		}
	}
	return out
}

// coveredMarkers counts the marker occurrences in line that sit inside
// one of the produced notes.
func coveredMarkers(line string, produced []string) int {
	spans := noteSpans(line, produced)
	var n int
	for off := 0; off < len(line); {
		k := strings.Index(line[off:], tieNoteMarker)
		if k < 0 {
			break
		}
		at := off + k
		off = at + len(tieNoteMarker)
		for _, s := range spans {
			if at >= s.lo && at < s.hi {
				n++
				break
			}
		}
	}
	return n
}

// staleNoteQuotes reports every line of doc carrying tieNoteMarker that
// the code cannot produce, and returns how many marker occurrences it
// examined so a caller can prove the scan looked at something.
//
// TWO RULES, because a fence and a sentence claim different things.
// Inside a fenced block the line claims to BE the output, so it must
// equal a produced note once surrounding space is off: that is what
// catches a correct quote with a fabricated tail, and a hard wrap,
// which are the same defect wearing different clothes. In prose the
// marker only has to sit inside a produced note, so an inline mention
// survives whatever surrounds it.
//
// Neither rule anchors. The first version of this scan used
// `(?m)^\d+ samples share time`, and review's bypass was one leading
// space; re-anchoring on `^\s*` would have moved the hole out one
// level rather than closing it, since a quote introduced mid-sentence
// escapes an indent-tolerant anchor exactly as an indented one escaped
// a column-zero anchor.
func staleNoteQuotes(
	doc string, produced []string,
) (reports []string, hits int) {
	inFence := false
	for n, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		found := strings.Count(line, tieNoteMarker)
		if found == 0 {
			continue
		}
		hits += found
		if inFence {
			if slices.Contains(produced, strings.TrimSpace(line)) {
				continue
			}
		} else if coveredMarkers(line, produced) == found {
			continue
		}
		reports = append(reports, fmt.Sprintf(
			"line %d quotes a note the code cannot produce:\n  %q",
			n+1, line))
	}
	return reports, hits
}

// The INVERSE, and it is the half that matters. Contains can only catch
// a quote that is MISSING; the defect review found twice was a quote
// that was STALE — an extra wrong string, not an absent right one. So
// scan the reference for anything shaped like the note and require each
// to be something the code actually produces.
func TestReferenceQuotesNoNoteTheCodeCannotProduce(t *testing.T) {
	produced := producedTieNotes(t)
	var total int
	for _, doc := range staleNoteScanDocs {
		raw := doc.text(t)
		reports, hits := staleNoteQuotes(raw, produced)
		total += hits
		for _, r := range reports {
			t.Errorf("%s: %s\nproduced:\n  %s\nA stale quote is the "+
				"defect this gate exists for — Contains alone only "+
				"catches a MISSING quote, and review found a stale one "+
				"twice.", doc.label, r, strings.Join(produced, "\n  "))
		}
	}
	if total < len(produced) {
		t.Fatalf("%d quote-shaped fragment(s) found across %d "+
			"document(s), but the code produces %d notes the sibling "+
			"gate requires the reference to quote; the marker or the "+
			"fixtures have broken and this gate is passing without "+
			"comparing anything",
			total, len(staleNoteScanDocs), len(produced))
	}
	t.Logf("examined %d quote-shaped fragment(s) across %d document(s)",
		total, len(staleNoteScanDocs))
}

// The gate's own cover. Review's bypass on the first version was
// deleting one character — a leading space — so the cases that matter
// are the ones where the quote is not at column zero and not alone on
// its line. Every case below FAILED to be reported by the anchored
// version.
func TestStaleNoteScanIgnoresIndentAndSurroundingProse(t *testing.T) {
	printed := "2 samples share time 5000; the API publishes no order " +
		"between them. Use -o json to read them all."
	fake := "9 samples share time 5000; the API publishes no order " +
		"between them. Use -o json to read them all."
	produced := []string{printed}

	cases := []struct {
		name string
		doc  string
		want int
	}{
		{"a correct quote at column zero", "```text\n" + printed + "\n```\n", 0},
		{"a correct quote indented", "    " + printed + "\n", 0},
		{"a correct quote inside prose",
			"It prints " + printed + " and stops.\n", 0},
		{"a stale quote at column zero", fake + "\n", 1},
		{"a stale quote indented by one space", " " + fake + "\n", 1},
		{"a stale quote indented by a fence", "    " + fake + "\n", 1},
		{"a stale quote inside prose",
			"The note reads " + fake + " in that case.\n", 1},
		{"a stale quote in a list item", "- " + fake + "\n", 1},
		{"a stale quote in a json fence",
			"```json\n" + fake + "\n```\n", 1},
		// A hard wrap inside the fence is itself a mismatch: the
		// fragment on the first line is no longer a note the code
		// prints, so the scan must report it.
		{"a correct quote hard-wrapped in a fence",
			"```text\n" +
				strings.Replace(printed, "no order ", "no order\n", 1) +
				"\n```\n", 1},
		// THE FENCE RULE, and the hole it closes. Containment alone
		// accepts a correct quote with a fabricated tail, because the
		// printed note is still a substring. A fenced line claims to BE
		// the output, so it must equal one.
		{"a fenced quote with a fabricated tail",
			"```text\n" + printed + " All 4 are shown.\n```\n", 1},
		// In PROSE the same tail is allowed, and that is the scoped
		// cost of letting a sentence mention the note inline.
		{"a prose quote with a trailing sentence",
			printed + " All 4 are shown.\n", 0},
		{"both a correct and a stale quote",
			printed + "\n" + fake + "\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reports, hits := staleNoteQuotes(tc.doc, produced)
			if hits == 0 {
				t.Fatal("the scan found no quote-shaped fragment, so " +
					"this case proves nothing about it")
			}
			if len(reports) != tc.want {
				t.Errorf("got %d report(s), want %d: %v",
					len(reports), tc.want, reports)
			}
		})
	}
}

// A marker hit must be judged against the notes the CODE produced, not
// against any plausible-looking sibling. Widening the fixture table is
// the only way to widen what the reference may quote, and that is why
// there is one table.
func TestStaleNoteScanIsNotSatisfiedByAnUnrelatedNote(t *testing.T) {
	produced := producedTieNotes(t)
	doc := "3 samples share time 1787168130000; the API publishes no " +
		"order between them. Use -o json to read them all.\n"
	reports, hits := staleNoteQuotes(doc, produced)
	if hits != 1 {
		t.Fatalf("examined %d fragments, want 1", hits)
	}
	if len(reports) != 1 {
		t.Fatalf("got %d reports, want 1 — a count no fixture "+
			"produces was accepted: %v", len(reports), reports)
	}
}
