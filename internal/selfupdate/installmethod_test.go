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
		{"/opt/homebrew/lib/node_modules/@pgedge/cli-darwin-arm64/bin/pgedge", "npm"},
		{"/Users/alice/.nvm/versions/node/v24.1.0/lib/node_modules/@pgedge/cli-linux-x64/bin/pgedge", "npm"},
		{"/app/node_modules/.pnpm/@pgedge+cli-linux-arm64@0.5.0/node_modules/@pgedge/cli-linux-arm64/bin/pgedge", "npm"},
		{`C:\Users\alice\AppData\Roaming\npm\node_modules\@pgedge\cli-win32-x64\bin\pgedge.exe`, "npm"},
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
