package repo

import (
	"context"
	"os"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/runner"
)

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// Scan returns the canonical paths of repositories that have local changes. It
// performs no network I/O. Non-existent or non-git paths are skipped. Results
// preserve the input order with duplicates removed by canonical path.
func Scan(ctx context.Context, r runner.Runner, repos []config.RepoTarget) []string {
	seen := map[string]bool{}
	var changed []string
	for _, repo := range repos {
		if !isDir(repo.Path) {
			continue
		}
		key := canonical(repo.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		if !IsWorkTree(ctx, r, repo.Path) {
			continue
		}
		if HasChanges(ctx, r, repo.Path) {
			changed = append(changed, repo.Path)
		}
	}
	return changed
}
