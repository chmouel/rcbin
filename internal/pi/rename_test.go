package pi

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// testRenameNoReplace exercises the production no-replace rename on the running
// platform. It is shared by the per-OS test files so the darwin and linux
// syscalls are both covered by the runner that can execute them.
func testRenameNoReplace(t *testing.T) {
	t.Helper()

	t.Run("file onto existing file", func(t *testing.T) {
		root := t.TempDir()
		src := filepath.Join(root, "src")
		dst := filepath.Join(root, "dst")
		writeFile(t, src, "mine")
		writeFile(t, dst, "theirs")

		if err := renameNoReplace(src, dst); !errors.Is(err, os.ErrExist) {
			t.Fatalf("error = %v, want a destination-exists error", err)
		}
		if got := readFile(t, dst); got != "theirs" {
			t.Fatalf("destination was overwritten: %q", got)
		}
		if got := readFile(t, src); got != "mine" {
			t.Fatalf("source was moved: %q", got)
		}
	})

	t.Run("directory onto existing empty directory", func(t *testing.T) {
		root := t.TempDir()
		src := filepath.Join(root, "src")
		dst := filepath.Join(root, "dst")
		writeFile(t, filepath.Join(src, "file"), "mine")
		mkdir(t, dst)

		if err := renameNoReplace(src, dst); !errors.Is(err, os.ErrExist) {
			t.Fatalf("error = %v, want a destination-exists error", err)
		}
		if got := readFile(t, filepath.Join(src, "file")); got != "mine" {
			t.Fatalf("source was moved: %q", got)
		}
		if k, err := lstatKind(dst); err != nil || k != kindDir {
			t.Fatalf("destination = %v (err=%v), want the empty directory untouched", k, err)
		}
	})

	t.Run("directory onto existing populated directory", func(t *testing.T) {
		root := t.TempDir()
		src := filepath.Join(root, "src")
		dst := filepath.Join(root, "dst")
		writeFile(t, filepath.Join(src, "file"), "mine")
		writeFile(t, filepath.Join(dst, "file"), "theirs")

		if err := renameNoReplace(src, dst); !errors.Is(err, os.ErrExist) {
			t.Fatalf("error = %v, want a destination-exists error", err)
		}
		if got := readFile(t, filepath.Join(dst, "file")); got != "theirs" {
			t.Fatalf("destination was replaced: %q", got)
		}
	})

	t.Run("moves a directory to a free name", func(t *testing.T) {
		root := t.TempDir()
		src := filepath.Join(root, "src")
		dst := filepath.Join(root, "dst")
		writeFile(t, filepath.Join(src, "file"), "content")

		if err := renameNoReplace(src, dst); err != nil {
			t.Fatal(err)
		}
		if k, err := lstatKind(src); err != nil || k != kindAbsent {
			t.Fatalf("source = %v (err=%v), want it moved", k, err)
		}
		if got := readFile(t, filepath.Join(dst, "file")); got != "content" {
			t.Fatalf("moved content = %q", got)
		}
	})
}
