package output

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type fakeRow struct{ a, b string }

func (r fakeRow) Columns() []string { return []string{r.a, r.b} }

func TestRendererPrint(t *testing.T) {
	rows := []Row{fakeRow{"one", "1"}, fakeRow{"two", "2"}}
	tests := []struct {
		name    string
		format  string
		data    any
		headers []string
		want    []string
		wantErr bool
	}{
		{name: "text renders table", format: "text",
			data: rows, headers: []string{"NAME", "N"},
			want: []string{"NAME", "one", "two"}},
		{name: "table alias works", format: "table",
			data: rows, headers: []string{"NAME", "N"},
			want: []string{"one"}},
		{name: "json compact", format: "json",
			data: map[string]string{"k": "v"},
			want: []string{`{"k":"v"}`}},
		{name: "yaml", format: "yaml",
			data: map[string]string{"k": "v"},
			want: []string{"k: v"}},
		{name: "unknown format errors", format: "xml",
			data: rows, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			r := &Renderer{Format: tt.format, Out: &buf}
			err := r.Print(tt.data, tt.headers)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			for _, w := range tt.want {
				if !strings.Contains(buf.String(), w) {
					t.Errorf("output %q missing %q",
						buf.String(), w)
				}
			}
		})
	}
}

// jsonTagged mirrors the shape of a generated API type: json tags and
// no yaml tags, which is what every client in internal/*/api carries.
//
// The tag names are deliberately synthetic rather than real API field
// names. internal/clitest's collectFieldNames walks every .go file
// under internal/output — tests included — and treats each json tag it
// finds as a field the code really has. Naming these `created_at` or
// `postgres_version` therefore silently teaches that gate that
// internal/output owns those fields, which makes a legitimate
// doc-gate marker in ROADMAP.md read as redundant and fails
// TestSkillDocsFieldClaimsMatchStructs. Multi-word names are all this
// test needs: the property under test is that the emitted key equals
// the json tag rather than the lowercased Go field name.
type jsonTagged struct {
	MadeOn      string      `json:"made_on"`
	WidgetLabel string      `json:"widget_label"`
	OwnerRef    *string     `json:"owner_ref,omitempty"`
	InnerBlock  *nestedSpec `json:"inner_block,omitempty"`
}

type nestedSpec struct {
	GadgetRelease string `json:"gadget_release"`
}

// TestRendererYAMLHonorsJSONTags pins the one reason yaml output goes
// through JSON first. The generated clients carry zero `yaml:"..."`
// tags, and yaml.v3 ignores json tags — it lowercases and concatenates
// the Go field name instead. Encoding such a struct directly therefore
// emits `created_at` as `createdat`, which matches no API field name,
// does not match this command's own -o json output, and cannot be fed
// back to anything that reads a spec.
//
// The absent-key half of this test is the half that bites: asserting
// only that `created_at` is present passes just as happily on output
// that also contains `createdat` elsewhere.
func TestRendererYAMLHonorsJSONTags(t *testing.T) {
	tid := "t-1"
	data := jsonTagged{
		MadeOn:      "1970-01-01T00:00:00Z",
		WidgetLabel: "store",
		OwnerRef:    &tid,
		InnerBlock:  &nestedSpec{GadgetRelease: "16.14"},
	}

	var buf bytes.Buffer
	r := &Renderer{Format: "yaml", Out: &buf}
	if err := r.Print(data, nil); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	// Present: the json tag spelling, at both nesting levels.
	for _, want := range []string{
		"made_on:", "widget_label:", "owner_ref:", "inner_block:",
		"gadget_release:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("yaml output missing %q\n%s", want, got)
		}
	}

	// Absent: yaml.v3's lowercased-field-name spelling.
	for _, bad := range []string{
		"madeon", "widgetlabel", "ownerref", "innerblock",
		"gadgetrelease",
	} {
		if strings.Contains(got, bad) {
			t.Errorf("yaml output leaked Go field name %q\n%s",
				bad, got)
		}
	}
}

