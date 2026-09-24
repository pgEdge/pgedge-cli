//go:build !windows

package selfupdate

import (
	"fmt"
	"os"
)

// Swap replaces target with newBinary. On every platform but Windows,
// an executing file can be renamed out from under itself, so this is
// a chmod (newBinary takes target's existing permission bits) then a
// single os.Rename onto target. newBinary comes from StageBeside, so
// it is already in target's own directory: same filesystem, so the
// rename is atomic and there is never a moment with no binary at
// target's path.
func Swap(target, newBinary string) error {
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("stat %s: %w", target, err)
	}

	if err := os.Chmod(newBinary, info.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod %s: %w", newBinary, err)
	}

	if err := os.Rename(newBinary, target); err != nil {
		return swapError(target, err)
	}

	return nil
}
