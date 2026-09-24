// Package atomicfile replaces a file in one step, so a reader arriving
// mid-write sees the whole old file or the whole new one and never a
// truncated one. The CLI shares one config file and one token cache
// across every command in the tree, so two concurrent invocations are
// ordinary (#146 D5), and the config file is the only copy of every
// profile and credential (#437).
package atomicfile

import (
	"os"
	"path/filepath"
)

// StagingPrefix names the temp files Write creates beside one target.
// It is the single definition shared by the writer and any sweeper: two
// spellings of this prefix is how a cleanup ends up walking past the
// very file it exists to remove.
func StagingPrefix(base string) string { return "." + base + ".tmp" }

// Write stages data in a temp file in path's own directory, chmods it to
// perm, and renames it over path. os.Rename is only atomic within a
// filesystem and the system temp directory is routinely a different one,
// which is why the staging file is never created anywhere else.
//
// A directory that exists but is not writable refuses the write even
// when the existing file itself is writable, because the staging file
// cannot be created there. That is inherent to every atomic write.
//
// There is deliberately no fsync: the rename is what protects a
// concurrent reader, and a power-loss window leaves at worst an
// unreadable file, which the CLI reports by name rather than silently.
//
// A symlink at path is followed, so the file it points at is what gets
// replaced and the link survives. A rename over the link itself would
// leave the link's target holding the OLD content while the path
// served the new, which is a silent divergence for a config a user
// manages through a dotfiles link. A dangling or absent link resolves
// to path itself.
//
// Cleanup is a deferred Remove, so it does NOT survive a signal: a
// process killed between the write and the rename leaves a perm-mode
// staging file holding the data. Callers that promise "no on-disk copy"
// sweep by StagingPrefix.
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
	// Every failure below leaves the previous file untouched; the
	// staged temp file must not outlive the attempt either way.
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
