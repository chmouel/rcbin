package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// ReadFile parses a TOML configuration file. A missing file yields found=false
// and no error so optional layers are skipped cleanly.
func ReadFile(path string) (file File, found bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, false, nil
		}
		return File{}, false, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, &file); err != nil {
		return File{}, false, fmt.Errorf("parsing %s: %w", path, err)
	}
	return file, true, nil
}

// ResolveRoots merges the roots maps of the given layers (last value wins) and
// expands each one. It is used to locate the host overlays before the full
// configuration is built.
func ResolveRoots(layers []File, vars Vars) (map[string]string, error) {
	merged := map[string]string{}
	for _, f := range layers {
		for k, v := range f.Roots {
			merged[k] = v
		}
	}
	roots := map[string]string{}
	var errs []error
	for k, v := range merged {
		ex, err := expand(v, vars)
		if err != nil {
			errs = append(errs, fmt.Errorf("root %q: %w", k, err))
			continue
		}
		roots[k] = filepath.Clean(ex)
	}
	return roots, errors.Join(errs...)
}

// ordered is an insertion-ordered keyed collection. Inserting an existing key
// from a later layer overrides the value in place; inserting the same key twice
// within one layer is a conflict.
type ordered[T any] struct {
	keys  []string
	items map[string]T
	layer map[string]int
}

func newOrdered[T any]() *ordered[T] {
	return &ordered[T]{items: map[string]T{}, layer: map[string]int{}}
}

