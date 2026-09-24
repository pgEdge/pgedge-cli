package output

import "fmt"

// byteUnits are the binary size suffixes FormatBytes steps through,
// starting one step above plain bytes.
var byteUnits = []string{"KiB", "MiB", "GiB", "TiB", "PiB"}

// FormatBytes renders an optional byte count for table output: a blank
// cell when the pointer is nil, a plain byte count below 1 KiB, and a
// one-decimal binary unit above it. Sizes arrive from the API as
// optional ints, and a raw ten-digit byte count is unreadable in a
// column.
func FormatBytes(n *int) string {
	if n == nil {
		return ""
	}
	v := *n
	if v < 1024 {
		return fmt.Sprintf("%d B", v)
	}
	size := float64(v)
	unit := -1
	for size >= 1024 && unit < len(byteUnits)-1 {
		size /= 1024
		unit++
	}
	return fmt.Sprintf("%.1f %s", size, byteUnits[unit])
}
