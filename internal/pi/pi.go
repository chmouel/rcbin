// Package pi keeps the Pi coding agent configuration in a canonical Git
// repository and symlinks a curated set of entries into the agent directory.
//
// rc owns configuration placement only: links, first-run seeding, adoption of
// conflicting local files, and per-host overrides. Pi owns package installation,
// both through its startup resolver and through `pi update --extensions`. rc
// never parses `pi list`, never calls `pi install`, and never rewrites the
// canonical settings.json.
package pi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

const (
	// SettingsFile is the canonical Pi settings file, where configured package
	// sources live.
	SettingsFile = "settings.json"
	// AuthFile holds provider credentials. It is never linked; with SyncAuth
	// it is kept in sync with an encrypted pass entry.
	AuthFile = "auth.json"
	// HostDir is the directory holding per-host entry overrides.
	HostDir = "hosts"

	minPiMajor, minPiMinor, minPiPatch = 1, 3, 0
)

var hostNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type entry struct {
	name  string
	isDir bool
}

func curatedEntries() []entry {
	return []entry{
		{name: SettingsFile},
		{name: "models.json"},
		{name: "keybindings.json"},
		{name: "mcp.json"},
		{name: "AGENTS.md"},
		{name: "extensions", isDir: true},
		{name: "skills", isDir: true},
		{name: "prompts", isDir: true},
		{name: "themes", isDir: true},
	}
}

// Syncer links the canonical Pi agent configuration into an agent directory.
type Syncer struct {
	R   runner.Runner
	Rep *output.Reporter

	AgentDir string
	Source   string
	Host     string

	DryRun bool
	// Adopt allows backing up conflicting local entries before linking.
	Adopt bool
	// SyncAuth keeps auth.json in sync with the AuthEntry pass entry.
	SyncAuth bool
	// AuthEntry is the pass entry name, DefaultAuthEntry when empty.
	AuthEntry string
	// PasswordStore is the pass store directory.
	PasswordStore string
	// AuthState is the file recording the last synced auth.json state.
	AuthState string
	// AllowCreate permits creating a missing Source. It is set when the user
	// configured [pi].source explicitly or when roots.desktop_config exists.
	AllowCreate bool

	// Injection points for failure-injection tests.
	renamePath  func(oldPath, newPath string) error
	symlinkPath func(oldPath, newPath string) error
	chmodFile   func(f *os.File, mode os.FileMode) error
	// piLockWait bounds the wait for Pi's auth.json lock; zero is the default.
	piLockWait time.Duration
	// afterPreflight runs after the unlocked preflight, so tests can mutate the
	// targets and prove Sync repeats its checks under the locks.
	afterPreflight func()
}

func (s *Syncer) rename() func(string, string) error {
	if s.renamePath != nil {
		return s.renamePath
	}
	return renameNoReplace
}

func (s *Syncer) symlink() func(string, string) error {
	if s.symlinkPath != nil {
		return s.symlinkPath
	}
	return os.Symlink
}

func (s *Syncer) chmod() func(*os.File, os.FileMode) error {
	if s.chmodFile != nil {
		return s.chmodFile
	}
	return func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) }
}

