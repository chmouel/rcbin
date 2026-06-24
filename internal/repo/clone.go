package repo

import (
	"context"
	"fmt"
	"strings"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/runner"
)

// CloneURL expands a clone shorthand. A value without a slash is resolved as
// "<provider>/<value>.git"; anything else is used verbatim.
func CloneURL(clone, provider string) string {
	if clone == "" {
		return ""
	}
	if !strings.Contains(clone, "/") {
		return fmt.Sprintf("%s/%s.git", provider, clone)
	}
	return clone
}

// EnsureClone clones a repository when its path does not yet exist. It returns
// whether a clone was performed.
func EnsureClone(ctx context.Context, r runner.Runner, clone, provider, path string, dryRun bool) (bool, error) {
	if isDir(path) {
		return false, nil
	}
	url := CloneURL(clone, provider)
	if url == "" {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	if _, err := r.Run(ctx, runner.Spec{Name: "git", Args: []string{"clone", url, path}}); err != nil {
		return false, fmt.Errorf("cloning %s into %s: %w", url, path, err)
	}
	return true, nil
}

// EnsureClones clones every missing configured repository that declares a clone
// source, returning the first error encountered.
func EnsureClones(ctx context.Context, r runner.Runner, provider string, repos []config.RepoTarget, dryRun bool) error {
	for _, repo := range repos {
		if repo.Clone == "" {
			continue
		}
		if _, err := EnsureClone(ctx, r, repo.Clone, provider, repo.Path, dryRun); err != nil {
			return err
		}
	}
	return nil
}
