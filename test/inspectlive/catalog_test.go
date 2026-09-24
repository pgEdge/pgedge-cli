package inspectlive_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/inspect"
)

// TestEveryAnalysisHasALiveTest walks the vocabulary and requires each
// name to appear as a string literal in this package's test files, so
// an analysis cannot be added without a test here naming it. It needs
// no server and runs under `make test`.
func TestEveryAnalysisHasALiveTest(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no test files found: %v", err)
	}
	seen := map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		node, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(node, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil {
					seen[v] = true
				}
			}
			return true
		})
	}
	// A name here would satisfy the gate by itself, so the control is
	// a count: a scan that saw the files finds far more literals.
	if len(seen) < 50 {
		t.Fatalf("the scan found only %d string literals", len(seen))
	}
	for _, name := range inspect.Names() {
		if !seen[name] {
			t.Errorf("analysis %q has no live test naming it", name)
		}
	}
}

// The tables setup created, which is every user table on a fresh
// server and the whole of table-sizes' rows in the fixture schema.
var fixtureTables = []string{
	"bloated", "calls_marker", "deleted", "locked", "replicated",
	"scanned", "sized", "vacuumed",
}

func TestTableSizes(t *testing.T) {
	requireLive(t)
	got, want := stable(t, pub, "table-sizes", `
		SELECT c.relname, pg_size_pretty(pg_total_relation_size(c.oid)),
		  pg_size_pretty(pg_table_size(c.oid)), pg_size_pretty(pg_indexes_size(c.oid))
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = '`+schema+`' AND c.relkind = 'r'`)
	got = inSchema(got)
	if len(got) != len(fixtureTables) {
		t.Fatalf("want %d fixture tables, got %d: %v", len(fixtureTables), len(got), got)
	}
	for _, w := range want {
		r := find(t, got, "table", w[0])
		if r["size"] != w[1] || r["table_size"] != w[2] || r["index_size"] != w[3] {
			t.Errorf("%s: got size=%q table_size=%q index_size=%q, want %q %q %q",
				w[0], r["size"], r["table_size"], r["index_size"], w[1], w[2], w[3])
		}
	}
	// The fixture, not the reference, says which column is which: a
	// 200-byte pad against three integer indexes, and a table with no
	// index at all.
	sized := find(t, got, "table", "sized")
	if idx := parseSize(t, sized["index_size"]); idx <= 0 ||
		parseSize(t, sized["table_size"]) <= idx ||
		parseSize(t, sized["size"]) < parseSize(t, sized["table_size"]) {
		t.Errorf("sized: size %q, table_size %q, index_size %q are not ordered",
			sized["size"], sized["table_size"], sized["index_size"])
	}
	if v := find(t, got, "table", "bloated")["index_size"]; v != "0 bytes" {
		t.Errorf("bloated has no index, index_size %q", v)
	}
	assertNonIncreasing(t, got, "size")
}

func TestIndexSizes(t *testing.T) {
	requireLive(t)
	got, want := stable(t, pub, "index-sizes", `
		SELECT t.relname, i.relname, pg_size_pretty(pg_relation_size(i.oid))
		FROM pg_index x
		JOIN pg_class i ON i.oid = x.indexrelid
		JOIN pg_class t ON t.oid = x.indrelid
		JOIN pg_namespace n ON n.oid = i.relnamespace
		WHERE n.nspname = '`+schema+`'`)
	got = inSchema(got)
	if len(got) != 6 {
		t.Fatalf("want 6 fixture indexes, got %d: %v", len(got), got)
	}
	for _, w := range want {
		r := find(t, got, "index", w[1])
		if r["table"] != w[0] || r["size"] != w[2] {
			t.Errorf("%s: got table=%q size=%q, want %q %q", w[1], r["table"], r["size"], w[0], w[2])
		}
	}
	// 20000 sequential keys inserted one by one outsizes any other
	// fixture index, so the largest-first order puts sized_pkey first.
	if got[0]["index"] != "sized_pkey" {
		t.Errorf("first row is %q, want sized_pkey", got[0]["index"])
	}
	assertNonIncreasing(t, got, "size")
}

