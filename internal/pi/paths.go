package pi

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type kind int

const (
	kindAbsent kind = iota
	kindFile
	kindDir
	kindSymlink
	kindOther
)

func (k kind) String() string {
	switch k {
	case kindAbsent:
		return "missing"
	case kindFile:
		return "regular file"
	case kindDir:
		return "directory"
	case kindSymlink:
		return "symlink"
	default:
		return "special file"
	}
}

func lstatKind(path string) (kind, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return kindAbsent, nil
		}
		return kindAbsent, fmt.Errorf("inspecting %s: %w", path, err)
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return kindSymlink, nil
	case info.IsDir():
		return kindDir, nil
	case info.Mode().IsRegular():
		return kindFile, nil
	default:
		return kindOther, nil
	}
}

// entryKind resolves a source entry to the kind it will be linked as. Symlinks
// are followed so an aliased payload is still valid, while broken or looping
// links, sockets, FIFOs, and devices are rejected.
func entryKind(path string, e entry) (kind, error) {
	k, err := lstatKind(path)
	if err != nil {
		return k, err
	}
	if k == kindSymlink {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return k, fmt.Errorf("pi source entry %s is a broken or looping symlink: %w", path, err)
		}
		if k, err = lstatKind(resolved); err != nil {
			return k, err
		}
	}
	switch k {
	case kindAbsent:
		return k, nil
	case kindFile:
		if e.isDir {
			return k, fmt.Errorf("pi source entry %s must be a directory", path)
		}
	case kindDir:
		if !e.isDir {
			return k, fmt.Errorf("pi source entry %s must be a regular file", path)
		}
	default:
		return k, fmt.Errorf("pi source entry %s is not a regular file or directory", path)
	}
	return k, nil
}

// absolutePath cleans and absolutises p without resolving symlinks, so links
// point at the path the user configured.
func absolutePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", p, err)
	}
	return filepath.Clean(abs), nil
}

// canonicalPath resolves symlinks in the longest existing ancestor of p and
// re-appends the remaining components, so not-yet-created paths still compare
// against the real tree.
func canonicalPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", p, err)
	}
	cur := abs
	var rest []string
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, rest[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("resolving %s: %w", cur, err)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("resolving %s: no existing ancestor", abs)
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

func checkOverlap(agentDir, source string) error {
	switch {
	case agentDir == source:
		return fmt.Errorf("pi agent directory and source are the same path %q", agentDir)
	case within(source, agentDir):
		return fmt.Errorf("pi source %q is inside the agent directory %q", source, agentDir)
	case within(agentDir, source):
		return fmt.Errorf("pi agent directory %q is inside the source %q", agentDir, source)
	}
	return nil
}

// checkIdenticalTrees catches directory aliases that string comparison misses:
// on a case-insensitive filesystem <root>/Pi and <root>/pi are the same
// directory, and linking one into the other would point the tree at itself.
// Every existing component is compared with os.SameFile, so the check works on
// paths that already exist (it is re-run after the directories are created).
func checkIdenticalTrees(agentDir, source string) error {
	same, err := sameDirectory(agentDir, source)
	if err != nil {
		return err
	}
	if same {
		return fmt.Errorf("pi agent directory %q and source %q are the same directory", agentDir, source)
	}
	inside, err := directoryInside(source, agentDir)
	if err != nil {
		return err
	}
	if inside {
		return fmt.Errorf("pi source %q is inside the agent directory %q", source, agentDir)
	}
	inside, err = directoryInside(agentDir, source)
	if err != nil {
		return err
	}
	if inside {
		return fmt.Errorf("pi agent directory %q is inside the source %q", agentDir, source)
	}
	return nil
}

// sameDirectory reports whether two existing paths are the same filesystem
// entry. A path that does not exist cannot alias anything yet; the check runs
// again once the directories exist.
func sameDirectory(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspecting %s: %w", a, err)
	}
	bi, err := os.Stat(b)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspecting %s: %w", b, err)
	}
	return os.SameFile(ai, bi), nil
}

// directoryInside reports whether any existing ancestor of child is the same
// filesystem entry as parent.
func directoryInside(child, parent string) (bool, error) {
	info, err := os.Stat(parent)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspecting %s: %w", parent, err)
	}
	for cur := filepath.Dir(child); ; {
		curInfo, statErr := os.Stat(cur)
		if statErr == nil && os.SameFile(curInfo, info) {
			return true, nil
		}
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			return false, fmt.Errorf("inspecting %s: %w", cur, statErr)
		}
		next := filepath.Dir(cur)
		if next == cur {
			return false, nil
		}
		cur = next
	}
}

