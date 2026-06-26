// Package linker resolves the complete desired set of symlinks, recreates the
// desktop binary directory, records managed links in a manifest, and removes
// only stale managed links outside that directory.
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
	DesktopBin   string
	DryRun       bool
}

// New returns a production linker.
func New(r runner.Runner, rep *output.Reporter, home, manifestPath, desktopBin string, dryRun bool) *Linker {
	return &Linker{
		R:            r,
		Rep:          rep,
		FS:           OSFS{},
		Home:         home,
		ManifestPath: manifestPath,
		DesktopBin:   desktopBin,
		DryRun:       dryRun,
	}
}

// BuildPlan computes the desired link set from the configuration, including
// discovered Zsh completions for binaries.
func (l *Linker) BuildPlan(cfg *config.Config) []Plan {
	var plans []Plan
	if plan, ok := zshRootPlan(cfg); ok {
		plans = append(plans, plan)
	}
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
			for _, base := range completionBases(bin) {
				comp := filepath.Join(filepath.Dir(bin.Source), "_"+base)
				if _, err := l.FS.Lstat(comp); err != nil {
					continue
				}
				plans = append(plans, Plan{
					Source:   comp,
					Target:   filepath.Join(zshHostDir, "_"+base),
					Optional: true,
					Kind:     "completion",
				})
			}
		}
	}
	return plans
}

func zshRootPlan(cfg *config.Config) (Plan, bool) {
	rcRoot := cfg.Roots["rc"]
	zshRoot := cfg.Roots["zsh"]
	if rcRoot == "" || zshRoot == "" {
		return Plan{}, false
	}
	return Plan{
		Source: filepath.Join(rcRoot, "zsh"),
		Target: zshRoot,
		Kind:   "link",
	}, true
}

func completionBases(bin config.ResolvedBin) []string {
	sourceBase := filepath.Base(bin.Source)
	targetBase := filepath.Base(bin.Target)
	if sourceBase == targetBase {
		return []string{sourceBase}
	}
	return []string{sourceBase, targetBase}
}

