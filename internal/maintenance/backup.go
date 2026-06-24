package maintenance

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

// Backup runs backup tasks.
type Backup struct {
	R      runner.Runner
	Rep    *output.Reporter
	Shell  string
	GOOS   string
	DryRun bool
	Now    func() time.Time
}

// Run executes the selected backup tasks. It never pushes.
func (b *Backup) Run(ctx context.Context, tasks []config.ResolvedBackup, filter []string) error {
	now := b.Now
	if now == nil {
		now = time.Now
	}
	var failures int
	for _, task := range tasks {
		if !selected(task.Name, filter) {
			continue
		}
		if !platformMatches(task.Platforms, currentGOOS(b.GOOS)) {
			b.Rep.Debugf("backup %q skipped: platform", task.Name)
			continue
		}
		if missing, ok := requirementsMet(b.R, task.Requires); !ok {
			b.Rep.Skipf("backup %s skipped: %s not available", task.Name, missing)
			continue
		}
		if b.DryRun {
			b.Rep.Infof("[dry-run] would back up %s to %s", task.Name, task.Output)
			continue
		}
		if err := b.runOne(ctx, task, now()); err != nil {
			failures++
			b.Rep.Errorf("backup %s failed: %v", task.Name, err)
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d backup task(s) failed", failures)
	}
	return nil
}

func (b *Backup) runOne(ctx context.Context, task config.ResolvedBackup, now time.Time) error {
	spec := commandSpec(task.Command, task.RepoRoot, b.shell())
	res, err := b.R.Run(ctx, spec)
	if err != nil {
		return err
	}

	changed, err := writeIfChanged(task.Output, []byte(res.Stdout))
	if err != nil {
		return fmt.Errorf("writing %s: %w", task.Output, err)
	}
	if !changed {
		b.Rep.Skipf("%s backup unchanged", task.Name)
		return nil
	}

	rel, relErr := filepath.Rel(task.RepoRoot, task.Output)
	if relErr != nil {
		rel = task.Output
	}
	if _, err := b.R.Run(ctx, runner.Spec{Name: "git", Args: []string{"-C", task.RepoRoot, "add", "--", rel}, Dir: task.RepoRoot}); err != nil {
		return fmt.Errorf("staging %s: %w", rel, err)
	}
	args := []string{"-C", task.RepoRoot, "commit", "-m",
		fmt.Sprintf("%s update %s", task.Name, now.Format("2006-01-02"))}
	if task.Signoff {
		args = append(args, "-s")
	}
	args = append(args, "--", rel)

	if _, err := b.R.Run(ctx, runner.Spec{Name: "git", Args: args, Dir: task.RepoRoot}); err != nil {
		if _, diffErr := b.R.Run(ctx, runner.Spec{Name: "git", Args: []string{"-C", task.RepoRoot, "diff", "--cached", "--quiet", "--", rel}, Dir: task.RepoRoot}); diffErr == nil {
			b.Rep.Skipf("%s backup committed nothing", task.Name)
			return nil
		}
		return fmt.Errorf("committing %s: %w", rel, err)
	}
	b.Rep.Successf("%s backup done", task.Name)
	return nil
}

func (b *Backup) shell() string {
	if b.Shell != "" {
		return b.Shell
	}
	return "sh"
}
