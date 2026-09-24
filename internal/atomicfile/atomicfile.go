// Package atomicfile replaces a file in one step, so a reader arriving
// mid-write sees the whole old file or the whole new one and never a
// truncated one. Every command shares one config file and one token
// cache, so concurrent invocations are ordinary, and the config file
// is the only copy of every profile.
package atomicfile

import (
	"os"
	"path/filepath"
)

// StagingPrefix names the temp files Write creates beside one target.
// The writer and every sweeper share it, so a cleanup cannot walk past
// the file it exists to remove.
func StagingPrefix(base string) string { return "." + base + ".tmp" }

// Write stages data in a temp file in path's own directory, chmods it to
// perm, and renames it over path. os.Rename is atomic only within one
// filesystem, and the system temp directory is often another. So an
// unwritable directory refuses the write even when the file itself is
// writable.
//
// No fsync, deliberately: the rename protects a concurrent reader, and
// power loss leaves at worst an unreadable file, which the CLI reports
// by name.
//
// A symlink at path is followed, so its target is replaced and the
// link survives; renaming over the link would leave a dotfiles-managed
// target holding the OLD content. A dangling link is replaced itself.
//
// Cleanup is a deferred Remove, which a signal skips: a process killed
// before the rename leaves a perm-mode staging file holding the data.
// Callers that promise "no on-disk copy" sweep by StagingPrefix.
func Write(path string, data []byte, perm os.FileMode) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, StagingPrefix(filepath.Base(path)))
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if tmp != "" {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	tmp = "" // renamed away; nothing left to clean up
	return nil
}
