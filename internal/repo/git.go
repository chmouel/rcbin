// Package repo inspects and synchronizes Git repositories using porcelain and
// ref queries (never human-readable output), a bounded worker pool for clean
// repositories, and an interactive adapter for dirty repositories.
package repo

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chmouel/rc/internal/runner"
)

// git runs a git subcommand inside dir and returns trimmed stdout.
func git(ctx context.Context, r runner.Runner, dir string, args ...string) (string, error) {
	res, err := r.Run(ctx, runner.Spec{Name: "git", Args: append([]string{"-C", dir}, args...), Dir: dir})
	return strings.TrimSpace(res.Stdout), err
}

// IsWorkTree reports whether dir is inside a Git work tree.
func IsWorkTree(ctx context.Context, r runner.Runner, dir string) bool {
	out, err := git(ctx, r, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// HasChanges reports whether the repository has staged, unstaged, or untracked
// changes using porcelain v2 output.
func HasChanges(ctx context.Context, r runner.Runner, dir string) bool {
	res, _ := r.Run(ctx, runner.Spec{
		Name: "git",
		Args: []string{"-C", dir, "status", "--porcelain=v2", "--untracked-files=normal"},
		Dir:  dir,
	})
	return strings.TrimSpace(res.Stdout) != ""
}

// Head returns the current HEAD revision, or "none" when unavailable.
func Head(ctx context.Context, r runner.Runner, dir string) string {
	out, err := git(ctx, r, dir, "rev-parse", "HEAD")
	if err != nil || out == "" {
		return "none"
	}
	return out
}

// Upstream reports the count of commits HEAD is ahead of its upstream and
// whether an upstream is configured.
func Upstream(ctx context.Context, r runner.Runner, dir string) (ahead int, hasUpstream bool) {
	out, err := git(ctx, r, dir, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, true
	}
	return n, true
}

// RemoteURL returns the origin remote URL, if any.
func RemoteURL(ctx context.Context, r runner.Runner, dir string) string {
	out, _ := git(ctx, r, dir, "remote", "get-url", "origin")
	return out
}

// Name derives a display name from the origin URL, falling back to the
// directory base name.
func Name(ctx context.Context, r runner.Runner, dir string) string {
	url := RemoteURL(ctx, r, dir)
	if url != "" {
		base := filepath.Base(url)
		base = strings.TrimSuffix(base, ".git")
		if base != "" && base != "." && base != "/" {
			return base
		}
	}
	return filepath.Base(dir)
}
