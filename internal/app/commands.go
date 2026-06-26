package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/chmouel/rc/internal/commitui"
	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/doctor"
	"github.com/chmouel/rc/internal/linker"
	"github.com/chmouel/rc/internal/maintenance"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/repo"
	"github.com/chmouel/rc/internal/yadm"
)

// dirtyHandler returns the interactive adapter, or nil in non-interactive mode
// so dirty repositories become actionable errors.
func dirtyHandler(g *globals, deps Deps, rep *output.Reporter, cfg *config.Config) *commitui.Adapter {
	if g.nonInteractive {
		return nil
	}
	return &commitui.Adapter{
		R:     deps.Runner,
		Rep:   rep,
		Tools: cfg.Tools,
		Pr:    commitui.NewStdinPrompter(),
	}
}

// abortedByUser reports whether err is a user-initiated quit (q or Ctrl-C at the
// dirty-repository prompt) and, if so, prints a short notice. Callers return nil
// afterwards so rc exits cleanly without running later phases.
func abortedByUser(rep *output.Reporter, err error) bool {
	if errors.Is(err, repo.ErrAbort) {
		rep.Infof("aborted")
		return true
	}
	return false
}

func newStatusCmd(g *globals, deps Deps) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report repositories with local changes (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "text" && format != "waybar" {
				return fmt.Errorf("invalid --format %q: want text or waybar", format)
			}
			rep := newReporter(g, deps)
			cfg, err := loadConfig(g)
			if err != nil {
				return op(err)
			}
			dirty := repo.Scan(cmd.Context(), deps.Runner, cfg.Repos)

			if format == "waybar" {
				payload, err := output.Waybar(dirty)
				if err != nil {
					return op(err)
				}
				fmt.Fprintln(rep.Out(), payload)
				return nil
			}

			if len(dirty) == 0 {
				rep.Successf("all repositories are clean")
				return nil
			}
			rep.Infof("%d repository(ies) with local changes:", len(dirty))
			for _, path := range dirty {
				rep.Println(repo.Name(cmd.Context(), deps.Runner, path))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or waybar")
	return cmd
}

func newSyncCmd(g *globals, deps Deps) *cobra.Command {
	var (
		changedOnly bool
		repoPaths   []string
		skipYadm    bool
		yadmOnly    bool
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize Git repositories and YADM",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if skipYadm && yadmOnly {
				return fmt.Errorf("--skip-yadm and --yadm-only are mutually exclusive")
			}
			rep := newReporter(g, deps)
			cfg, err := loadConfig(g)
			if err != nil {
				return op(err)
			}
			ctx := cmd.Context()

			var errs []error
			if !yadmOnly {
				if err := syncRepos(ctx, g, deps, rep, cfg, repoPaths, changedOnly); err != nil {
					if abortedByUser(rep, err) {
						return nil
					}
					errs = append(errs, err)
				}
			}
			effectiveSkipYadm := skipYadm || (changedOnly && !yadmOnly)
			if !effectiveSkipYadm {
				if err := syncYadm(ctx, g, deps, rep, cfg); err != nil {
					errs = append(errs, err)
				}
			}
			return op(errors.Join(errs...))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&changedOnly, "changed-only", false, "only process repositories with local changes")
	f.StringArrayVar(&repoPaths, "repo", nil, "restrict to the given repository path(s)")
	f.BoolVar(&skipYadm, "skip-yadm", false, "skip YADM synchronization")
	f.BoolVar(&yadmOnly, "yadm-only", false, "only synchronize YADM")
	return cmd
}

func syncRepos(ctx context.Context, g *globals, deps Deps, rep *output.Reporter, cfg *config.Config, repoPaths []string, changedOnly bool) error {
	repos := cfg.Repos
	if len(repoPaths) > 0 {
		repos = filterRepos(repos, repoPaths)
		if len(repos) == 0 {
			return fmt.Errorf("no configured repository matches the requested path(s)")
		}
	}

	if changedOnly {
		changed := map[string]bool{}
		for _, p := range repo.Scan(ctx, deps.Runner, repos) {
			changed[canonicalPath(p)] = true
		}
		var filtered []config.RepoTarget
		for _, r := range repos {
			if changed[canonicalPath(r.Path)] {
				filtered = append(filtered, r)
			}
		}
		repos = filtered
		if len(repos) == 0 {
			rep.Successf("no repositories with local changes")
			return nil
		}
	}

	if err := repo.EnsureClones(ctx, deps.Runner, cfg.Provider, repos, g.dryRun); err != nil {
		rep.Warnf("clone step reported issues: %v", err)
	}

	syncer := &repo.Syncer{
		R:      deps.Runner,
		Rep:    rep,
		Dirty:  handlerOrNil(dirtyHandler(g, deps, rep, cfg)),
		Limit:  cfg.WorkerLimit(),
		DryRun: g.dryRun,
		Shell:  cfg.Shell(),
	}
	return syncer.Sync(ctx, repos)
}

// handlerOrNil converts a possibly-nil *Adapter into a repo.DirtyHandler that is
// genuinely nil so the syncer's nil check fires in non-interactive mode.
func handlerOrNil(a *commitui.Adapter) repo.DirtyHandler {
	if a == nil {
		return nil
	}
	return a
}

