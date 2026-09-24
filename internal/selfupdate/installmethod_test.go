package selfupdate

import "testing"

// Cases copied verbatim from internal/cli/doctor_test.go's
// TestInstallMethodFrom, the classifier's original home, so the move
// loses nothing: this package now pins the cases directly instead of
// only through doctor's report.
func TestInstallMethodFrom(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/opt/homebrew/Cellar/pgedge/0.1.0/bin/pgedge", "homebrew"},
		{"/usr/local/Homebrew/bin/pgedge", "homebrew"},
		{"/Users/alice/.local/bin/pgedge", "install-script"},
		{"/Users/alice/go/bin/pgedge", "go-install"},
		{"/var/folders/x/T/go-build123/b001/cli.test", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want+" "+tt.path, func(t *testing.T) {
			if got := InstallMethodFrom(tt.path); got != tt.want {
				t.Errorf("InstallMethodFrom(%q) = %q, want %q",
					tt.path, got, tt.want)
			}
		})
	}
}
