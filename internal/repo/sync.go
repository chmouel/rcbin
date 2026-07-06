package repo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

// ErrAbort signals that the user chose to quit the interactive sync (for
// example by pressing q or Ctrl-C at the dirty-repository prompt). Callers treat
// it as a clean, user-initiated exit rather than a synchronization failure.
var ErrAbort = errors.New("aborted")

// DirtyHandler processes a repository that has local changes. Implementations
// may launch interactive tools; the non-interactive implementation returns an
// error.
type DirtyHandler interface {
	Handle(ctx context.Context, repo config.RepoTarget, name string) (headChanged bool, err error)
}

// Syncer synchronizes a set of repositories.
type Syncer struct {
	R      runner.Runner
	Rep    *output.Reporter
	Dirty  DirtyHandler
	Limit  int
	DryRun bool
	Shell  string
}

type result struct {
	repo        config.RepoTarget
	name        string
	lines       []string
	err         error
	headChanged bool
	attempted   bool
}

// Sync inspects and synchronizes the repositories, returning an error if any
// repository fails.
func (s *Syncer) Sync(ctx context.Context, repos []config.RepoTarget) error {
	targets := s.prepare(repos)
	if len(targets) == 0 {
		return nil
	}

	clean := make([]int, 0, len(targets))
	dirty := make(map[int]bool)
	results := make([]result, len(targets))

	inspectProgress := s.Rep.Progress("Inspecting repositories", len(targets))
	for i, t := range targets {
		results[i].repo = t
		results[i].name = Name(ctx, s.R, t.Path)
		if HasChanges(ctx, s.R, t.Path) {
			dirty[i] = true
		} else {
			clean = append(clean, i)
		}
		inspectProgress.Advance(results[i].name)
	}
	inspectProgress.Stop()

	// Clean repositories run concurrently within a bounded worker pool.
	s.runClean(ctx, targets, clean, results)

	var failures int
	for i := range targets {
		if dirty[i] {
			results[i] = s.syncDirty(ctx, targets[i], results[i].name)
		}
		r := results[i]
		for _, line := range r.lines {
			s.Rep.Println(line)
		}
		if r.err != nil {
			// A user-initiated quit stops the whole sync immediately and is not
			// counted as a failure.
			if errors.Is(r.err, ErrAbort) {
				return ErrAbort
			}
			failures++
			s.Rep.Failf("%s: %v", r.name, r.err)
		}
		s.runHooks(ctx, r)
	}

	if failures > 0 {
		return fmt.Errorf("%d repository(ies) failed to synchronize", failures)
	}
	return nil
}

