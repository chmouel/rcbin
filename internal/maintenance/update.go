package maintenance

import (
	"context"
	"fmt"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

// Update runs update tasks sequentially.
type Update struct {
	R      runner.Runner
	Rep    *output.Reporter
	Shell  string
	GOOS   string
	DryRun bool
}

// Run executes the selected update tasks in order.
func (u *Update) Run(ctx context.Context, tasks []config.ResolvedUpdate, filter []string) error {
	var failures int
	for _, task := range tasks {
		if !selected(task.Name, filter) {
			continue
		}
		if !platformMatches(task.Platforms, currentGOOS(u.GOOS)) {
			u.Rep.Debugf("update %q skipped: platform", task.Name)
			continue
		}
		if missing, ok := requirementsMet(u.R, task.Requires); !ok {
			u.Rep.Skipf("update %s skipped: %s not available", task.Name, missing)
			continue
		}
		if u.DryRun {
			u.Rep.Infof("[dry-run] would run update task %s", task.Name)
			continue
		}
		if err := u.runOne(ctx, task); err != nil {
			failures++
			u.Rep.Errorf("update %s failed: %v", task.Name, err)
			continue
		}
		u.Rep.Successf("%s updated", task.Name)
	}
	if failures > 0 {
		return fmt.Errorf("%d update task(s) failed", failures)
	}
	return nil
}

func (u *Update) runOne(ctx context.Context, task config.ResolvedUpdate) error {
	shell := u.Shell
	if shell == "" {
		shell = "sh"
	}
	for _, c := range task.Commands {
		spec := commandSpec(c, task.Dir, shell)
		spec.Interactive = true
		if _, err := u.R.Run(ctx, spec); err != nil {
			if task.ContinueOnError {
				u.Rep.Warnf("update %s: command failed (continuing): %v", task.Name, err)
				continue
			}
			return err
		}
	}
	return nil
}