// TestRendererYAMLMatchesJSONKeys is the invariant the fix exists to
// protect: yaml and json are two encodings of one object, so a key
// present in one must be present in the other. It compares the decoded
// key sets rather than substrings, so it cannot be satisfied by a
// coincidental match.
func TestRendererYAMLMatchesJSONKeys(t *testing.T) {
	tid := "t-1"
	data := jsonTagged{
		MadeOn:      "1970-01-01T00:00:00Z",
		WidgetLabel: "store",
		OwnerRef:    &tid,
		InnerBlock:  &nestedSpec{GadgetRelease: "16.14"},
	}

	var yBuf, jBuf bytes.Buffer
	if err := (&Renderer{Format: "yaml", Out: &yBuf}).
		Print(data, nil); err != nil {
		t.Fatal(err)
	}
	if err := (&Renderer{Format: "json", Out: &jBuf}).
		Print(data, nil); err != nil {
		t.Fatal(err)
	}

	var fromYAML, fromJSON map[string]any
	if err := yaml.Unmarshal(yBuf.Bytes(), &fromYAML); err != nil {
		t.Fatalf("yaml output does not parse: %v\n%s", err, yBuf.String())
	}
	if err := json.Unmarshal(jBuf.Bytes(), &fromJSON); err != nil {
		t.Fatalf("json output does not parse: %v", err)
	}

	if len(fromYAML) == 0 {
		t.Fatal("decoded yaml has no keys; the comparison would be vacuous")
	}
	for k := range fromJSON {
		if _, ok := fromYAML[k]; !ok {
			t.Errorf("key %q in json output but not yaml: %v",
				k, keysOf(fromYAML))
		}
	}
	for k := range fromYAML {
		if _, ok := fromJSON[k]; !ok {
			t.Errorf("key %q in yaml output but not json: %v",
				k, keysOf(fromJSON))
		}
	}
}

type withNumbers struct {
	SlotCount  int64   `json:"slot_count"`
	WideCount  int64   `json:"wide_count"`
	VastCount  int64   `json:"vast_count"`
	ShareRatio float64 `json:"share_ratio"`
	SlotList   []int64 `json:"slot_list"`
}

// TestRendererYAMLPreservesIntegers guards the UseNumber detour in
// jsonShaped. Routing yaml through a plain json.Unmarshal into any
// decodes every number as float64, and the yaml encoder then prints
// 1099511627776 as 1.099511627776e+12 and rounds anything above 2^53.
// Trading mangled keys for mangled numbers would not be a fix, so the
// exact rendering is asserted rather than merely "it parses".
func TestRendererYAMLPreservesIntegers(t *testing.T) {
	var buf bytes.Buffer
	r := &Renderer{Format: "yaml", Out: &buf}
	err := r.Print(withNumbers{
		SlotCount:  5432,
		WideCount:  1 << 40,
		VastCount:  9007199254740993, // 2^53+1: not exact as float64
		ShareRatio: 0.5,
		SlotList:   []int64{1, 1 << 40},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	for _, want := range []string{
		"slot_count: 5432",
		"wide_count: 1099511627776",
		"vast_count: 9007199254740993",
		"share_ratio: 0.5",
		"- 1099511627776",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("yaml output missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "e+") {
		t.Errorf("yaml output used scientific notation\n%s", got)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestJSONIsNewlineTerminated(t *testing.T) {
	var buf bytes.Buffer
	r := &Renderer{Format: "json", Out: &buf}
	if err := r.Print(map[string]int{"n": 1}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Error("json output not newline-terminated")
	}
}

func TestRendererColorHeaders(t *testing.T) {
	rows := []Row{fakeRow{"val", "1"}}

	tests := []struct {
		name         string
		color        bool
		colorEnabled bool
		want         string
	}{
		{name: "color true wraps header in bold",
			color: true, colorEnabled: true, want: "\033[1mHEADER\033[0m"},
		{name: "color false returns plain header",
			color: false, colorEnabled: true, want: "HEADER"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldColorEnabled := ColorEnabled
			t.Cleanup(func() { ColorEnabled = oldColorEnabled })
			ColorEnabled = tt.colorEnabled

			var buf bytes.Buffer
			r := &Renderer{Format: "text", Color: tt.color, Out: &buf}
			err := r.Print(rows, []string{"HEADER"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), tt.want) {
				t.Errorf("output missing %q: %q", tt.want, buf.String())
			}
		})
	}
}

func TestValidFormat(t *testing.T) {
	cases := map[string]bool{
		"text":  true,
		"table": true,
		"json":  true,
		"yaml":  true,
		"toml":  false,
		"":      false,
		"JSON":  false,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ValidFormat(name); got != want {
				t.Errorf("ValidFormat(%q) = %v, want %v", name, got, want)
			}
		})
	}
}

// TestIsText pins the one definition of the text view: both of its
// names are text, and every other format -o accepts is structured.
func TestIsText(t *testing.T) {
	for _, tc := range []struct {
		format string
		want   bool
	}{
		{"text", true},
		{"table", true},
		{"json", false},
		{"yaml", false},
		{"", false},
	} {
		if got := IsText(tc.format); got != tc.want {
			t.Errorf("IsText(%q) = %v, want %v", tc.format, got, tc.want)
		}
		r := &Renderer{Format: tc.format}
		if got := r.Structured(); got != !tc.want {
			t.Errorf("Renderer{%q}.Structured() = %v, want %v",
				tc.format, got, !tc.want)
		}
	}
}