// Apply validates and applies the plan, updating the managed-link manifest.
func (l *Linker) Apply(ctx context.Context, plans []Plan) error {
	if err := detectConflicts(plans); err != nil {
		return err
	}
	if err := l.validatePlans(plans); err != nil {
		return err
	}
	plans = parentFirst(plans)

	old, err := LoadManifest(l.ManifestPath)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}
	l.Rep.Debugf("link plan: %d item(s) (%s); manifest has %d managed link(s)", len(plans), planKindSummary(plans), len(old.Links))
	next := &Manifest{Links: map[string]string{}}
	kindByTarget := map[string]string{}

	if err := l.prepareDesktopBin(); err != nil {
		return err
	}

	var errs []error
	for _, p := range plans {
		if err := l.linkOne(ctx, p, next); err != nil {
			l.Rep.Errorf("%s: %v", p.Target, err)
			errs = append(errs, fmt.Errorf("%s: %w", p.Target, err))
			continue
		}
		if _, ok := next.Links[p.Target]; ok {
			kindByTarget[p.Target] = planKind(p)
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
	l.reportLinkSummary(linkSummaryFromTargets(kindByTarget))
	return nil
}

func parentFirst(plans []Plan) []Plan {
	ordered := make([]Plan, 0, len(plans))
	for _, p := range plans {
		insertAt := len(ordered)
		for i, existing := range ordered {
			if pathContains(p.Target, existing.Target) {
				insertAt = i
				break
			}
		}
		ordered = append(ordered, Plan{})
		copy(ordered[insertAt+1:], ordered[insertAt:])
		ordered[insertAt] = p
	}
	return ordered
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
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

func (l *Linker) validatePlans(plans []Plan) error {
	var errs []error
	for _, p := range plans {
		if _, err := l.FS.Lstat(p.Source); err != nil {
			if p.Optional {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: source does not exist: %s", p.Target, p.Source))
			continue
		}
		if !p.Privileged && !l.underHome(p.Target) {
			errs = append(errs, fmt.Errorf("%s: target outside home requires privileged=true", p.Target))
		}
	}
	return errors.Join(errs...)
}

func (l *Linker) prepareDesktopBin() error {
	desktopBin := filepath.Clean(l.DesktopBin)
	if err := l.validateDesktopBin(desktopBin); err != nil {
		return err
	}
	if l.DryRun {
		l.Rep.Infof("[dry-run] would recreate desktop bin directory %s", desktopBin)
		return nil
	}
	if err := l.FS.RemoveAll(desktopBin); err != nil {
		return fmt.Errorf("cleaning desktop bin %s: %w", desktopBin, err)
	}
	if err := l.FS.MkdirAll(desktopBin, 0o755); err != nil {
		return fmt.Errorf("creating desktop bin %s: %w", desktopBin, err)
	}
	l.Rep.Debugf("recreated desktop bin directory %s", desktopBin)
	return nil
}

func (l *Linker) validateDesktopBin(desktopBin string) error {
	if l.DesktopBin == "" || desktopBin == "." {
		return fmt.Errorf("unsafe desktop_bin cleanup path: empty")
	}
	if !filepath.IsAbs(desktopBin) {
		return fmt.Errorf("unsafe desktop_bin cleanup path %q: must be absolute", l.DesktopBin)
	}
	if filepath.Dir(desktopBin) == desktopBin {
		return fmt.Errorf("unsafe desktop_bin cleanup path: %s", desktopBin)
	}
	if l.Home == "" {
		return nil
	}
	home := filepath.Clean(l.Home)
	if desktopBin == home {
		return fmt.Errorf("unsafe desktop_bin cleanup path equals home: %s", desktopBin)
	}
	if !l.underHome(desktopBin) {
		return fmt.Errorf("desktop_bin cleanup path outside home: %s", desktopBin)
	}
	return nil
}

type linkSummary struct {
	Links       int
	Bins        int
	Completions int
	Other       int
}

func linkSummaryFromTargets(kindByTarget map[string]string) linkSummary {
	var summary linkSummary
	for _, kind := range kindByTarget {
		summary.add(kind)
	}
	return summary
}

func (s *linkSummary) add(kind string) {
	switch kind {
	case "link":
		s.Links++
	case "bin":
		s.Bins++
	case "completion":
		s.Completions++
	default:
		s.Other++
	}
}

func (s linkSummary) total() int {
	return s.Links + s.Bins + s.Completions + s.Other
}

func (s linkSummary) detail() string {
	parts := []string{
		fmt.Sprintf("links=%d", s.Links),
		fmt.Sprintf("bins=%d", s.Bins),
		fmt.Sprintf("completions=%d", s.Completions),
	}
	if s.Other > 0 {
		parts = append(parts, fmt.Sprintf("other=%d", s.Other))
	}
	return strings.Join(parts, " ")
}

func (l *Linker) reportLinkSummary(summary linkSummary) {
	total := summary.total()
	noun := "targets"
	if total == 1 {
		noun = "target"
	}
	if l.DryRun {
		l.Rep.Infof("[dry-run] would link %d %s (%s)", total, noun, summary.detail())
		return
	}
	l.Rep.Successf("linked %d %s (%s)", total, noun, summary.detail())
}

func planKindSummary(plans []Plan) string {
	var links, bins, completions, other int
	for _, p := range plans {
		switch planKind(p) {
		case "link":
			links++
		case "bin":
			bins++
		case "completion":
			completions++
		default:
			other++
		}
	}
	parts := []string{
		fmt.Sprintf("links=%d", links),
		fmt.Sprintf("bins=%d", bins),
		fmt.Sprintf("completions=%d", completions),
	}
	if other > 0 {
		parts = append(parts, fmt.Sprintf("other=%d", other))
	}
	return strings.Join(parts, " ")
}

func planKind(p Plan) string {
	if p.Kind != "" {
		return p.Kind
	}
	return "link"
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
	refresh := false
	if info, err := l.FS.Lstat(p.Target); err == nil {
		if !isSymlink(info) {
			return fmt.Errorf("%s exists and is not a symlink; not overriding", p.Target)
		}
		refresh = true
		if l.DryRun {
			l.Rep.Infof("[dry-run] would refresh link %s -> %s", p.Target, p.Source)
			next.Links[p.Target] = p.Source
			return nil
		}
		if err := l.remove(ctx, p.Target, privileged); err != nil {
			return err
		}
	}

	if l.DryRun {
		source := l.linkSource(p.Target, p.Source)
		l.Rep.Infof("[dry-run] would link %s -> %s", p.Target, source)
		next.Links[p.Target] = p.Source
		return nil
	}

	if err := l.ensureParent(ctx, p.Target, privileged); err != nil {
		return err
	}
	source := l.linkSource(p.Target, p.Source)
	if err := l.symlink(ctx, source, p.Target, privileged); err != nil {
		return err
	}
	if refresh {
		l.Rep.Debugf("refreshed %s %s -> %s", planKind(p), p.Target, p.Source)
	} else {
		l.Rep.Debugf("created %s %s -> %s", planKind(p), p.Target, p.Source)
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
			l.Rep.Debugf("stale link already absent: %s", target)
			continue // already gone
		}
		if !isSymlink(info) {
			l.Rep.Debugf("stale manifest entry is not a symlink, leaving: %s", target)
			continue // no longer a link we own; leave it
		}
		if !l.linkMatchesSource(target, old.Links[target]) {
			l.Rep.Debugf("stale manifest entry was repointed, leaving: %s", target)
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
	parent := l.resolvedParent(target)
	source = l.resolvedSource(source)
	rel, err := filepath.Rel(parent, source)
	if err != nil {
		return source
	}
	return rel
}

func (l *Linker) resolvedParent(path string) string {
	parent := filepath.Dir(path)
	if resolved, err := l.FS.EvalSymlinks(parent); err == nil {
		return resolved
	}
	return parent
}

func (l *Linker) resolvedSource(source string) string {
	parent := filepath.Dir(source)
	resolved, err := l.FS.EvalSymlinks(parent)
	if err != nil {
		return source
	}
	// Keep the source leaf intact so a symlink source links to the symlink, not
	// to its resolved destination.
	return filepath.Join(resolved, filepath.Base(source))
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
