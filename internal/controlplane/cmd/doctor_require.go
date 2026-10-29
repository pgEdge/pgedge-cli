package cmd

import (
	"fmt"
	"strings"
)

// doctorVerdict is what --require can test, each field one of
// doctor's own reported verdicts.
type doctorVerdict struct {
	// reachable is the top-level JSON reachable: any server answered.
	reachable bool
	// cluster is cluster_initialized true; absent fails, as unknown.
	cluster bool
	// supported holds when at least one server answered and none that
	// answered is below the support floor.
	supported bool
}

var doctorConditions = []string{"reachable", "cluster", "supported"}

// parseRequire validates --require before any probe, so a typo is a
// usage error rather than a failed gate.
func parseRequire(vals []string) ([]string, error) {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		v = strings.TrimSpace(v)
		known := false
		for _, c := range doctorConditions {
			if v == c {
				known = true
				break
			}
		}
		if !known {
			return nil, &ExitError{
				msg: fmt.Sprintf("unknown --require condition %q: "+
					"want one or more of %s", v,
					strings.Join(doctorConditions, ", ")),
				code: ExitUsage,
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// unmetConditions returns the required conditions v does not satisfy,
// in the order they were asked for.
func unmetConditions(required []string, v doctorVerdict) []string {
	var unmet []string
	for _, c := range required {
		ok := false
		switch c {
		case "reachable":
			ok = v.reachable
		case "cluster":
			ok = v.cluster
		case "supported":
			ok = v.supported
		}
		if !ok {
			unmet = append(unmet, c)
		}
	}
	return unmet
}
