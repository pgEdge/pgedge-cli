//go:build !windows

package selfupdate

import (
	"fmt"
	"os"
)

// Swap replaces target with newBinary. Outside Windows an executing
// file can be renamed over, so this is a chmod to target's bits and
// one os.Rename. StageBeside put newBinary in target's directory, so
// the rename is atomic and target's path is never empty.
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
