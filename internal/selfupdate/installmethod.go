package selfupdate

import "strings"

// InstallMethodFrom infers the install method from a resolved
// executable path. Moved here verbatim from internal/cli/doctor.go's
// installMethodFrom (same cases, same returns) so `pgedge doctor` and
// the refusal checks in ResolveTarget share one definition of "how
// was this installed" instead of two that could drift apart.
func InstallMethodFrom(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.Contains(lower, "cellar") ||
		strings.Contains(lower, "homebrew"):
		return "homebrew"
	case strings.Contains(lower, ".local/bin"):
		return "install-script"
	case strings.Contains(lower, "go/bin"):
		return "go-install"
	default:
		return "unknown"
	}
}
