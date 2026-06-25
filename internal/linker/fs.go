package linker

import (
	"os"
	"path/filepath"
)

// FileSystem wraps the filesystem operations the linker needs, allowing
// deterministic tests. The production implementation delegates to the os
// package.
type FileSystem interface {
	Lstat(name string) (os.FileInfo, error)
	Readlink(name string) (string, error)
	Symlink(oldname, newname string) error
	Remove(name string) error
	MkdirAll(path string, perm os.FileMode) error
	EvalSymlinks(path string) (string, error)
}

// OSFS is the production FileSystem backed by the os package.
type OSFS struct{}

func (OSFS) Lstat(name string) (os.FileInfo, error)       { return os.Lstat(name) }
func (OSFS) Readlink(name string) (string, error)         { return os.Readlink(name) }
func (OSFS) Symlink(oldname, newname string) error        { return os.Symlink(oldname, newname) }
func (OSFS) Remove(name string) error                     { return os.Remove(name) }
func (OSFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (OSFS) EvalSymlinks(path string) (string, error)     { return filepath.EvalSymlinks(path) }

// isSymlink reports whether info describes a symbolic link.
func isSymlink(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}
