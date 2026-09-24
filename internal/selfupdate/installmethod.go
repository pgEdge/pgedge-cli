package selfupdate

import "strings"

// InstallMethodFrom infers the install method from a resolved
// executable path. `pgedge doctor` and ResolveTarget's refusal checks
// share it, so the two cannot disagree on how pgedge was installed.
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