// Sync links the curated entries, seeding or adopting local files as needed.
func (s *Syncer) Sync(ctx context.Context) error {
	if s.AgentDir == "" || s.Source == "" {
		s.Rep.Debugf("pi: agent directory or source is not configured, skipping")
		return nil
	}

	agentDir, err := absolutePath(s.AgentDir)
	if err != nil {
		return err
	}
	source, err := absolutePath(s.Source)
	if err != nil {
		return err
	}
	canonAgent, err := canonicalPath(agentDir)
	if err != nil {
		return err
	}
	canonSource, err := canonicalPath(source)
	if err != nil {
		return err
	}
	if err := checkOverlap(canonAgent, canonSource); err != nil {
		return err
	}
	if err := checkIdenticalTrees(canonAgent, canonSource); err != nil {
		return err
	}
	if err := validateHost(s.Host); err != nil {
		return err
	}
	if _, ok := s.R.LookPath("pi"); !ok {
		s.Rep.Debugf("pi: not installed, skipping agent configuration sync")
		return nil
	}

	// The source check comes before preflight: a machine without the canonical
	// checkout must no-op, not report adoption conflicts for local entries.
	sourceKind, err := lstatKind(source)
	if err != nil {
		return err
	}
	if sourceKind == kindAbsent && !s.AllowCreate {
		s.Rep.Debugf("pi: source %s does not exist, skipping agent configuration sync", source)
		return nil
	}
	if sourceKind != kindAbsent && sourceKind != kindDir {
		return fmt.Errorf("pi source %s is not a directory", source)
	}

	s.warnPlaintextAuth(source)
	entries := curatedEntries()
	plan, fatal, conflicts := s.preflight(source, agentDir, entries)
	if len(fatal) > 0 {
		return errors.Join(fatal...)
	}
	if len(conflicts) > 0 && !s.Adopt {
		return adoptionRequired(conflicts)
	}
	if s.afterPreflight != nil {
		s.afterPreflight()
	}

	if s.DryRun {
		s.reportPlan(plan, sourceKind == kindAbsent, agentDir, source)
		if k, err := lstatKind(agentDir); err != nil || k == kindAbsent {
			return err
		}
		return s.syncAuth(ctx, agentDir)
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return fmt.Errorf("creating pi agent directory %s: %w", agentDir, err)
	}
	if sourceKind == kindAbsent {
		if err := os.MkdirAll(source, 0o755); err != nil {
			return fmt.Errorf("creating pi source %s: %w", source, err)
		}
	}
	// Now that both directories exist, a case-insensitive alias is visible to
	// os.SameFile. Abort before anything is moved.
	if err := checkIdenticalTrees(canonAgent, canonSource); err != nil {
		return err
	}

	locks, err := acquireLocks(lockPaths(canonSource, canonAgent))
	if err != nil {
		return err
	}
	defer locks.release()

	// Recompute the plan under the locks: a concurrent run, or Pi itself, may
	// have changed a target since the unlocked preflight.
	plan, fatal, conflicts = s.preflight(source, agentDir, entries)
	if len(fatal) > 0 {
		return errors.Join(fatal...)
	}
	if len(conflicts) > 0 && !s.Adopt {
		return adoptionRequired(conflicts)
	}

	var (
		failures                         []error
		linked, seeded, adopted, skipped int
	)
	for i := range plan {
		a := &plan[i]
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := s.apply(a); err != nil {
			failures = append(failures, err)
			s.Rep.Errorf("pi: %v", err)
			continue
		}
		switch a.kind {
		case actLink:
			linked++
			s.Rep.Successf("pi: linked %s", a.targetPath)
		case actSeed:
			seeded++
			s.Rep.Successf("pi: seeded %s into %s", a.targetPath, a.sourcePath)
		case actAdopt:
			adopted++
			s.Rep.Successf("pi: adopted %s (backup %s)", a.targetPath, a.backupPath)
		case actSkip:
			skipped++
		}
	}

	if err := s.syncAuth(ctx, agentDir); err != nil {
		failures = append(failures, err)
		s.Rep.Errorf("pi: %v", err)
	}

	if len(plan) > 0 {
		s.Rep.Infof("pi: linked=%d seeded=%d adopted=%d skipped=%d failed=%d", linked, seeded, adopted, skipped, len(failures))
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(append(failures, err)...)
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	if err := s.reconcile(ctx); err != nil {
		return errors.Join(append(failures, err)...)
	}
	return nil
}

func adoptionRequired(conflicts []error) error {
	return fmt.Errorf("pi configuration conflicts; rerun with --adopt-pi to back them up and link the canonical entries: %w", errors.Join(conflicts...))
}

func entryKindName(isDir bool) string {
	if isDir {
		return kindDir.String()
	}
	return kindFile.String()
}

func validateHost(host string) error {
	if !hostNamePattern.MatchString(host) {
		return fmt.Errorf("pi: invalid host name %q", host)
	}
	return nil
}

type actionKind int

const (
	actSkip actionKind = iota
	actLink
	actSeed
	actAdopt
)

type action struct {
	kind       actionKind
	name       string
	sourcePath string
	targetPath string
	backupPath string
}

func (s *Syncer) preflight(source, agentDir string, entries []entry) (plan []action, fatal, conflicts []error) {
	canonAgent, err := canonicalPath(agentDir)
	if err != nil {
		return nil, []error{err}, nil
	}
	for _, e := range entries {
		sharedPath := filepath.Join(source, e.name)
		hostPath := filepath.Join(source, HostDir, s.Host, e.name)

		srcPath := sharedPath
		srcKind, err := entryKind(sharedPath, e)
		if err != nil {
			fatal = append(fatal, err)
			continue
		}
		hostKind, err := entryKind(hostPath, e)
		if err != nil {
			fatal = append(fatal, err)
			continue
		}
		if hostKind != kindAbsent {
			srcPath, srcKind = hostPath, hostKind
		}
		// A canonical entry that is a symlink into the agent directory would be
		// linked into a self-referential loop once its referent is adopted.
		// entryKind follows source symlinks, so look at the raw entry here.
		rawKind, err := lstatKind(srcPath)
		if err != nil {
			fatal = append(fatal, err)
			continue
		}
		if rawKind == kindSymlink {
			resolved, err := canonicalPath(srcPath)
			if err != nil {
				fatal = append(fatal, err)
				continue
			}
			if within(resolved, canonAgent) {
				fatal = append(fatal, fmt.Errorf("pi: canonical %s is a symlink into the agent directory (%s); move the real file into the canonical tree instead", srcPath, resolved))
				continue
			}
		}
		srcPresent := srcKind != kindAbsent

		targetPath := filepath.Join(agentDir, e.name)
		tgtKind, err := lstatKind(targetPath)
		if err != nil {
			fatal = append(fatal, err)
			continue
		}

		a := action{name: e.name, sourcePath: srcPath, targetPath: targetPath}
		switch tgtKind {
		case kindAbsent:
			if srcPresent {
				a.kind = actLink
				plan = append(plan, a)
			} else {
				a.kind = actSkip
			}
		case kindSymlink:
			linked, err := samePath(targetPath, srcPath)
			if err != nil {
				fatal = append(fatal, err)
				continue
			}
			switch {
			case srcPresent && linked:
				a.kind = actSkip
			case !srcPresent:
				s.Rep.Warnf("pi: %s is a symlink but %s does not exist; leaving it untouched", targetPath, srcPath)
				a.kind = actSkip
			case s.Adopt:
				a.kind = actAdopt
				plan = append(plan, a)
			default:
				conflicts = append(conflicts, fmt.Errorf("%s is a symlink that does not point to %s", targetPath, srcPath))
			}
		case kindFile, kindDir:
			if (tgtKind == kindDir) != e.isDir {
				fatal = append(fatal, fmt.Errorf("pi: local %s is a %s but the canonical entry %s must be a %s; move it aside before syncing",
					targetPath, tgtKind, e.name, entryKindName(e.isDir)))
				continue
			}
			switch {
			case srcPresent && s.Adopt:
				a.kind = actAdopt
				plan = append(plan, a)
			case srcPresent:
				conflicts = append(conflicts, fmt.Errorf("%s already exists and %s also exists", targetPath, srcPath))
			default:
				// Seed into the shared tree, which is the only copy in play.
				a.kind = actSeed
				a.sourcePath = sharedPath
				plan = append(plan, a)
			}
		default:
			fatal = append(fatal, fmt.Errorf("pi: %s is not a regular file or directory", targetPath))
		}
	}
	return plan, fatal, conflicts
}

func (s *Syncer) apply(a *action) error {
	switch a.kind {
	case actSkip:
		return nil
	case actLink:
		if err := os.MkdirAll(filepath.Dir(a.targetPath), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(a.targetPath), err)
		}
		if err := s.symlink()(a.sourcePath, a.targetPath); err != nil {
			return fmt.Errorf("linking %s -> %s: %w", a.targetPath, a.sourcePath, err)
		}
		return nil
	case actSeed:
		if err := os.MkdirAll(filepath.Dir(a.sourcePath), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(a.sourcePath), err)
		}
		if err := s.moveNoReplace(a.targetPath, a.sourcePath); err != nil {
			return err
		}
		return s.linkInto(a.sourcePath, a.targetPath, a.sourcePath)
	case actAdopt:
		backup, err := nextFreeBackup(a.targetPath)
		if err != nil {
			return err
		}
		if err := s.moveNoReplace(a.targetPath, backup); err != nil {
			return err
		}
		a.backupPath = backup
		return s.linkInto(a.sourcePath, a.targetPath, backup)
	}
	return nil
}

