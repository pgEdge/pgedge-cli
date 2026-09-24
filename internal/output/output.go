package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// Row is implemented by any type that can provide its column
// values as strings.
type Row interface {
	Columns() []string
}

// CheckRow is one line of a doctor-style check table: a check's name,
// its status, and a human-readable detail. Its Columns runs Status
// through ColorStatus, so every doctor colours its verdicts the same
// way. It lives here rather than in `pgedge doctor`, `pgedge starfleet
// doctor` and `pgedge controlplane doctor` so the three cannot drift
// apart, and all three already import this package.
type CheckRow struct {
	Check   string
	Status  string
	Details string
}

// Columns satisfies Row.
func (r CheckRow) Columns() []string {
	return []string{r.Check, ColorStatus(r.Status), r.Details}
}

// CheckHeaders returns the header row every doctor's check table
// prints. A function, not a package-level slice, so no caller can
// reorder the shared headers for everyone else by writing to it.
func CheckHeaders() []string {
	return []string{"CHECK", "STATUS", "DETAILS"}
}

// ValidFormat reports whether name is an output format the renderer
// understands. "table" is an internal alias for "text"; both are
// accepted even though only text/json/yaml are advertised on -o.
func ValidFormat(name string) bool {
	switch name {
	case "text", "table", "json", "yaml":
		return true
	default:
		return false
	}
}

// Renderer writes command results honoring the -o format flag.
// Structured data goes to Out; Err is for progress/debug lines.
type Renderer struct {
	Format string
	Color  bool
	Out    io.Writer
	Err    io.Writer
}

// IsText reports whether format is the human-facing table view, under
// either of its names. Every verb that chooses between printing a
// response verbatim and rendering rows asks this one function, so a
// guard cannot drop the alias on its own. ValidFormat and Print's
// dispatch are the only other places both names are spelled out.
func IsText(format string) bool {
	return format == "text" || format == "table"
}

// Structured reports whether -o selected a machine-readable format
// (json or yaml) rather than the text view.
func (r *Renderer) Structured() bool {
	return !IsText(r.Format)
}

// jsonShaped converts data into plain maps, slices and scalars keyed
// exactly as encoding/json would key it, so the yaml encoder emits the
// API's own field names. The generated clients in internal/*/api carry
// json tags only and yaml.v3 reads only yaml tags, so it would fall back
// to the lowercased Go field name (`created_at` becomes `createdat`),
// which matches no API field, disagrees with -o json and cannot be read
// back as a spec. loadSpecBytes in internal/controlplane/cmd is the
// mirror image, normalising input through JSON.
//
// UseNumber keeps integers exact. A plain decode into any gives float64,
// which yaml prints in scientific notation (1099511627776 as
// 1.099511627776e+12) and which loses precision above 2^53.
func jsonShaped(data any) (any, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("render yaml: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("render yaml: %w", err)
	}
	return restoreNumbers(out), nil
}

// restoreNumbers turns every json.Number back into an int64 when it is
// exactly representable as one, and a float64 otherwise, recursing
// through maps and slices. Anything that parses as neither keeps its
// original text rather than being dropped.
func restoreNumbers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = restoreNumbers(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = restoreNumbers(val)
		}
		return t
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	default:
		return v
	}
}

// Print writes data in the renderer's format.
//
// "text" (alias "table") — tab-aligned table; data must be []Row.
// "json" — compact JSON, newline-terminated (json.Encoder).
// "yaml" — YAML document.
func (r *Renderer) Print(data any, headers []string) error {
	switch r.Format {
	case "json":
		return json.NewEncoder(r.Out).Encode(data)

	case "yaml":
		node, err := jsonShaped(data)
		if err != nil {
			return err
		}
		enc := yaml.NewEncoder(r.Out)
		err = enc.Encode(node)
		if closeErr := enc.Close(); err == nil {
			err = closeErr
		}
		return err

	case "text", "table":
		tw := tabwriter.NewWriter(r.Out, 0, 0, 2, ' ', 0)
		if len(headers) > 0 {
			cols := make([]string, len(headers))
			for i, h := range headers {
				if r.Color {
					cols[i] = Bold(h)
				} else {
					cols[i] = h
				}
			}
			fmt.Fprintln(tw, strings.Join(cols, "\t"))
		}
		if rows, ok := data.([]Row); ok {
			for _, row := range rows {
				// Sanitized here, the one place every text/table
				// render passes through, rather than in each row
				// adapter, where a new one would arrive unescaped. A server string carrying a newline would
				// otherwise forge rows a reader cannot tell from real
				// ones. This also escapes the colour Columns() adds;
				// see Sanitize for why that is safe today.
				cells := row.Columns()
				safe := make([]string, len(cells))
				for i, c := range cells {
					safe[i] = Sanitize(c)
				}
				fmt.Fprintln(tw, strings.Join(safe, "\t"))
			}
		}
		return tw.Flush()

	default:
		return fmt.Errorf(
			"unsupported output format: %q (want text, json, yaml)",
			r.Format)
	}
}