func syncYadm(ctx context.Context, g *globals, deps Deps, rep *output.Reporter, cfg *config.Config) error {
	var dirty yadm.Interactive
	if a := dirtyHandler(g, deps, rep, cfg); a != nil {
		dirty = a
	}
	syncer := &yadm.Syncer{
		R:              deps.Runner,
		Rep:            rep,
		Track:          cfg.Yadm.Track,
		StateDir:       cfg.Roots["yadm_state"],
		Dirty:          dirty,
		NonInteractive: g.nonInteractive,
		DryRun:         g.dryRun,
	}
	return syncer.Sync(ctx)
}

func newLinkCmd(g *globals, deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "link",
		Short: "Create managed symlinks, binaries, and completions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rep := newReporter(g, deps)
			cfg, err := loadConfig(g)
			if err != nil {
				return op(err)
			}
			ctx := cmd.Context()

			// Clone missing repositories before resolving their assets.
			if err := repo.EnsureClones(ctx, deps.Runner, cfg.Provider, cfg.Repos, g.dryRun); err != nil {
				rep.Warnf("clone step reported issues: %v", err)
			}

			home := cfg.Roots["home"]
			l := linker.New(deps.Runner, rep, home, cfg.ManifestPath, cfg.Roots["desktop_bin"], g.dryRun)
			plans := l.BuildPlan(cfg)
			return op(l.Apply(ctx, plans))
		},
	}
}

func newBackupCmd(g *globals, deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "backup [TASK...]",
		Short: "Run backup tasks and commit changed outputs",
		RunE: func(cmd *cobra.Command, args []string) error {
			rep := newReporter(g, deps)
			cfg, err := loadConfig(g)
			if err != nil {
				return op(err)
			}
			b := &maintenance.Backup{
				R:      deps.Runner,
				Rep:    rep,
				Shell:  cfg.Shell(),
				GOOS:   runtime.GOOS,
				DryRun: g.dryRun,
			}
			return op(b.Run(cmd.Context(), cfg.Backups, args))
		},
	}
}

func newUpdateCmd(g *globals, deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "update [TASK...]",
		Short: "Run system and tool update tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			rep := newReporter(g, deps)
			cfg, err := loadConfig(g)
			if err != nil {
				return op(err)
			}
			u := &maintenance.Update{
				R:      deps.Runner,
				Rep:    rep,
				Shell:  cfg.Shell(),
				GOOS:   runtime.GOOS,
				DryRun: g.dryRun,
			}
			return op(u.Run(cmd.Context(), cfg.Updates, args))
		},
	}
}

func newDoctorCmd(g *globals, deps Deps) *cobra.Command {
	var offline bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run diagnostic checks and print a summary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rep := newReporter(g, deps)
			cfg, err := loadConfig(g)
			if err != nil {
				return op(err)
			}
			d := &doctor.Doctor{
				R:       deps.Runner,
				Rep:     rep,
				Cfg:     cfg,
				Offline: offline,
			}
			_, err = d.Run(cmd.Context())
			return op(err)
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "skip network connectivity checks")
	return cmd
}

func newRunCmd(g *globals, deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run sync, link, and backup in order",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rep := newReporter(g, deps)
			cfg, err := loadConfig(g)
			if err != nil {
				return op(err)
			}
			ctx := cmd.Context()

			// Synchronize first. A sync failure makes further mutation unsafe.
			var syncErrs []error
			if err := syncRepos(ctx, g, deps, rep, cfg, nil, false); err != nil {
				if abortedByUser(rep, err) {
					return nil
				}
				syncErrs = append(syncErrs, err)
			}
			if err := syncYadm(ctx, g, deps, rep, cfg); err != nil {
				syncErrs = append(syncErrs, err)
			}
			if len(syncErrs) > 0 {
				rep.Errorf("synchronization failed; skipping link and backup")
				return op(errors.Join(syncErrs...))
			}

			// Link and backup are independent; aggregate their errors.
			var errs []error
			home := cfg.Roots["home"]
			l := linker.New(deps.Runner, rep, home, cfg.ManifestPath, cfg.Roots["desktop_bin"], g.dryRun)
			if err := l.Apply(ctx, l.BuildPlan(cfg)); err != nil {
				errs = append(errs, err)
			}
			b := &maintenance.Backup{R: deps.Runner, Rep: rep, Shell: cfg.Shell(), GOOS: runtime.GOOS, DryRun: g.dryRun}
			if err := b.Run(ctx, cfg.Backups, nil); err != nil {
				errs = append(errs, err)
			}
			return op(errors.Join(errs...))
		},
	}
}

func newConfigCmd(g *globals, deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect configuration",
	}
	validate := &cobra.Command{
		Use:   "validate",
		Short: "Load and validate the merged configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rep := newReporter(g, deps)
			cfg, err := loadConfig(g)
			if err != nil {
				return op(err)
			}
			rep.Infof("configuration valid for host %q", cfg.Hostname)
			rep.Infof("links=%d bins=%d repos=%d backups=%d updates=%d",
				len(cfg.Links), len(cfg.Bins), len(cfg.Repos), len(cfg.Backups), len(cfg.Updates))
			return nil
		},
	}
	cmd.AddCommand(validate)
	return cmd
}

// filterRepos keeps repositories whose canonical path or base name matches any
// of the requested paths.
func filterRepos(repos []config.RepoTarget, paths []string) []config.RepoTarget {
	want := map[string]bool{}
	base := map[string]bool{}
	for _, p := range paths {
		want[canonicalPath(p)] = true
		base[filepath.Base(p)] = true
	}
	var out []config.RepoTarget
	for _, r := range repos {
		if want[canonicalPath(r.Path)] || base[filepath.Base(r.Path)] {
			out = append(out, r)
		}
	}
	return out
}

func canonicalPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}