func TestUnusedIndexes(t *testing.T) {
	requireLive(t)
	base := refInt(t, pub, `SELECT idx_scan FROM pg_stat_user_indexes WHERE indexrelname = 'sized_tag_idx'`)
	exec1(t, pub, `SET enable_seqscan = off`)
	for range 3 {
		exec1(t, pub, `SELECT count(*) FROM `+schema+`.sized WHERE tag = -1`)
	}
	exec1(t, pub, `RESET enable_seqscan`)
	flush(t, pub)
	waitFor(t, "sized_tag_idx scan count", func() (bool, error) {
		n := refInt(t, pub, `SELECT idx_scan FROM pg_stat_user_indexes WHERE indexrelname = 'sized_tag_idx'`)
		return n == base+3, nil
	})

	got := inSchema(rows(t, pub, "unused-indexes"))
	// Every primary key is unique and out; the two plain indexes on
	// sized are in, one scanned three times and one never.
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d: %v", len(got), got)
	}
	tag := find(t, got, "index", "sized_tag_idx")
	v2 := find(t, got, "index", "sized_v2_idx")
	if tag["scans"] != strconv.FormatInt(base+3, 10) || v2["scans"] != "0" {
		t.Errorf("scans: sized_tag_idx %q (want %d), sized_v2_idx %q (want 0)",
			tag["scans"], base+3, v2["scans"])
	}
	for _, r := range got {
		want := ref(t, pub, `SELECT pg_size_pretty(pg_relation_size(indexrelid))
		  FROM pg_stat_user_indexes WHERE indexrelname = $1`, r["index"])
		if r["table"] != "sized" || r["size"] != want[0][0] {
			t.Errorf("%s: got table=%q size=%q, want sized %q", r["index"], r["table"], r["size"], want[0][0])
		}
	}
}

func TestSeqScans(t *testing.T) {
	requireLive(t)
	const counters = `SELECT seq_scan, seq_tup_read, COALESCE(idx_scan, 0)
	  FROM pg_stat_user_tables WHERE relname = 'scanned'`
	base := ref(t, pub, counters)[0]
	for range 4 {
		exec1(t, pub, `SELECT count(*) FROM `+schema+`.scanned`)
	}
	exec1(t, pub, `SET enable_seqscan = off`)
	for range 2 {
		exec1(t, pub, `SELECT v FROM `+schema+`.scanned WHERE id = 7`)
	}
	exec1(t, pub, `RESET enable_seqscan`)
	flush(t, pub)

	want := []string{
		strconv.Itoa(atoi(t, base[0]) + 4),
		strconv.Itoa(atoi(t, base[1]) + 4*1000),
		strconv.Itoa(atoi(t, base[2]) + 2),
	}
	waitFor(t, "scanned counters", func() (bool, error) {
		now := ref(t, pub, counters)[0]
		return now[0] == want[0] && now[1] == want[1] && now[2] == want[2], nil
	})
	got := inSchema(rows(t, pub, "seq-scans"))
	r := find(t, got, "table", "scanned")
	if r["seq_scans"] != want[0] || r["seq_rows_read"] != want[1] || r["index_scans"] != want[2] {
		t.Errorf("scanned: got seq_scans=%q seq_rows_read=%q index_scans=%q, want %v",
			r["seq_scans"], r["seq_rows_read"], r["index_scans"], want)
	}
	// deleted has no index, so idx_scan is NULL and the default shows.
	if v := find(t, got, "table", "deleted")["index_scans"]; v != "0" {
		t.Errorf("deleted: index_scans %q, want 0 for a table with no index", v)
	}
}

func TestVacuumStats(t *testing.T) {
	requireLive(t)
	waitFor(t, "deleted dead rows", func() (bool, error) {
		return refInt(t, pub, `SELECT n_dead_tup FROM pg_stat_user_tables WHERE relname = 'deleted'`) == 400, nil
	})
	got, want := stable(t, pub, "vacuum-stats", `
		SELECT relname, n_live_tup::text, n_dead_tup::text,
		  COALESCE(last_vacuum::text, ''), COALESCE(last_autovacuum::text, '')
		FROM pg_stat_user_tables WHERE schemaname = '`+schema+`'`)
	got = inSchema(got)
	for _, w := range want {
		r := find(t, got, "table", w[0])
		if r["live_rows"] != w[1] || r["dead_rows"] != w[2] ||
			r["last_vacuum"] != w[3] || r["last_autovacuum"] != w[4] {
			t.Errorf("%s: got %v, want %v", w[0], r, w)
		}
	}
	deleted := find(t, got, "table", "deleted")
	vacuumed := find(t, got, "table", "vacuumed")
	if deleted["live_rows"] != "600" || deleted["dead_rows"] != "400" ||
		deleted["last_vacuum"] != "" || deleted["last_autovacuum"] != "" {
		t.Errorf("deleted: %v, want 600 live, 400 dead, never vacuumed", deleted)
	}
	if vacuumed["live_rows"] != "600" || vacuumed["dead_rows"] != "0" ||
		vacuumed["last_vacuum"] == "" || vacuumed["last_autovacuum"] != "" {
		t.Errorf("vacuumed: %v, want 600 live, 0 dead, a manual vacuum only", vacuumed)
	}
	parseTimestamp(t, vacuumed["last_vacuum"])
	bloated := find(t, got, "table", "bloated")
	if bloated["live_rows"] != "4000" || bloated["dead_rows"] != "16000" {
		t.Errorf("bloated: %v, want 4000 live and 16000 dead", bloated)
	}
	if got[0]["table"] != "bloated" || got[1]["table"] != "deleted" {
		t.Errorf("order %q, %q; want bloated then deleted (most dead rows first)",
			got[0]["table"], got[1]["table"])
	}
}