// linkInto creates the symlink and restores the moved entry when that fails. It
// never overwrites a target that reappeared, and it never restores on a stat
// error it cannot interpret.
func (s *Syncer) linkInto(sourcePath, targetPath, movedTo string) error {
	err := s.symlink()(sourcePath, targetPath)
	if err == nil {
		return nil
	}

	if _, statErr := os.Lstat(targetPath); statErr == nil || !errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Errorf("linking %s -> %s failed (%w) and %s was left at %s because the target is no longer absent: %w",
			targetPath, sourcePath, err, movedTo, movedTo, statErr)
	}
	if restoreErr := s.moveNoReplace(movedTo, targetPath); restoreErr != nil {
		return errors.Join(
			fmt.Errorf("linking %s -> %s: %w", targetPath, sourcePath, err),
			fmt.Errorf("restoring %s to %s: %w", movedTo, targetPath, restoreErr),
		)
	}
	return fmt.Errorf("linking %s -> %s: %w (restored the original from %s)", targetPath, sourcePath, err, movedTo)
}

func (s *Syncer) reportPlan(plan []action, createSource bool, agentDir, source string) {
	if createSource {
		s.Rep.Infof("pi: would create %s", source)
	}
	if k, err := lstatKind(agentDir); err == nil && k == kindAbsent {
		s.Rep.Infof("pi: would create %s", agentDir)
	}
	if len(plan) == 0 {
		s.Rep.Infof("pi: nothing to link")
	}
	for _, a := range plan {
		switch a.kind {
		case actLink:
			s.Rep.Infof("pi: would link %s", a.targetPath)
		case actSeed:
			s.Rep.Infof("pi: would seed %s into %s", a.targetPath, a.sourcePath)
		case actAdopt:
			s.Rep.Infof("pi: would back up %s and link %s", a.targetPath, a.sourcePath)
		}
	}
	s.Rep.Infof("pi: would refresh installed packages with pi update --extensions")
}
