package pi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// lockPaths returns the advisory lock files for this run. Both live outside any
// Git repository so they never appear in desktop-config or the agent directory.
// The source-keyed lock serializes agent directories that share one canonical
// source; the agent-keyed lock serializes runs that adopt into the same agent
// directory.
func lockPaths(source, agentDir string) []string {
	paths := []string{
		filepath.Join(os.TempDir(), "rc-pi-src-"+pathKey(source)),
		filepath.Join(os.TempDir(), "rc-pi-agent-"+pathKey(agentDir)),
	}
	sort.Strings(paths)
	return dedupePaths(paths)
}

func pathKey(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:8])
}

func dedupePaths(in []string) []string {
	var last string
	first := true
	out := make([]string, 0, len(in))
	for _, p := range in {
		if !first && p == last {
			continue
		}
		out = append(out, p)
		last, first = p, false
	}
	return out
}

type lock struct {
	path  string
	token string
}

type locks struct {
	held []*lock
}

// acquireLocks takes every lock in the given order, releasing what it already
// holds when one is taken.
func acquireLocks(paths []string) (*locks, error) {
	l := &locks{}
	for _, p := range paths {
		held, err := acquireLock(p)
		if err != nil {
			l.release()
			return nil, err
		}
		l.held = append(l.held, held)
	}
	return l, nil
}

func acquireLock(path string) (*lock, error) {
	token, err := lockToken()
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("pi sync lock %s is already held%s; remove it once the owning rc run has finished", path, lockOwner(path))
		}
		return nil, fmt.Errorf("creating lock %s: %w", path, err)
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("writing lock %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("writing lock %s: %w", path, err)
	}
	return &lock{path: path, token: token}, nil
}

func lockToken() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating lock token: %w", err)
	}
	return fmt.Sprintf("%d %s", os.Getpid(), hex.EncodeToString(buf)), nil
}

func lockOwner(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	owner := strings.TrimSpace(string(content))
	if owner == "" {
		return ""
	}
	return fmt.Sprintf(" by %q", owner)
}

// release removes only locks this run still owns, so a lock that was replaced
// by a newer run survives.
func (l *locks) release() {
	if l == nil {
		return
	}
	for i := len(l.held) - 1; i >= 0; i-- {
		l.held[i].release()
	}
	l.held = nil
}

func (l *lock) release() {
	content, err := os.ReadFile(l.path)
	if err != nil {
		return
	}
	if strings.TrimSpace(string(content)) != l.token {
		return
	}
	_ = os.Remove(l.path)
}