var estimateRE = regexp.MustCompile(`^\d+\.\d$`)

func TestBloat(t *testing.T) {
	requireLive(t)
	got := inSchema(rows(t, pub, "bloat"))
	for _, r := range got {
		if !estimateRE.MatchString(r["bloat_estimate"]) {
			t.Errorf("%s: bloat_estimate %q is not a one-decimal number", r["table"], r["bloat_estimate"])
		}
		parseSize(t, r["waste"])
	}
	// Measured on 18: 5.1 and 2224 kB for bloated, 1.0 and 72 kB for
	// sized. The thresholds sit well inside both.
	bloated := find(t, got, "table", "bloated")
	if est := atof(t, bloated["bloat_estimate"]); est < 3.0 {
		t.Errorf("bloated: estimate %v after deleting four rows in five", est)
	}
	if parseSize(t, bloated["waste"]) < 1<<20 {
		t.Errorf("bloated: waste %q, want at least 1 MB", bloated["waste"])
	}
	if est := atof(t, find(t, got, "table", "sized")["bloat_estimate"]); est > 1.5 {
		t.Errorf("sized: estimate %v for a table never deleted from", est)
	}
	for _, r := range got {
		if r["table"] == "scanned" {
			t.Errorf("scanned has under ten pages and must not appear: %v", r)
		}
	}
	if got[0]["table"] != "bloated" {
		t.Errorf("first row is %q, want bloated (most waste)", got[0]["table"])
	}
}

func TestCallsAndOutliers(t *testing.T) {
	requireLive(t)
	// Reset first so the marker, at seven calls, is inside the top
	// twenty on both orderings whatever the earlier tests ran.
	exec1(t, pub, `SELECT pg_stat_statements_reset()`)
	for range 7 {
		exec1(t, pub, `SELECT pg_sleep(0.02) FROM `+schema+`.calls_marker`)
	}
	all := rows(t, pub, "calls")
	calls := find(t, all, "query", `SELECT pg_sleep($1) FROM `+schema+`.calls_marker`)
	if calls["calls"] != "7" {
		t.Errorf("calls: %q, want 7", calls["calls"])
	}
	// After the reset every other statement has run once; the marker
	// leads the most-called-first order.
	if all[0]["query"] != calls["query"] {
		t.Errorf("first row is %q, want the seven-call marker", all[0]["query"])
	}
	total, mean := atof(t, calls["total_time"]), atof(t, calls["mean_time"])
	// Seven 20 ms sleeps: the mean sits near 20 and the total near
	// 140, so a swap between them fails both checks.
	if mean < 15 || mean > 200 {
		t.Errorf("mean_time %v ms for a 20 ms sleep", mean)
	}
	if total <= mean || total < 7*mean-1 || total > 7*mean+1 {
		t.Errorf("total_time %v against mean %v and 7 calls", total, mean)
	}

	outliers := rows(t, pub, "outliers")
	o := find(t, outliers, "query", calls["query"])
	if o["calls"] != calls["calls"] || o["total_time"] != calls["total_time"] || o["mean_time"] != calls["mean_time"] {
		t.Errorf("outliers row %v disagrees with calls row %v", o, calls)
	}
	if outliers[0]["query"] != calls["query"] {
		t.Errorf("first outlier is %q, want the 140 ms marker", outliers[0]["query"])
	}
}

func TestCallsWithoutTheExtension(t *testing.T) {
	requireLive(t)
	for _, analysis := range []string{"calls", "outliers"} {
		r := run(t, sub, analysis)
		// The verb's own sentence, not the driver's "relation does not
		// exist", which also names the extension and also exits 1.
		want := analysis + " needs the pg_stat_statements extension"
		if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, want) {
			t.Errorf("%s on the subscriber: exit %d, stdout %q, stderr %q; want exit 1, "+
				"nothing on stdout and %q", analysis, r.code, r.stdout, r.stderr, want)
		}
	}
}

// assertNonIncreasing checks the largest-first order the analyses
// promise, through the pretty-printed sizes.
func assertNonIncreasing(t *testing.T, got []map[string]string, col string) {
	t.Helper()
	for i := 1; i < len(got); i++ {
		if parseSize(t, got[i][col]) > parseSize(t, got[i-1][col]) {
			t.Errorf("row %d %s %q exceeds row %d %q", i, col, got[i][col], i-1, got[i-1][col])
		}
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("not an integer: %q", s)
	}
	return n
}

func atof(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("not a number: %q", s)
	}
	return f
}
