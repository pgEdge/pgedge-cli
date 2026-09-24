//go:build windows

package selfupdate

import "os"

// Swap replaces target with newBinary on Windows, where os.Rename
// cannot land ON a file this process is executing from — it fails
// with "The process cannot access the file because it is being used
// by another process." Renaming target ASIDE first (target ->
// target+".old") frees the name so newBinary can take it while the
// old file stays mapped into this running process; cmd/pgedge/main.go
// removes the ".old" file on the next launch, once nothing has it
// open any more.
func Swap(target, newBinary string) error {
	old := target + ".old"
	if err := os.Rename(target, old); err != nil {
		return swapError(target, err)
	}

	if err := os.Rename(newBinary, target); err != nil {
		// Best-effort restore: put the original back so a failed
		// second rename does not leave pgedge missing outright.
		_ = os.Rename(old, target)
		return swapError(target, err)
	}

	return nil
}
