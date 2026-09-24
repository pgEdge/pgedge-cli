// Package metricfmt renders metric samples for text tables. byoc and
// managed both need it and must agree, so it lives outside both.
package metricfmt

import (
	"fmt"
	"math"
	"strconv"
)

// Value renders one metric sample for a text table.
//
// FormatFloat precision -1 rather than %v, which prints 4.74362391e+08
// for a byte count. Nothing is rounded or rescaled: the endpoints
// disagree on their own timestamp units, so reading meaning from a
// column name would be guessing.
func Value(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) &&
			math.Abs(t) < 1e15 {
			return strconv.FormatFloat(t, 'f', 0, 64)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", t)
	}
}
