//go:build windows

package selfupdate

import "os"

// Swap replaces target with newBinary on Windows, where os.Rename
// cannot land ON a file this process is executing from ("The process
// cannot access the file because it is being used by another
// process."). Renaming target ASIDE to target+".old" frees the name
// while the old file stays mapped; cmd/pgedge/main.go removes ".old"
// on the next launch.
func Swap(target, newBinary string) error {
	old := target + ".old"
	if err := os.Rename(target, old); err != nil {
		return swapError(target, err)
	}

	if err := os.Rename(newBinary, target); err != nil {
		// Best-effort restore, so pgedge is not left missing.
		_ = os.Rename(old, target)
		return swapError(target, err)
	}

	return nil
}
