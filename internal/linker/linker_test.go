package linker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

func newTestLinker(t *testing.T, home string, dryRun bool) (*Linker, *runner.Fake) {
	t.Helper()
	l, fake, _ := newBufferedTestLinker(t, home, dryRun, false)
	return l, fake
}

func newBufferedTestLinker(t *testing.T, home string, dryRun, verbose bool) (*Linker, *runner.Fake, *bytes.Buffer) {
	t.Helper()
	fake := runner.NewFake()
	var out, errBuf bytes.Buffer
	rep := output.New(&out, &errBuf, false, verbose)
	desktopBin := filepath.Join(home, ".local", "bin", "desktop")
	l := New(fake, rep, home, filepath.Join(home, ".config", "rc", "manifest.json"), desktopBin, dryRun)
	return l, fake, &errBuf
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLinkNewAndStaleRemoval(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "src", "file")
	writeFile(t, src, "x")
	target := filepath.Join(home, ".config", "file")

	l, _ := newTestLinker(t, home, false)
	if err := l.Apply(context.Background(), []Plan{{Source: src, Target: target, Kind: "link"}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(target)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink at %s: %v", target, err)
	}

	// Re-apply with an empty plan: the previously managed link becomes stale.
	if err := l.Apply(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("stale managed link should be removed, got err=%v", err)
	}
}

func TestVerboseRealApplyReportsActions(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "src", "file")
	writeFile(t, src, "x")
	target := filepath.Join(home, ".config", "file")

	l, _, errBuf := newBufferedTestLinker(t, home, false, true)
	if err := l.Apply(context.Background(), []Plan{{Source: src, Target: target, Kind: "link"}}); err != nil {
		t.Fatal(err)
	}
	got := errBuf.String()
	if !strings.Contains(got, "link plan: 1 item(s)") {
		t.Fatalf("verbose output missing plan summary:\n%s", got)
	}
	if !strings.Contains(got, "created link "+target+" -> "+src) {
		t.Fatalf("verbose output missing created link action:\n%s", got)
	}

	errBuf.Reset()
	if err := l.Apply(context.Background(), []Plan{{Source: src, Target: target, Kind: "link"}}); err != nil {
		t.Fatal(err)
	}
	got = errBuf.String()
	if !strings.Contains(got, "refreshed link "+target+" -> "+src) {
		t.Fatalf("verbose output missing refreshed link action:\n%s", got)
	}

	errBuf.Reset()
	if err := l.Apply(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	got = errBuf.String()
	if !strings.Contains(got, "removed stale link "+target) {
		t.Fatalf("verbose output missing stale removal action:\n%s", got)
	}
}

func TestNonVerboseRealApplyReportsSummary(t *testing.T) {
	home := t.TempDir()
	linkSource := filepath.Join(home, "src", "file")
	binSource := filepath.Join(home, "src", "tool")
	completionSource := filepath.Join(home, "src", "_tool")
	writeFile(t, linkSource, "x")
	writeFile(t, binSource, "x")
	writeFile(t, completionSource, "x")
	linkTarget := filepath.Join(home, ".config", "file")

	l, _, errBuf := newBufferedTestLinker(t, home, false, false)
	if err := l.Apply(context.Background(), []Plan{
		{Source: linkSource, Target: linkTarget, Kind: "link"},
		{Source: binSource, Target: filepath.Join(l.DesktopBin, "tool"), Kind: "bin"},
		{Source: completionSource, Target: filepath.Join(home, ".config", "zsh", "functions", "hosts", "test", "_tool"), Kind: "completion"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := errBuf.String(); !strings.Contains(got, "linked 3 targets (links=1 bins=1 completions=1)") {
		t.Fatalf("non-verbose real apply should report link breakdown, got:\n%s", got)
	}
}

func TestRefuseOverwriteRealFile(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "src", "file")
	writeFile(t, src, "x")
	target := filepath.Join(home, ".config", "real")
	writeFile(t, target, "precious user data")

	l, _ := newTestLinker(t, home, false)
	if err := l.Apply(context.Background(), []Plan{{Source: src, Target: target}}); err == nil {
		t.Fatal("expected blocked real file to fail")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "precious user data" {
		t.Errorf("real file must not be overwritten, got %q", data)
	}
	if info, _ := os.Lstat(target); info.Mode()&os.ModeSymlink != 0 {
		t.Error("target must remain a regular file")
	}
}

func TestRequiredMissingSourceFails(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, ".config", "missing")
	l, _ := newTestLinker(t, home, false)
	err := l.Apply(context.Background(), []Plan{{Source: filepath.Join(home, "missing"), Target: target}})
	if err == nil || !strings.Contains(err.Error(), "source does not exist") {
		t.Fatalf("expected required missing source failure, got %v", err)
	}
}

func TestOptionalMissingSourceSkips(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, ".config", "missing")
	l, _ := newTestLinker(t, home, false)
	if err := l.Apply(context.Background(), []Plan{{Source: filepath.Join(home, "missing"), Target: target, Optional: true}}); err != nil {
		t.Fatalf("optional missing source should skip: %v", err)
	}
}

func TestUnmanagedFileUntouchedOnStaleSweep(t *testing.T) {
	home := t.TempDir()
	// Pre-seed a manifest claiming to manage a path that is actually a real
	// file. The stale sweep must not delete it.
	target := filepath.Join(home, ".config", "unmanaged")
	writeFile(t, target, "real")
	m := &Manifest{Links: map[string]string{target: "/whatever"}}
	manifestPath := filepath.Join(home, ".config", "rc", "manifest.json")
	if err := m.Save(manifestPath); err != nil {
		t.Fatal(err)
	}

	l, _ := newTestLinker(t, home, false)
	if err := l.Apply(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("unmanaged real file must survive stale sweep: %v", err)
	}
}

func TestDesktopBinCleanupRemovesUnmanagedEntriesAndUpdatesManifest(t *testing.T) {
	home := t.TempDir()
	l, _ := newTestLinker(t, home, false)
	desktopBin := l.DesktopBin
	staleFile := filepath.Join(desktopBin, "old-file")
	staleNested := filepath.Join(desktopBin, "nested", "old-file")
	oldSource := filepath.Join(home, "src", "old-tool")
	oldTarget := filepath.Join(desktopBin, "old-tool")
	newSource := filepath.Join(home, "src", "new-tool")
	newTarget := filepath.Join(desktopBin, "new-tool")
	writeFile(t, staleFile, "old")
	writeFile(t, staleNested, "old")
	writeFile(t, oldSource, "old")
	writeFile(t, newSource, "new")
	if err := os.Symlink(oldSource, oldTarget); err != nil {
		t.Fatal(err)
	}
	if err := (&Manifest{Links: map[string]string{oldTarget: oldSource}}).Save(l.ManifestPath); err != nil {
		t.Fatal(err)
	}

	if err := l.Apply(context.Background(), []Plan{{Source: newSource, Target: newTarget, Kind: "bin"}}); err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{staleFile, staleNested, oldTarget} {
		if _, err := os.Lstat(stale); !os.IsNotExist(err) {
			t.Fatalf("stale desktop entry %s should be removed, got err=%v", stale, err)
		}
	}
	if info, err := os.Lstat(newTarget); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("new desktop binary should be linked, info=%v err=%v", info, err)
	}
	entries, err := os.ReadDir(desktopBin)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "new-tool" {
		t.Fatalf("desktop bin should contain only current links, got %v", entries)
	}
	manifest, err := LoadManifest(l.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.Links[oldTarget]; ok {
		t.Fatalf("old desktop target should be removed from manifest: %v", manifest.Links)
	}
	if got := manifest.Links[newTarget]; got != newSource {
		t.Fatalf("new desktop target manifest source = %q, want %q", got, newSource)
	}
}

func TestDesktopBinCleanupDryRunDoesNotRemoveEntries(t *testing.T) {
	home := t.TempDir()
	l, _ := newTestLinker(t, home, true)
	desktopBin := l.DesktopBin
	staleFile := filepath.Join(desktopBin, "old-file")
	source := filepath.Join(home, "src", "tool")
	target := filepath.Join(desktopBin, "tool")
	writeFile(t, staleFile, "old")
	writeFile(t, source, "new")

	if err := l.Apply(context.Background(), []Plan{{Source: source, Target: target, Kind: "bin"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staleFile); err != nil {
		t.Fatalf("dry-run should leave stale desktop entry in place: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not create desktop link, got err=%v", err)
	}
	if _, err := os.Stat(l.ManifestPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not write manifest, got err=%v", err)
	}
}

func TestDesktopBinCleanupRejectsHomePath(t *testing.T) {
	home := t.TempDir()
	victim := filepath.Join(home, "victim")
	source := filepath.Join(home, "src", "tool")
	writeFile(t, victim, "keep")
	writeFile(t, source, "new")

	l, _ := newTestLinker(t, home, false)
	l.DesktopBin = home
	err := l.Apply(context.Background(), []Plan{{Source: source, Target: filepath.Join(home, ".local", "bin", "desktop", "tool"), Kind: "bin"}})
	if err == nil || !strings.Contains(err.Error(), "equals home") {
		t.Fatalf("expected unsafe desktop_bin error, got %v", err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("unsafe cleanup must not remove home contents: %v", err)
	}
}

func TestDesktopBinCleanupWaitsForValidPlan(t *testing.T) {
	home := t.TempDir()
	l, _ := newTestLinker(t, home, false)
	staleFile := filepath.Join(l.DesktopBin, "old-file")
	writeFile(t, staleFile, "old")

	missingSource := filepath.Join(home, "missing")
	err := l.Apply(context.Background(), []Plan{{Source: missingSource, Target: filepath.Join(l.DesktopBin, "tool"), Kind: "bin"}})
	if err == nil || !strings.Contains(err.Error(), "source does not exist") {
		t.Fatalf("expected missing source error, got %v", err)
	}
	if _, err := os.Stat(staleFile); err != nil {
		t.Fatalf("desktop bin should not be cleaned before preflight passes: %v", err)
	}
}

func TestRelativeLink(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "src", "file")
	writeFile(t, src, "x")
	target := filepath.Join(home, ".config", "file")

	l, _ := newTestLinker(t, home, false)
	if err := l.Apply(context.Background(), []Plan{{Source: src, Target: target}}); err != nil {
		t.Fatal(err)
	}
	dest, err := os.Readlink(target)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(dest) {
		t.Errorf("expected relative link, got %q", dest)
	}
	if dest != "../src/file" {
		t.Errorf("unexpected relative target %q", dest)
	}
}

func TestBuildPlanIncludesZshRootLink(t *testing.T) {
	home := t.TempDir()
	rcRoot := filepath.Join(home, ".local", "share", "rc")
	zshRoot := filepath.Join(home, ".config", "zsh")
	cfg := &config.Config{
		Roots: map[string]string{
			"rc":  rcRoot,
			"zsh": zshRoot,
		},
	}

	l, _ := newTestLinker(t, home, false)
	plans := l.BuildPlan(cfg)
	if len(plans) != 1 {
		t.Fatalf("expected one zsh root plan, got %d: %+v", len(plans), plans)
	}
	if got, want := plans[0].Source, filepath.Join(rcRoot, "zsh"); got != want {
		t.Errorf("zsh root source = %q, want %q", got, want)
	}
	if got := plans[0].Target; got != zshRoot {
		t.Errorf("zsh root target = %q, want %q", got, zshRoot)
	}
}

func TestZshRootLinkPrecedesNestedShellLinks(t *testing.T) {
	home := t.TempDir()
	rcRoot := filepath.Join(home, ".local", "share", "rc")
	zshSource := filepath.Join(rcRoot, "zsh")
	hostInit := filepath.Join(home, ".config", "yadm", "hosts", "common", "shell", "init.zsh")
	if err := os.MkdirAll(zshSource, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, hostInit, "host init")

	cfg := &config.Config{
		Hostname: "ibra",
		Roots: map[string]string{
			"rc":  rcRoot,
			"zsh": filepath.Join(home, ".config", "zsh"),
		},
		Links: []config.ResolvedLink{{
			Source: hostInit,
			Target: filepath.Join(home, ".config", "zsh", "hosts", "ibra.sh"),
		}},
	}

	l, _ := newTestLinker(t, home, false)
	plans := parentFirst(l.BuildPlan(cfg))
	if got, want := plans[0].Target, cfg.Roots["zsh"]; got != want {
		t.Fatalf("zsh root link must be planned before nested host links: first target=%q want %q", got, want)
	}
	if err := l.Apply(context.Background(), plans); err != nil {
		t.Fatal(err)
	}
	if dest, err := os.Readlink(cfg.Roots["zsh"]); err != nil {
		t.Fatalf("zsh root should be a symlink: %v", err)
	} else if dest != filepath.Join("..", ".local", "share", "rc", "zsh") {
		t.Fatalf("zsh root link = %q, want relative link to rc zsh", dest)
	}
	if _, err := os.Lstat(filepath.Join(cfg.Roots["zsh"], "hosts", "ibra.sh")); err != nil {
		t.Fatalf("nested host shell link should be created under zsh root: %v", err)
	}
}

func TestZshRootRealDirectoryIsRefused(t *testing.T) {
	home := t.TempDir()
	rcRoot := filepath.Join(home, ".local", "share", "rc")
	zshRoot := filepath.Join(home, ".config", "zsh")
	if err := os.MkdirAll(filepath.Join(rcRoot, "zsh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(zshRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	l, _ := newTestLinker(t, home, false)
	err := l.Apply(context.Background(), l.BuildPlan(&config.Config{
		Roots: map[string]string{
			"rc":  rcRoot,
			"zsh": zshRoot,
		},
	}))
	if err == nil || !strings.Contains(err.Error(), "exists and is not a symlink") {
		t.Fatalf("expected real zsh directory to be refused, got %v", err)
	}
	if info, err := os.Lstat(zshRoot); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("real zsh directory must remain untouched, info=%v err=%v", info, err)
	}
}

func TestZshRootDryRunDoesNotMutate(t *testing.T) {
	home := t.TempDir()
	rcRoot := filepath.Join(home, ".local", "share", "rc")
	zshRoot := filepath.Join(home, ".config", "zsh")
	if err := os.MkdirAll(filepath.Join(rcRoot, "zsh"), 0o755); err != nil {
		t.Fatal(err)
	}

	l, _ := newTestLinker(t, home, true)
	if err := l.Apply(context.Background(), l.BuildPlan(&config.Config{
		Roots: map[string]string{
			"rc":  rcRoot,
			"zsh": zshRoot,
		},
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(zshRoot); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not create zsh root link, err=%v", err)
	}
}

func TestBuildPlanDiscoversSourceAndTargetCompletionNames(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "src", "launchcmd.sh")
	writeFile(t, source, "x")
	writeFile(t, filepath.Join(home, "src", "_launchcmd.sh"), "source completion")
	writeFile(t, filepath.Join(home, "src", "_launchcmd"), "target completion")

	l, _ := newTestLinker(t, home, false)
	cfg := &config.Config{
		Hostname: "ibra",
		Roots:    map[string]string{"zsh": filepath.Join(home, ".config", "zsh")},
		Bins: []config.ResolvedBin{{
			Source:             source,
			Target:             filepath.Join(home, ".local", "bin", "launchcmd"),
			DiscoverCompletion: true,
		}},
	}
	plans := l.BuildPlan(cfg)
	byTarget := map[string]Plan{}
	for _, plan := range plans {
		byTarget[plan.Target] = plan
	}

	sourceCompletion := filepath.Join(home, ".config", "zsh", "functions", "hosts", "ibra", "_launchcmd.sh")
	if plan, ok := byTarget[sourceCompletion]; !ok || plan.Source != filepath.Join(home, "src", "_launchcmd.sh") {
		t.Fatalf("missing source-name completion plan: %+v", byTarget)
	}
	targetCompletion := filepath.Join(home, ".config", "zsh", "functions", "hosts", "ibra", "_launchcmd")
	if plan, ok := byTarget[targetCompletion]; !ok || plan.Source != filepath.Join(home, "src", "_launchcmd") {
		t.Fatalf("missing target-name completion plan: %+v", byTarget)
	}
}

func TestLinkSourceCanonicalizesParentsWithoutResolvingSourceLeaf(t *testing.T) {
	root := filepath.FromSlash("/var/folders/example")
	targetParent := filepath.Join(root, ".config")
	target := filepath.Join(targetParent, "file")
	sourceParent := filepath.Join(root, "src")
	source := filepath.Join(sourceParent, "link")

	l := &Linker{FS: evalSymlinkFS{resolved: map[string]string{
		targetParent: filepath.FromSlash("/private/var/folders/example/.config"),
		sourceParent: filepath.FromSlash("/private/var/folders/example/src"),
		source:       filepath.FromSlash("/private/var/folders/example/real/file"),
	}}}
	got := l.linkSource(target, source)
	if want := filepath.Join("..", "src", "link"); got != want {
		t.Fatalf("linkSource() = %q, want %q", got, want)
	}
}

func TestNestedTargetUnderManagedSymlinkedParent(t *testing.T) {
	home := t.TempDir()
	rcRoot := filepath.Join(home, ".local", "share", "rc")
	parentSource := filepath.Join(rcRoot, "aichat")
	childSource := filepath.Join(rcRoot, "ai", "prompts")
	if err := os.MkdirAll(parentSource, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(childSource, 0o755); err != nil {
		t.Fatal(err)
	}
	parentTarget := filepath.Join(home, ".config", "aichat")
	childTarget := filepath.Join(parentTarget, "roles")

	l, _ := newTestLinker(t, home, false)
	if err := l.Apply(context.Background(), []Plan{
		{Source: childSource, Target: childTarget, Kind: "link"},
		{Source: parentSource, Target: parentTarget, Kind: "link"},
	}); err != nil {
		t.Fatal(err)
	}

	parentDest, err := os.Readlink(parentTarget)
	if err != nil {
		t.Fatalf("parent target should be a symlink: %v", err)
	}
	if filepath.IsAbs(parentDest) {
		t.Fatalf("parent link should be relative, got %q", parentDest)
	}

	physicalChild := filepath.Join(parentSource, "roles")
	childDest, err := os.Readlink(physicalChild)
	if err != nil {
		t.Fatalf("child target should be created inside the physical parent: %v", err)
	}
	if childDest != "../ai/prompts" {
		t.Fatalf("child link target = %q, want ../ai/prompts", childDest)
	}
	resolved, err := filepath.EvalSymlinks(childTarget)
	if err != nil {
		t.Fatalf("child target should resolve through managed parent: %v", err)
	}
	expectedSource, err := filepath.EvalSymlinks(childSource)
	if err != nil {
		t.Fatalf("child source should resolve: %v", err)
	}
	if resolved != expectedSource {
		t.Fatalf("child target resolves to %q, want %q", resolved, expectedSource)
	}
}

type evalSymlinkFS struct {
	OSFS
	resolved map[string]string
}

func (fs evalSymlinkFS) EvalSymlinks(path string) (string, error) {
	if resolved, ok := fs.resolved[path]; ok {
		return resolved, nil
	}
	return path, nil
}

func TestDryRunNoChanges(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "src", "file")
	writeFile(t, src, "x")
	target := filepath.Join(home, ".config", "file")

	l, _ := newTestLinker(t, home, true)
	if err := l.Apply(context.Background(), []Plan{{Source: src, Target: target}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("dry-run must not create links, got err=%v", err)
	}
	if _, err := os.Stat(l.ManifestPath); !os.IsNotExist(err) {
		t.Errorf("dry-run must not write manifest")
	}
}

func TestConflictDetection(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, ".config", "x")
	l, _ := newTestLinker(t, home, false)
	err := l.Apply(context.Background(), []Plan{
		{Source: "/a", Target: target},
		{Source: "/b", Target: target},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("expected conflict error, got %v", err)
	}
}

func TestSpacesInPaths(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "my src", "the file")
	writeFile(t, src, "x")
	target := filepath.Join(home, ".config", "with space")

	l, _ := newTestLinker(t, home, false)
	if err := l.Apply(context.Background(), []Plan{{Source: src, Target: target}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); err != nil {
		t.Errorf("link with spaces should be created: %v", err)
	}
}

func TestPrivilegedPlanningUsesSudoNotRealFS(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "src", "file")
	writeFile(t, src, "x")
	// Target outside home => privileged. Use a path under a temp dir we mark as
	// outside home by setting Home to a subdir.
	outside := filepath.Join(t.TempDir(), "etc", "thing")

	l, fake := newTestLinker(t, home, false)
	if err := l.Apply(context.Background(), []Plan{{Source: src, Target: outside, Privileged: true}}); err != nil {
		t.Fatal(err)
	}
	var sawSudoLn bool
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "sudo ln ") {
			sawSudoLn = true
		}
	}
	if !sawSudoLn {
		t.Errorf("expected a planned 'sudo ln', calls: %v", fake.CallLines())
	}
	// The fake runner never touches the real filesystem.
	if _, err := os.Lstat(outside); !os.IsNotExist(err) {
		t.Errorf("privileged link must not be created on real FS in test")
	}
}

func TestOutsideHomeRequiresExplicitPrivilege(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "src", "file")
	writeFile(t, src, "x")
	outside := filepath.Join(t.TempDir(), "etc", "thing")

	l, fake := newTestLinker(t, home, false)
	err := l.Apply(context.Background(), []Plan{{Source: src, Target: outside}})
	if err == nil || !strings.Contains(err.Error(), "privileged=true") {
		t.Fatalf("expected outside-home privilege error, got %v", err)
	}
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "sudo ") {
			t.Fatalf("non-privileged outside-home target must not invoke sudo, calls: %v", fake.CallLines())
		}
	}
}

func TestStaleSweepLeavesRepointedSymlink(t *testing.T) {
	home := t.TempDir()
	original := filepath.Join(home, "src", "original")
	repointed := filepath.Join(home, "src", "repointed")
	writeFile(t, original, "old")
	writeFile(t, repointed, "new")
	target := filepath.Join(home, ".config", "managed")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repointed, target); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(home, ".config", "rc", "manifest.json")
	if err := (&Manifest{Links: map[string]string{target: original}}).Save(manifestPath); err != nil {
		t.Fatal(err)
	}

	l, _ := newTestLinker(t, home, false)
	if err := l.Apply(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	dest, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("repointed symlink should survive stale sweep: %v", err)
	}
	if dest != repointed {
		t.Fatalf("stale sweep changed link target to %q, want %q", dest, repointed)
	}
}