func (o *ordered[T]) put(layer int, key string, v T) error {
	if prev, ok := o.layer[key]; ok && prev == layer {
		return fmt.Errorf("duplicate %q within a single layer", key)
	}
	if _, ok := o.items[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.items[key] = v
	o.layer[key] = layer
	return nil
}

func (o *ordered[T]) values() []T {
	out := make([]T, 0, len(o.keys))
	for _, k := range o.keys {
		out = append(out, o.items[k])
	}
	return out
}

// Build merges the ordered layers into a fully resolved configuration. Layers
// must already be in merge order: defaults, global, common, sorted multi-host,
// then exact host. All resolution errors are aggregated.
func Build(layers []File, vars Vars, hostname string) (*Config, error) {
	roots, err := ResolveRoots(layers, vars)
	if err != nil {
		return nil, err
	}

	home := roots["home"]
	if home == "" {
		home = vars["HOME"]
	}
	repoBase := roots["repo_base"]
	desktopBin := roots["desktop_bin"]

	cfg := &Config{
		Hostname: hostname,
		Vars:     vars,
		Roots:    roots,
	}

	links := newOrdered[ResolvedLink]()
	bins := newOrdered[ResolvedBin]()
	repos := newOrdered[RepoTarget]()
	backups := newOrdered[ResolvedBackup]()
	updates := newOrdered[ResolvedUpdate]()

	var errs []error
	add := func(e error) {
		if e != nil {
			errs = append(errs, e)
		}
	}

	for i, f := range layers {
		// Scalars: last specified value wins.
		if f.Git.Provider != "" {
			cfg.Provider = f.Git.Provider
		}
		if f.Yadm.Remote != "" {
			cfg.Yadm.Remote = f.Yadm.Remote
		}
		if len(f.Yadm.Track) > 0 {
			track := make([]string, 0, len(f.Yadm.Track))
			for _, t := range f.Yadm.Track {
				ex, err := expand(t, vars)
				if err != nil {
					add(fmt.Errorf("yadm.track: %w", err))
					continue
				}
				track = append(track, filepath.Clean(ex))
			}
			cfg.Yadm.Track = track
		}
		if f.Sync.Concurrency != 0 {
			cfg.Concurrency = f.Sync.Concurrency
		}
		if f.Tools.Lazygit != "" {
			cfg.Tools.Lazygit = f.Tools.Lazygit
		}
		if f.Tools.Aicommit != "" {
			cfg.Tools.Aicommit = f.Tools.Aicommit
		}
		if f.Tools.Shell != "" {
			cfg.Tools.Shell = f.Tools.Shell
		}
		if f.Tools.PreferEmacs != nil {
			cfg.Tools.PreferEmacs = *f.Tools.PreferEmacs
		}
		if len(f.Doctor.Endpoints) > 0 {
			cfg.Doctor.Endpoints = f.Doctor.Endpoints
		}
		if f.Doctor.TimeoutSeconds != 0 {
			cfg.Doctor.TimeoutSeconds = f.Doctor.TimeoutSeconds
		}

		// Default repositories key by resolved path.
		for _, r := range f.Git.Repositories {
			path, err := resolveRepoPath(r.Path, repoBase, vars)
			if err != nil {
				add(fmt.Errorf("git.repositories: %w", err))
				continue
			}
			add(repos.put(i, path, RepoTarget{Path: path, Clone: r.Clone, Optional: r.Optional}))
		}

		for _, l := range f.Links {
			rl, err := resolveLink(l, roots, vars, home)
			if err != nil {
				add(fmt.Errorf("link source=%q: %w", l.Source, err))
				continue
			}
			add(links.put(i, rl.Target, rl))
		}

		for _, b := range f.Bins {
			rb, err := resolveBin(b, roots, vars, home, desktopBin)
			if err != nil {
				add(fmt.Errorf("bin source=%q: %w", b.Source, err))
				continue
			}
			add(bins.put(i, rb.Target, rb))
		}

		for _, r := range f.Repositories {
			path, err := resolveRepoPath(r.Path, repoBase, vars)
			if err != nil {
				add(fmt.Errorf("repositories: %w", err))
				continue
			}
			if err := validateHooks(r.Hooks); err != nil {
				add(fmt.Errorf("repository %q hooks: %w", r.Path, err))
				continue
			}
			add(repos.put(i, path, RepoTarget{Path: path, Optional: false, Hooks: r.Hooks}))
		}

		for _, b := range f.Backups {
			rb, err := resolveBackup(b, roots, vars)
			if err != nil {
				add(fmt.Errorf("backup %q: %w", b.Name, err))
				continue
			}
			add(backups.put(i, rb.Name, rb))
		}

		for _, u := range f.Updates {
			ru, err := resolveUpdate(u, vars)
			if err != nil {
				add(fmt.Errorf("update %q: %w", u.Name, err))
				continue
			}
			add(updates.put(i, ru.Name, ru))
		}
	}

	cfg.Links = links.values()
	cfg.Bins = bins.values()
	cfg.Repos = repos.values()
	cfg.Backups = backups.values()
	cfg.Updates = updates.values()

	if cfg.ManifestPath == "" {
		base := roots["config"]
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		cfg.ManifestPath = filepath.Join(base, "rc", "managed-links.json")
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return cfg, nil
}

func resolveSource(rootName, source string, roots map[string]string, vars Vars) (string, error) {
	s, err := expand(source, vars)
	if err != nil {
		return "", err
	}
	if rootName != "" {
		root, ok := roots[rootName]
		if !ok {
			return "", fmt.Errorf("unknown source_root %q", rootName)
		}
		if filepath.IsAbs(s) {
			return filepath.Clean(s), nil
		}
		return filepath.Join(root, s), nil
	}
	if !filepath.IsAbs(s) {
		return "", fmt.Errorf("source %q requires source_root or an absolute path", source)
	}
	return filepath.Clean(s), nil
}

func resolveTarget(target string, vars Vars, home string) (string, error) {
	t, err := expand(target, vars)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(t) {
		return filepath.Clean(t), nil
	}
	return filepath.Join(home, t), nil
}

func resolveLink(l Link, roots map[string]string, vars Vars, home string) (ResolvedLink, error) {
	if l.Target == "" {
		return ResolvedLink{}, fmt.Errorf("link has no target")
	}
	src, err := resolveSource(l.SourceRoot, l.Source, roots, vars)
	if err != nil {
		return ResolvedLink{}, err
	}
	tgt, err := resolveTarget(l.Target, vars, home)
	if err != nil {
		return ResolvedLink{}, err
	}
	return ResolvedLink{Source: src, Target: tgt, Optional: l.Optional, Privileged: l.Privileged}, nil
}

func resolveBin(b Bin, roots map[string]string, vars Vars, home, desktopBin string) (ResolvedBin, error) {
	src, err := resolveSource(b.SourceRoot, b.Source, roots, vars)
	if err != nil {
		return ResolvedBin{}, err
	}
	target := b.Target
	if target == "" {
		target = filepath.Base(src)
	}
	t, err := expand(target, vars)
	if err != nil {
		return ResolvedBin{}, err
	}
	var tgt string
	switch {
	case filepath.IsAbs(t):
		tgt = filepath.Clean(t)
	case len(t) > 0 && containsSlash(t):
		tgt = filepath.Join(home, t)
	default:
		tgt = filepath.Join(desktopBin, t)
	}
	return ResolvedBin{Source: src, Target: tgt, Optional: b.Optional, DiscoverCompletion: b.DiscoverCompletion}, nil
}

func resolveRepoPath(path, repoBase string, vars Vars) (string, error) {
	p, err := expand(path, vars)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(repoBase, p)
	}
	return filepath.Clean(p), nil
}

func resolveBackup(b BackupTask, roots map[string]string, vars Vars) (ResolvedBackup, error) {
	if b.Name == "" {
		return ResolvedBackup{}, fmt.Errorf("backup task has no name")
	}
	if err := validateCommand(b.Command); err != nil {
		return ResolvedBackup{}, err
	}
	root, ok := roots[b.Repo]
	if !ok {
		return ResolvedBackup{}, fmt.Errorf("unknown repo root %q", b.Repo)
	}
	out, err := expand(b.Output, vars)
	if err != nil {
		return ResolvedBackup{}, err
	}
	return ResolvedBackup{
		Name:      b.Name,
		Platforms: b.Platforms,
		Requires:  b.Requires,
		Command:   b.Command,
		RepoRoot:  root,
		Output:    filepath.Join(root, out),
		Signoff:   b.Signoff,
	}, nil
}

func resolveUpdate(u UpdateTask, vars Vars) (ResolvedUpdate, error) {
	if u.Name == "" {
		return ResolvedUpdate{}, fmt.Errorf("update task has no name")
	}
	if len(u.Commands) == 0 {
		return ResolvedUpdate{}, fmt.Errorf("update task %q has no commands", u.Name)
	}
	for _, c := range u.Commands {
		if err := validateCommand(c); err != nil {
			return ResolvedUpdate{}, err
		}
	}
	dir := ""
	if u.Dir != "" {
		ex, err := expand(u.Dir, vars)
		if err != nil {
			return ResolvedUpdate{}, err
		}
		dir = ex
	}
	return ResolvedUpdate{
		Name:            u.Name,
		Platforms:       u.Platforms,
		Requires:        u.Requires,
		Commands:        u.Commands,
		Dir:             dir,
		ContinueOnError: u.ContinueOnError,
	}, nil
}

func validateCommand(c Command) error {
	hasArgv := len(c.Argv) > 0
	hasShell := c.Shell != ""
	switch {
	case hasArgv && hasShell:
		return fmt.Errorf("command sets both argv and shell")
	case !hasArgv && !hasShell:
		return fmt.Errorf("command sets neither argv nor shell")
	}
	return nil
}

func validateHooks(h Hooks) error {
	if h.PostUpdate != nil {
		if err := validateCommand(*h.PostUpdate); err != nil {
			return fmt.Errorf("post_update: %w", err)
		}
	}
	if h.Always != nil {
		if err := validateCommand(*h.Always); err != nil {
			return fmt.Errorf("always: %w", err)
		}
	}
	return nil
}

func containsSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}
