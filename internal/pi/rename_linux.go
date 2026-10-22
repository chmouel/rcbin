//go:build linux

package pi

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace is renameat2(RENAME_NOREPLACE), which fails with EEXIST
// instead of replacing the destination. os.Rename cannot express this: on every
// Unix it Lstats the destination first and rejects an existing directory, so it
// is neither a working directory move nor a no-replace primitive.
//
// Filesystems that refuse the flag are reported instead of being retried with a
// copy-then-unlink dance, which cannot be made atomic and can delete a
// concurrent save.
func renameNoReplace(src, dst string) error {
	err := unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL), errors.Is(err, unix.EOPNOTSUPP):
		return fmt.Errorf("%s does not support atomic no-replace renames: %w", src, err)
	default:
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
}
