package linker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	l := New(fake, rep, home, filepath.Join(home, ".config", "rc", "manifest.json"), dryRun)
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

func TestNonVerboseRealApplyStaysQuiet(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "src", "file")
	writeFile(t, src, "x")
	target := filepath.Join(home, ".config", "file")

	l, _, errBuf := newBufferedTestLinker(t, home, false, false)
	if err := l.Apply(context.Background(), []Plan{{Source: src, Target: target, Kind: "link"}}); err != nil {
		t.Fatal(err)
	}
	if got := errBuf.String(); got != "" {
		t.Fatalf("non-verbose real apply should stay quiet, got:\n%s", got)
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
