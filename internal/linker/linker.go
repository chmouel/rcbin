// Package linker resolves the complete desired set of symlinks, applies it
// without ever overwriting real files, records managed links in a manifest, and
// removes only stale managed links.
package linker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

// Plan is a single desired link.
type Plan struct {
	Source     string
	Target     string
	Optional   bool
	Privileged bool
	Kind       string
}

// Linker applies a link plan.
type Linker struct {
	R            runner.Runner
	Rep          *output.Reporter
	FS           FileSystem
	Home         string
	ManifestPath string
	DryRun       bool
}

// New returns a production linker.
func New(r runner.Runner, rep *output.Reporter, home, manifestPath string, dryRun bool) *Linker {
	return &Linker{R: r, Rep: rep, FS: OSFS{}, Home: home, ManifestPath: manifestPath, DryRun: dryRun}
}

// BuildPlan computes the desired link set from the configuration, including
// discovered Zsh completions for binaries.
func (l *Linker) BuildPlan(cfg *config.Config) []Plan {
	var plans []Plan
	for _, link := range cfg.Links {
		plans = append(plans, Plan{
			Source:     link.Source,
			Target:     link.Target,
			Optional:   link.Optional,
			Privileged: link.Privileged,
			Kind:       "link",
		})
	}

	zshHostDir := filepath.Join(cfg.Roots["zsh"], "functions", "hosts", cfg.Hostname)
	for _, bin := range cfg.Bins {
		plans = append(plans, Plan{
			Source:   bin.Source,
			Target:   bin.Target,
			Optional: bin.Optional,
			Kind:     "bin",
		})
		if bin.DiscoverCompletion {
			comp := filepath.Join(filepath.Dir(bin.Source), "_"+filepath.Base(bin.Source))
			if _, err := l.FS.Lstat(comp); err == nil {
				plans = append(plans, Plan{
					Source:   comp,
					Target:   filepath.Join(zshHostDir, "_"+filepath.Base(bin.Source)),
					Optional: true,
					Kind:     "completion",
				})
			}
		}
	}
	return plans
}

// Apply validates and applies the plan, updating the managed-link manifest.
func (l *Linker) Apply(ctx context.Context, plans []Plan) error {
	if err := detectConflicts(plans); err != nil {
		return err
	}

	old, err := LoadManifest(l.ManifestPath)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}
	next := &Manifest{Links: map[string]string{}}

	var errs []error
	for _, p := range plans {
		if err := l.linkOne(ctx, p, next); err != nil {
			l.Rep.Errorf("%s: %v", p.Target, err)
			errs = append(errs, fmt.Errorf("%s: %w", p.Target, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	l.removeStale(ctx, old, next)

	if !l.DryRun {
		if err := next.Save(l.ManifestPath); err != nil {
			return fmt.Errorf("saving manifest: %w", err)
		}
	}
	return nil
}

func detectConflicts(plans []Plan) error {
	bySource := map[string]string{}
	for _, p := range plans {
		if prev, ok := bySource[p.Target]; ok && prev != p.Source {
			return fmt.Errorf("conflicting links for %s: %s and %s", p.Target, prev, p.Source)
		}
		bySource[p.Target] = p.Source
	}
	return nil
}

func (l *Linker) linkOne(ctx context.Context, p Plan, next *Manifest) error {
	if _, err := l.FS.Lstat(p.Source); err != nil {
		if p.Optional {
			l.Rep.Debugf("optional source missing, skipping: %s", p.Source)
			return nil
		}
		return fmt.Errorf("source does not exist: %s", p.Source)
	}

	if !p.Privileged && !l.underHome(p.Target) {
		return fmt.Errorf("target outside home requires privileged=true: %s", p.Target)
	}
	privileged := p.Privileged

	// Inspect the target. A real (non-symlink) file or directory is never
	// replaced; an existing symlink is refreshed.
	if info, err := l.FS.Lstat(p.Target); err == nil {
		if !isSymlink(info) {
			return fmt.Errorf("%s exists and is not a symlink; not overriding", p.Target)
		}
		if l.DryRun {
			l.Rep.Infof("[dry-run] would refresh link %s -> %s", p.Target, p.Source)
			next.Links[p.Target] = p.Source
			return nil
		}
		if err := l.remove(ctx, p.Target, privileged); err != nil {
			return err
		}
	}

	source := l.linkSource(p.Target, p.Source)

	if l.DryRun {
		l.Rep.Infof("[dry-run] would link %s -> %s", p.Target, source)
		next.Links[p.Target] = p.Source
		return nil
	}

	if err := l.ensureParent(ctx, p.Target, privileged); err != nil {
		return err
	}
	if err := l.symlink(ctx, source, p.Target, privileged); err != nil {
		return err
	}
	next.Links[p.Target] = p.Source
	return nil
}

func (l *Linker) removeStale(ctx context.Context, old, next *Manifest) {
	for _, target := range old.Targets() {
		if _, kept := next.Links[target]; kept {
			continue
		}
		info, err := l.FS.Lstat(target)
		if err != nil {
			continue // already gone
		}
		if !isSymlink(info) {
			continue // no longer a link we own; leave it
		}
		if !l.linkMatchesSource(target, old.Links[target]) {
			continue // user repointed the symlink; leave it
		}
		if l.DryRun {
			l.Rep.Infof("[dry-run] would remove stale link %s", target)
			continue
		}
		if err := l.remove(ctx, target, !l.underHome(target)); err != nil {
			l.Rep.Warnf("could not remove stale link %s: %v", target, err)
			continue
		}
		l.Rep.Debugf("removed stale link %s", target)
	}
}

// linkSource prefers a relative link target when one can be computed.
func (l *Linker) linkSource(target, source string) string {
	rel, err := filepath.Rel(filepath.Dir(target), source)
	if err != nil {
		return source
	}
	return rel
}

func (l *Linker) underHome(p string) bool {
	if l.Home == "" {
		return true
	}
	rel, err := filepath.Rel(l.Home, p)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}

func (l *Linker) linkMatchesSource(target, source string) bool {
	dest, err := l.FS.Readlink(target)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(filepath.Dir(target), dest)
	}
	return filepath.Clean(dest) == filepath.Clean(source)
}

func (l *Linker) ensureParent(ctx context.Context, target string, privileged bool) error {
	dir := filepath.Dir(target)
	if privileged {
		_, err := l.R.Run(ctx, runner.Spec{Name: "sudo", Args: []string{"mkdir", "-p", dir}})
		return err
	}
	return l.FS.MkdirAll(dir, 0o755)
}

func (l *Linker) symlink(ctx context.Context, source, target string, privileged bool) error {
	if privileged {
		_, err := l.R.Run(ctx, runner.Spec{Name: "sudo", Args: []string{"ln", "-sfn", source, target}})
		return err
	}
	return l.FS.Symlink(source, target)
}

func (l *Linker) remove(ctx context.Context, target string, privileged bool) error {
	if privileged {
		_, err := l.R.Run(ctx, runner.Spec{Name: "sudo", Args: []string{"rm", "-f", target}})
		return err
	}
	if err := l.FS.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