func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// samePath reports whether two paths (which may not exist) resolve to the same
// location.
func samePath(a, b string) (bool, error) {
	ca, err := canonicalPath(a)
	if err != nil {
		return false, err
	}
	cb, err := canonicalPath(b)
	if err != nil {
		return false, err
	}
	return ca == cb, nil
}

// moveNoReplace moves src to dst and fails when dst exists. It is a single
// atomic no-replace rename, so a writer that creates or replaces dst
// concurrently loses nothing: the rename either happens before dst appears or
// fails with EEXIST.
func (s *Syncer) moveNoReplace(src, dst string) error {
	if err := s.rename()(src, dst); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("moving %s to %s: the destination already exists: %w", src, dst, err)
		}
		return fmt.Errorf("moving %s to %s: %w", src, dst, err)
	}
	return nil
}

func nextFreeBackup(target string) (string, error) {
	base := target + ".rc-adopt.bak"
	for i := 0; i < 1000; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s.%d", base, i)
		}
		k, err := lstatKind(candidate)
		if err != nil {
			return "", err
		}
		if k == kindAbsent {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free backup name for %s", target)
}

// secureEffectiveAuth restricts the credential entry that is actually in play,
// whether it lives in the shared tree or in a host override, and whether the
// target was linked just now or was already correct.
func (s *Syncer) secureEffectiveAuth(source, host string) error {
	path := filepath.Join(source, AuthFile)
	k, err := lstatKind(path)
	if err != nil {
		return err
	}
	hostPath := filepath.Join(source, HostDir, host, AuthFile)
	hostKind, err := lstatKind(hostPath)
	if err != nil {
		return err
	}
	if hostKind != kindAbsent {
		path, k = hostPath, hostKind
	}
	if k == kindAbsent {
		return nil
	}
	if k == kindSymlink {
		s.Rep.Debugf("pi: %s is a symlink, leaving its mode unchanged", path)
		return nil
	}
	return s.secureAuthPath(path)
}

// secureAuthPath restricts a regular credential file to its owner. The file is
// opened with O_NOFOLLOW and chmodded through the descriptor, so a symlink
// swapped in between inspection and chmod cannot redirect the change to a
// referent outside the managed tree, and the mode applies to the inode that was
// inspected.
func (s *Syncer) secureAuthPath(path string) error {
	return s.secureCredential(path, path)
}

// secureCredential is secureAuthPath with a caller-chosen label, so a credential
// captured in the staging directory is reported under the path it is published
// to rather than under a transient staging name.
func (s *Syncer) secureCredential(path, label string) error {
	f, err := s.openCredential(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
			s.Rep.Debugf("pi: %s is not a regular file, leaving its mode unchanged", label)
			return nil
		}
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		s.Rep.Debugf("pi: %s is not a regular file, leaving its mode unchanged", label)
		return nil
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		s.Rep.Warnf("pi: %s was readable by other users (mode %04o); restricting it to 0600", label, perm)
	}
	if err := s.chmod()(f, 0o600); err != nil {
		return fmt.Errorf("restricting permissions on %s: %w", label, err)
	}
	return nil
}

// openCredential opens path without following a final symlink.
func (s *Syncer) openCredential(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW, 0)
}

// publishSecret moves a live credential into the canonical tree without ever
// exposing an unprotected inode there. The entry is first captured in an
// owner-only staging directory on the same filesystem, so later writes through
// the original pathname cannot swap the inode that is about to be published;
// the captured file is restricted through its descriptor and only then renamed
// into place.
func (s *Syncer) publishSecret(src, dst string) error {
	stage, err := os.MkdirTemp(filepath.Dir(dst), ".rc-pi-stage-")
	if err != nil {
		return fmt.Errorf("creating a staging directory next to %s: %w", dst, err)
	}
	staged := filepath.Join(stage, filepath.Base(dst))
	if err := s.moveNoReplace(src, staged); err != nil {
		_ = os.Remove(stage)
		return err
	}
	if err := s.secureCredential(staged, dst); err != nil {
		return fmt.Errorf("%w (the credential is preserved at %s)", err, staged)
	}
	if err := s.moveNoReplace(staged, dst); err != nil {
		return fmt.Errorf("%w (the credential is preserved at %s)", err, staged)
	}
	if err := os.Remove(stage); err != nil {
		s.Rep.Debugf("pi: could not remove the staging directory %s: %v", stage, err)
	}
	return nil
}

func (s *Syncer) secureBackup(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspecting credential backup %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		s.Rep.Warnf("pi: %s is a preserved symlink; its mode is left unchanged so the referent is not modified", path)
		return nil
	}
	return s.secureAuthPath(path)
}
