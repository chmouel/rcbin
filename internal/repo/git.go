// Package repo inspects and synchronizes Git repositories using porcelain and
// ref queries (never human-readable output), a bounded worker pool for clean
// repositories, and an interactive adapter for dirty repositories.
package repo

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chmouel/rc/internal/output"
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

// CommitCount reports the commits reachable from to but not from from.
func CommitCount(ctx context.Context, r runner.Runner, dir, from, to string) (int, error) {
	out, err := git(ctx, r, dir, "rev-list", "--count", from+".."+to)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("parse commit count %q: %w", out, err)
	}
	return n, nil
}

// SyncSummary formats the human-facing result of a repository synchronization.
func SyncSummary(rep *output.Reporter, name string, pulled, pushed int) string {
	pulledCount := fmt.Sprintf("%d %s", pulled, commitNoun(pulled))
	if pulled > 0 {
		pulledCount = rep.Key(pulledCount)
	}
	pushedCount := fmt.Sprintf("%d %s", pushed, commitNoun(pushed))
	if pushed > 0 {
		pushedCount = rep.Key(pushedCount)
	}
	return fmt.Sprintf(
		"%s synchronized (pulled %s, pushed %s)",
		name,
		pulledCount,
		pushedCount,
	)
}

func commitNoun(count int) string {
	if count == 1 {
		return "commit"
	}
	return "commits"
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
