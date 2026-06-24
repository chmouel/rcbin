// Package yadm synchronizes the YADM-tracked configuration repository using
// machine-readable status and ref queries, staging only configured paths,
// pulling at most once, and pushing only when the branch is ahead.
package yadm

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

// Interactive handles a dirty YADM repository (for example by launching
// lazygit). The non-interactive path returns an error instead.
type Interactive interface {
	YadmDirty(ctx context.Context, stateDir string) error
}

// Syncer synchronizes the YADM repository.
type Syncer struct {
	R              runner.Runner
	Rep            *output.Reporter
	Track          []string
	StateDir       string
	Dirty          Interactive
	NonInteractive bool
	DryRun         bool
}

func (s *Syncer) yadm(ctx context.Context, args ...string) (string, error) {
	res, err := s.R.Run(ctx, runner.Spec{Name: "yadm", Args: args})
	return strings.TrimSpace(res.Stdout), err
}

// Sync performs one YADM synchronization cycle.
func (s *Syncer) Sync(ctx context.Context) error {
	if _, ok := s.R.LookPath("yadm"); !ok {
		return fmt.Errorf("yadm is not installed")
	}
	if !s.initialized(ctx) {
		return fmt.Errorf("yadm is not initialized (state dir %s missing)", s.StateDir)
	}

	if s.DryRun {
		s.Rep.Infof("[dry-run] would stage %d path(s), pull once, and push if ahead", len(s.Track))
		return nil
	}

	if len(s.Track) > 0 {
		if _, err := s.yadm(ctx, append([]string{"add"}, s.Track...)...); err != nil {
			return fmt.Errorf("staging yadm paths: %w", err)
		}
	}

	if s.hasChanges(ctx) {
		if s.NonInteractive || s.Dirty == nil {
			return fmt.Errorf("yadm has uncommitted changes")
		}
		if err := s.Dirty.YadmDirty(ctx, s.StateDir); err != nil {
			return err
		}
		if s.hasChanges(ctx) {
			return fmt.Errorf("yadm still has uncommitted changes")
		}
	}

	// Pull exactly once.
	if _, err := s.yadm(ctx, "pull", "--quiet"); err != nil {
		return fmt.Errorf("yadm pull failed: %w", err)
	}

	// Push only when ahead.
	if ahead, ok := s.ahead(ctx); ok && ahead > 0 {
		if _, err := s.yadm(ctx, "push", "--quiet"); err != nil {
			return fmt.Errorf("yadm push failed: %w", err)
		}
	}

	remote, _ := s.yadm(ctx, "remote", "get-url", "origin")
	if remote != "" {
		s.Rep.Successf("Synced YADM to %s", remote)
	} else {
		s.Rep.Successf("Synced YADM")
	}
	return nil
}

func (s *Syncer) initialized(ctx context.Context) bool {
	if s.StateDir != "" {
		if info, err := os.Stat(s.StateDir); err == nil && info.IsDir() {
			return true
		}
	}
	_, err := s.yadm(ctx, "rev-parse", "--git-dir")
	return err == nil
}

func (s *Syncer) hasChanges(ctx context.Context) bool {
	out, err := s.yadm(ctx, "status", "--porcelain=v2")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

func (s *Syncer) ahead(ctx context.Context) (int, bool) {
	out, err := s.yadm(ctx, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, true
	}
	return n, true
}
