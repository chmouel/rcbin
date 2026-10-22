//go:build darwin

package pi

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace is renamex_np(RENAME_EXCL), which fails with EEXIST instead of
// replacing the destination. os.Rename cannot express this: it Lstats the
// destination first, so it rejects an existing directory before reaching the
// syscall, while for files it happily replaces the destination.
//
// Filesystems that refuse the flag are reported instead of being retried with a
// copy-then-unlink dance, which cannot be made atomic and can delete a
// concurrent save.
func renameNoReplace(src, dst string) error {
	err := unix.RenamexNp(src, dst, unix.RENAME_EXCL)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOTSUP):
		return fmt.Errorf("%s does not support atomic no-replace renames: %w", src, err)
	default:
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
}