// prepare validates existence and removes duplicates by canonical path.
func (s *Syncer) prepare(repos []config.RepoTarget) []config.RepoTarget {
	seen := map[string]bool{}
	var out []config.RepoTarget
	for _, r := range repos {
		if !isDir(r.Path) {
			if !r.Optional {
				s.Rep.Warnf("repository directory does not exist: %s", r.Path)
			}
			continue
		}
		key := canonical(r.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

func (s *Syncer) runClean(ctx context.Context, targets []config.RepoTarget, clean []int, results []result) {
	limit := s.Limit
	if limit < 1 {
		limit = 4
	}
	progress := s.Rep.Progress("Synchronizing repositories", len(clean))
	defer progress.Stop()
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, idx := range clean {
		select {
		case <-ctx.Done():
			results[idx].err = ctx.Err()
			progress.Advance(results[idx].name)
			continue
		default:
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = s.syncClean(ctx, targets[i], results[i].name)
			progress.Advance(results[i].name)
		}(idx)
	}
	wg.Wait()
}

// syncClean pulls once, then pushes when ahead, reporting every failure.
func (s *Syncer) syncClean(ctx context.Context, t config.RepoTarget, name string) result {
	r := result{repo: t, name: name, attempted: true}
	if s.DryRun {
		r.lines = append(r.lines, fmt.Sprintf("[dry-run] would pull and push %s", name))
		return r
	}

	before := Head(ctx, s.R, t.Path)

	if _, err := git(ctx, s.R, t.Path, "pull", "--quiet"); err != nil {
		r.err = fmt.Errorf("pull failed: %w", err)
		r.headChanged = Head(ctx, s.R, t.Path) != before
		return r
	}

	if ahead, ok := Upstream(ctx, s.R, t.Path); ok && ahead > 0 {
		if _, err := git(ctx, s.R, t.Path, "push"); err != nil {
			r.err = fmt.Errorf("push failed: %w", err)
			r.headChanged = Head(ctx, s.R, t.Path) != before
			return r
		}
	}

	after := Head(ctx, s.R, t.Path)
	r.headChanged = before != after
	r.lines = append(r.lines, s.Rep.SuccessLine("%s has been synchronized", name))
	return r
}

func (s *Syncer) syncDirty(ctx context.Context, t config.RepoTarget, name string) result {
	r := result{repo: t, name: name, attempted: true}
	if s.DryRun {
		r.lines = append(r.lines, fmt.Sprintf("[dry-run] %s has local changes", name))
		return r
	}
	if s.Dirty == nil {
		r.err = fmt.Errorf("repository has local changes and no interactive handler is available")
		return r
	}
	changed, err := s.Dirty.Handle(ctx, t, name)
	r.headChanged = changed
	r.err = err
	return r
}

// runHooks applies the always hook after every attempt and the post_update hook
// only after a successful attempt that changed HEAD.
func (s *Syncer) runHooks(ctx context.Context, r result) {
	if !r.attempted {
		return
	}
	if r.err == nil && r.headChanged && r.repo.Hooks.PostUpdate != nil {
		s.runHook(ctx, r.repo.Path, r.name, "post_update", r.repo.Hooks.PostUpdate)
	}
	if r.repo.Hooks.Always != nil {
		s.runHook(ctx, r.repo.Path, r.name, "always", r.repo.Hooks.Always)
	}
}

func (s *Syncer) runHook(ctx context.Context, dir, repoName, hookName string, c *config.Command) {
	name := strings.ReplaceAll(hookName, "_", "-")
	spec := commandSpec(*c, dir, s.shell())

	s.Rep.Infof("Running %s hook for %s", name, s.Rep.Accent(repoName))
	s.Rep.Printf("  %s %s\n", s.Rep.Arrow(), s.Rep.Bold(formatCommand(spec)))
	if s.DryRun {
		s.Rep.Skipf("[dry-run] command not executed")
		return
	}

	progress := s.Rep.Progress("Executing "+name+" hook", 1)
	if _, err := s.R.Run(ctx, spec); err != nil {
		progress.Stop()
		s.Rep.Warnf("%s hook failed for %s: %v", name, repoName, err)
		return
	}
	progress.Advance(repoName)
	progress.Stop()
	s.Rep.Successf("%s hook completed for %s", name, repoName)
}

func (s *Syncer) shell() string {
	if s.Shell != "" {
		return s.Shell
	}
	return "sh"
}

// commandSpec converts a config.Command into a runner spec.
func commandSpec(c config.Command, dir, shell string) runner.Spec {
	if c.Shell != "" {
		return runner.Spec{Name: shell, Args: []string{"-c", c.Shell}, Dir: dir}
	}
	return runner.Spec{Name: c.Argv[0], Args: c.Argv[1:], Dir: dir}
}

func formatCommand(spec runner.Spec) string {
	parts := make([]string, 0, len(spec.Args)+1)
	parts = append(parts, quoteCommandArg(spec.Name))
	for _, arg := range spec.Args {
		parts = append(parts, quoteCommandArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteCommandArg(arg string) string {
	if arg != "" && strings.IndexFunc(arg, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_@%+=:,./-", r)
	}) == -1 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

func canonical(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// SortedNames returns repository names sorted, for deterministic test output.
func SortedNames(repos []config.RepoTarget) []string {
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, filepath.Base(r.Path))
	}
	sort.Strings(names)
	return names
}
