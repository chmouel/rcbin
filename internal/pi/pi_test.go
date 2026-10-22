package pi

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

type fixture struct {
	root   string
	agent  string
	source string
	fake   *runner.Fake
	rep    *output.Reporter
	logs   *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{
		root:   root,
		agent:  filepath.Join(root, "agent"),
		source: filepath.Join(root, "desktop-config", "pi", "agent"),
		fake:   runner.NewFake(),
		logs:   &bytes.Buffer{},
	}
	f.fake.AddStub("pi --version", runner.Result{Stdout: "1.3.0"}, nil)
	f.fake.AddStub("pi update", runner.Result{}, nil)
	f.rep = output.New(f.logs, f.logs, false, true)
	return f
}

func (f *fixture) syncer() *Syncer {
	return &Syncer{R: f.fake, Rep: f.rep, AgentDir: f.agent, Source: f.source, Host: "ibra", AllowCreate: true}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func requireSymlinkTo(t *testing.T, link, want string) {
	t.Helper()
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("%s is not a symlink: %v", link, err)
	}
	if got != want {
		t.Fatalf("%s points to %q, want %q", link, got, want)
	}
}

func requireNoSymlink(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s should not be a symlink", path)
	}
}

func canon(t *testing.T, path string) string {
	t.Helper()
	resolved, err := canonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}

func requireLog(t *testing.T, logs *bytes.Buffer, want string) {
	t.Helper()
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("logs do not contain %q:\n%s", want, logs.String())
	}
}

func TestSyncLinksFilesAndDirectories(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.source, "settings.json"), "{}")
	writeFile(t, filepath.Join(f.source, "AGENTS.md"), "agents")
	writeFile(t, filepath.Join(f.source, "extensions", "ext.js"), "ext")

	if err := f.syncer().Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireSymlinkTo(t, filepath.Join(f.agent, "settings.json"), filepath.Join(f.source, "settings.json"))
	requireSymlinkTo(t, filepath.Join(f.agent, "AGENTS.md"), filepath.Join(f.source, "AGENTS.md"))
	requireSymlinkTo(t, filepath.Join(f.agent, "extensions"), filepath.Join(f.source, "extensions"))

	// Second run is a no-op and does not fail on existing links.
	logs := f.logs.Len()
	if err := f.syncer().Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.logs.String()[logs:]; strings.Contains(got, "linked") {
		t.Fatalf("second run should not relink:\n%s", got)
	}
}

func TestSyncHostOverrideWins(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.source, "settings.json"), "shared")
	writeFile(t, filepath.Join(f.source, HostDir, "ibra", "settings.json"), "host")
	writeFile(t, filepath.Join(f.source, "models.json"), "shared-models")

	if err := f.syncer().Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireSymlinkTo(t, filepath.Join(f.agent, "settings.json"), filepath.Join(f.source, HostDir, "ibra", "settings.json"))
	requireSymlinkTo(t, filepath.Join(f.agent, "models.json"), filepath.Join(f.source, "models.json"))
	if got := readFile(t, filepath.Join(f.agent, "settings.json")); got != "host" {
		t.Fatalf("settings.json = %q, want the host override", got)
	}
}

func TestSyncRejectsBadHost(t *testing.T) {
	for _, host := range []string{"", "..", "a/b", `a\b`, "host name"} {
		t.Run(host, func(t *testing.T) {
			f := newFixture(t)
			s := f.syncer()
			s.Host = host
			err := s.Sync(context.Background())
			if err == nil {
				t.Fatalf("host %q should be rejected", host)
			}
			if !strings.Contains(err.Error(), "host name") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSyncRejectsOverlappingTrees(t *testing.T) {
	f := newFixture(t)
	mkdir(t, filepath.Join(f.root, "real"))
	alias := filepath.Join(f.root, "alias")
	if err := os.Symlink(filepath.Join(f.root, "real"), alias); err != nil {
		t.Fatal(err)
	}

	cases := map[string]*Syncer{
		"same path via symlink alias": {AgentDir: filepath.Join(f.root, "real", "agent"), Source: filepath.Join(alias, "agent")},
		"source inside agent":         {AgentDir: filepath.Join(f.root, "real"), Source: filepath.Join(alias, "agent")},
		"agent inside source":         {AgentDir: filepath.Join(f.root, "real", "agent"), Source: filepath.Join(f.root, "real")},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			s.R, s.Rep, s.Host, s.AllowCreate = f.fake, f.rep, "ibra", true
			if err := s.Sync(context.Background()); err == nil {
				t.Fatal("expected an overlap error")
			}
		})
	}
}

func TestSyncRejectsInvalidSourceEntries(t *testing.T) {
	t.Run("fifo", func(t *testing.T) {
		f := newFixture(t)
		mkdir(t, f.source)
		if err := syscall.Mkfifo(filepath.Join(f.source, "settings.json"), 0o644); err != nil {
			t.Skipf("mkfifo unavailable: %v", err)
		}
		err := f.syncer().Sync(context.Background())
		if err == nil || !strings.Contains(err.Error(), "not a regular file or directory") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("broken symlink", func(t *testing.T) {
		f := newFixture(t)
		mkdir(t, f.source)
		if err := os.Symlink(filepath.Join(f.root, "missing"), filepath.Join(f.source, "settings.json")); err != nil {
			t.Fatal(err)
		}
		err := f.syncer().Sync(context.Background())
		if err == nil || !strings.Contains(err.Error(), "broken or looping symlink") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("wrong kind", func(t *testing.T) {
		f := newFixture(t)
		mkdir(t, filepath.Join(f.source, "settings.json"))
		err := f.syncer().Sync(context.Background())
		if err == nil || !strings.Contains(err.Error(), "must be a regular file") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestSyncSeedsLocalEntries(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.agent, "settings.json"), `{"local":true}`)
	writeFile(t, filepath.Join(f.agent, "extensions", "ext.js"), "ext")

	if err := f.syncer().Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireSymlinkTo(t, filepath.Join(f.agent, "settings.json"), filepath.Join(f.source, "settings.json"))
	requireSymlinkTo(t, filepath.Join(f.agent, "extensions"), filepath.Join(f.source, "extensions"))
	if got := readFile(t, filepath.Join(f.source, "settings.json")); got != `{"local":true}` {
		t.Fatalf("seeded content = %q", got)
	}
	if got := readFile(t, filepath.Join(f.source, "extensions", "ext.js")); got != "ext" {
		t.Fatalf("seeded directory content = %q", got)
	}
	requireLog(t, f.logs, "seeded")
}

func TestSyncCreatesMissingLayout(t *testing.T) {
	f := newFixture(t)
	s := f.syncer()
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{f.agent, f.source} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("%s should have been created: %v", dir, err)
		}
	}
	// Nothing to link yet, so no reconciliation call was needed beyond the
	// version probe plus the update.
	if got, want := len(f.fake.CallLines()), 2; got != want {
		t.Fatalf("calls = %v, want %d", f.fake.CallLines(), want)
	}
}

func TestSyncSkipsWhenSourceMissingAndCreationNotAllowed(t *testing.T) {
	f := newFixture(t)
	s := f.syncer()
	s.AllowCreate = false
	writeFile(t, filepath.Join(f.agent, "settings.json"), "local")

	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source should not be created: %v", err)
	}
	requireNoSymlink(t, filepath.Join(f.agent, "settings.json"))
}

func TestSyncConflictRequiresAdopt(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.source, "settings.json"), "canonical")
	writeFile(t, filepath.Join(f.agent, "settings.json"), "local")

	err := f.syncer().Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--adopt-pi") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readFile(t, filepath.Join(f.agent, "settings.json")); got != "local" {
		t.Fatalf("conflict must not change the target, got %q", got)
	}
	if got := readFile(t, filepath.Join(f.source, "settings.json")); got != "canonical" {
		t.Fatalf("conflict must not change the source, got %q", got)
	}
}

func TestSyncAdoptBacksUpFileAndDirectory(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.source, "settings.json"), "canonical")
	writeFile(t, filepath.Join(f.source, "extensions", "canonical.js"), "canonical")
	writeFile(t, filepath.Join(f.agent, "settings.json"), "local")
	writeFile(t, filepath.Join(f.agent, "extensions", "local.js"), "local")
	// Occupy the first backup name to check the .1 fallback.
	writeFile(t, filepath.Join(f.agent, "settings.json.rc-adopt.bak"), "older backup")

	s := f.syncer()
	s.Adopt = true
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireSymlinkTo(t, filepath.Join(f.agent, "settings.json"), filepath.Join(f.source, "settings.json"))
	requireSymlinkTo(t, filepath.Join(f.agent, "extensions"), filepath.Join(f.source, "extensions"))
	if got := readFile(t, filepath.Join(f.agent, "settings.json.rc-adopt.bak.1")); got != "local" {
		t.Fatalf("backup = %q", got)
	}
	if got := readFile(t, filepath.Join(f.agent, "settings.json.rc-adopt.bak")); got != "older backup" {
		t.Fatalf("existing backup was overwritten: %q", got)
	}
	if got := readFile(t, filepath.Join(f.agent, "extensions.rc-adopt.bak", "local.js")); got != "local" {
		t.Fatalf("directory backup = %q", got)
	}
	requireLog(t, f.logs, filepath.Join(f.agent, "extensions.rc-adopt.bak"))
}

func TestSyncAdoptReplacesForeignSymlink(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.source, "settings.json"), "canonical")
	external := filepath.Join(f.root, "external.json")
	writeFile(t, external, "external")
	mkdir(t, f.agent)
	if err := os.Symlink(external, filepath.Join(f.agent, "settings.json")); err != nil {
		t.Fatal(err)
	}

	if err := f.syncer().Sync(context.Background()); err == nil {
		t.Fatal("expected a conflict for a symlink pointing elsewhere")
	}

	s := f.syncer()
	s.Adopt = true
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireSymlinkTo(t, filepath.Join(f.agent, "settings.json"), filepath.Join(f.source, "settings.json"))
	backup := filepath.Join(f.agent, "settings.json.rc-adopt.bak")
	requireSymlinkTo(t, backup, external)
	if got := readFile(t, external); got != "external" {
		t.Fatalf("external referent changed: %q", got)
	}
}

func TestSyncSeedRollbackRestoresTarget(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.agent, "settings.json"), "local")

	s := f.syncer()
	s.symlinkPath = func(string, string) error { return errors.New("boom") }
	err := s.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "restored the original") {
		t.Fatalf("unexpected error: %v", err)
	}
	requireNoSymlink(t, filepath.Join(f.agent, "settings.json"))
	if got := readFile(t, filepath.Join(f.agent, "settings.json")); got != "local" {
		t.Fatalf("target content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(f.source, "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("seeded copy should have been moved back: %v", err)
	}
}

func TestSyncSeedRollbackKeepsReappearedTarget(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.agent, "settings.json"), "local")

	s := f.syncer()
	s.symlinkPath = func(_, newPath string) error {
		if err := os.WriteFile(newPath, []byte("intruder"), 0o644); err != nil {
			t.Fatalf("creating the reappeared target: %v", err)
		}
		return errors.New("boom")
	}
	err := s.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no longer absent") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readFile(t, filepath.Join(f.agent, "settings.json")); got != "intruder" {
		t.Fatalf("reappeared target was clobbered: %q", got)
	}
	if got := readFile(t, filepath.Join(f.source, "settings.json")); got != "local" {
		t.Fatalf("preserved source copy = %q", got)
	}
}

func TestSyncAdoptRollbackRestoresTarget(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.source, "settings.json"), "canonical")
	writeFile(t, filepath.Join(f.agent, "settings.json"), "local")

	s := f.syncer()
	s.Adopt = true
	s.symlinkPath = func(string, string) error { return errors.New("boom") }
	err := s.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "restored the original") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readFile(t, filepath.Join(f.agent, "settings.json")); got != "local" {
		t.Fatalf("target content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(f.agent, "settings.json.rc-adopt.bak")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup should have been moved back: %v", err)
	}
}

func TestLinkIntoDoesNotRestoreOnUnexpectedStatError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	f := newFixture(t)
	parent := filepath.Join(f.root, "locked")
	writeFile(t, filepath.Join(parent, "settings.json"), "local")

	s := f.syncer()
	s.symlinkPath = func(string, string) error { return errors.New("boom") }

	moved := filepath.Join(f.root, "moved.json")
	if err := os.Rename(filepath.Join(parent, "settings.json"), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	err := s.linkInto(filepath.Join(f.root, "canonical.json"), filepath.Join(parent, "settings.json"), moved)
	if err == nil || !strings.Contains(err.Error(), "no longer absent") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(moved); statErr != nil {
		t.Fatalf("moved file must be left in place: %v", statErr)
	}
}

func TestMoveNoReplaceRefusesExistingDestination(t *testing.T) {
	s := newFixture(t).syncer()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	writeFile(t, src, "src")
	writeFile(t, dst, "dst")

	if err := s.moveNoReplace(src, dst); err == nil {
		t.Fatal("expected a destination-exists error")
	}
	if got := readFile(t, dst); got != "dst" {
		t.Fatalf("foreign destination was overwritten: %q", got)
	}
	if got := readFile(t, src); got != "src" {
		t.Fatalf("source was moved: %q", got)
	}
}

func TestMoveNoReplaceRefusesExistingDirectory(t *testing.T) {
	s := newFixture(t).syncer()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	writeFile(t, filepath.Join(src, "file"), "mine")
	writeFile(t, filepath.Join(dst, "file"), "theirs")

	if err := s.moveNoReplace(src, dst); err == nil {
		t.Fatal("expected a destination-exists error")
	}
	if got := readFile(t, filepath.Join(dst, "file")); got != "theirs" {
		t.Fatalf("foreign destination was replaced: %q", got)
	}
	if got := readFile(t, filepath.Join(src, "file")); got != "mine" {
		t.Fatalf("source was moved: %q", got)
	}
}

func TestMoveNoReplaceMovesDirectories(t *testing.T) {
	s := newFixture(t).syncer()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mkdir(t, src)
	writeFile(t, filepath.Join(src, "file"), "content")
	dst := filepath.Join(root, "dst")

	if err := s.moveNoReplace(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists: %v", err)
	}
	if got := readFile(t, filepath.Join(dst, "file")); got != "content" {
		t.Fatalf("moved content = %q", got)
	}
}

func TestAcquireLockRefusesActiveLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := acquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = acquireLock(path)
	if err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), first.token) {
		t.Fatalf("error should name the owning token: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active lock must not be removed: %v", err)
	}

	first.release()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock should be removed: %v", err)
	}
}

func TestLockReleaseKeepsReplacedLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := acquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	second, err := acquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	first.release()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("replaced lock must survive a stale release: %v", err)
	}
	second.release()
}

func TestLockPathsStayOutsideTheManagedTrees(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.source, "settings.json"), "{}")
	if err := f.syncer().Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, p := range lockPaths(canon(t, f.source), canon(t, f.agent)) {
		if !strings.HasPrefix(p, os.TempDir()) {
			t.Fatalf("lock %s should live under %s", p, os.TempDir())
		}
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("lock %s should have been released", p)
		}
	}
	if _, err := os.Stat(filepath.Join(f.agent, ".rc-pi.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("agent directory must not contain a lock file")
	}
}

func TestSyncSerializesOnSharedSourceLock(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.source, "settings.json"), "{}")

	otherAgent := filepath.Join(f.root, "other-agent")
	held, err := acquireLocks(lockPaths(canon(t, f.source), canon(t, otherAgent)))
	if err != nil {
		t.Fatal(err)
	}
	defer held.release()

	err = f.syncer().Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(f.agent, "settings.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("no link should be created while the source lock is held")
	}
}

func TestPreflightReflectsCurrentTargetState(t *testing.T) {
	f := newFixture(t)
	mkdir(t, f.source)
	entries := curatedEntries()
	s := f.syncer()

	plan, fatal, conflicts := s.preflight(f.source, f.agent, entries)
	if len(fatal) != 0 || len(conflicts) != 0 {
		t.Fatalf("unexpected fatal=%v conflicts=%v", fatal, conflicts)
	}
	if len(plan) != 0 {
		t.Fatalf("nothing should be planned yet: %v", plan)
	}

	writeFile(t, filepath.Join(f.agent, "settings.json"), "local")
	plan, fatal, conflicts = s.preflight(f.source, f.agent, entries)
	if len(fatal) != 0 || len(conflicts) != 0 {
		t.Fatalf("unexpected fatal=%v conflicts=%v", fatal, conflicts)
	}
	seeded := false
	for _, a := range plan {
		if a.kind == actSeed && a.name == "settings.json" {
			seeded = true
		}
	}
	if !seeded {
		t.Fatalf("plan should seed the new local file: %v", plan)
	}

	writeFile(t, filepath.Join(f.source, "settings.json"), "canonical")
	_, _, conflicts = s.preflight(f.source, f.agent, entries)
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %v, want the newly introduced source entry", conflicts)
	}
}

func TestSyncDryRunChangesNothing(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.agent, "settings.json"), "local")

	s := f.syncer()
	s.DryRun = true
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created the source: %v", err)
	}
	if got := readFile(t, filepath.Join(f.agent, "settings.json")); got != "local" {
		t.Fatalf("dry run changed the target: %q", got)
	}
	requireNoSymlink(t, filepath.Join(f.agent, "settings.json"))
	if calls := f.fake.CallLines(); len(calls) != 0 {
		t.Fatalf("dry run ran commands: %v", calls)
	}
	for _, p := range lockPaths(canon(t, f.source), canon(t, f.agent)) {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("dry run created lock %s", p)
		}
	}
	requireLog(t, f.logs, "would")
}

func TestSyncSkipsWhenPiIsMissing(t *testing.T) {
	f := newFixture(t)
	f.fake.Missing["pi"] = true
	writeFile(t, filepath.Join(f.agent, "settings.json"), "local")

	if err := f.syncer().Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.source); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("nothing should be created when pi is missing")
	}
	requireNoSymlink(t, filepath.Join(f.agent, "settings.json"))
}

func TestReconcileVersionGate(t *testing.T) {
	t.Run("too old", func(t *testing.T) {
		f := newFixture(t)
		f.fake.Stubs = nil
		f.fake.AddStub("pi --version", runner.Result{Stdout: "1.2.0"}, nil)
		s := f.syncer()
		if err := s.reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got, want := len(f.fake.CallLines()), 1; got != want {
			t.Fatalf("calls = %v, want only the version probe", f.fake.CallLines())
		}
		requireLog(t, f.logs, "older than")
	})

	t.Run("supported", func(t *testing.T) {
		f := newFixture(t)
		s := f.syncer()
		if err := s.reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		calls := f.fake.CallRecords()
		if len(calls) != 2 {
			t.Fatalf("calls = %v", f.fake.CallLines())
		}
		update := calls[1]
		if update.Name != "pi" || strings.Join(update.Args, " ") != "update --extensions --no-approve" {
			t.Fatalf("unexpected update call: %+v", update)
		}
		if update.Dir != f.agent {
			t.Errorf("Dir = %q, want the agent directory %q", update.Dir, f.agent)
		}
		want := []string{"PI_CODING_AGENT_DIR=" + f.agent}
		if len(update.Extra) != 1 || update.Extra[0] != want[0] {
			t.Errorf("Extra = %v, want %v", update.Extra, want)
		}
		if update.Stdin != "" || update.Interactive {
			t.Errorf("update must be captured and non-interactive: %+v", update)
		}
	})

	t.Run("unparsable", func(t *testing.T) {
		f := newFixture(t)
		f.fake.Stubs = nil
		f.fake.AddStub("pi --version", runner.Result{Stdout: "not-a-version"}, nil)
		if err := f.syncer().reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := len(f.fake.CallLines()); got != 1 {
			t.Fatalf("calls = %v", f.fake.CallLines())
		}
		requireLog(t, f.logs, "cannot parse")
	})
}

func TestReconcileFailureIsAWarning(t *testing.T) {
	f := newFixture(t)
	f.fake.Stubs = nil
	f.fake.AddStub("pi --version", runner.Result{Stdout: "1.3.0"}, nil)
	f.fake.AddStub("pi update", runner.Result{ExitCode: 1, Stderr: "boom"}, errors.New("exit 1"))

	s := f.syncer()
	if err := s.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile failure must not be fatal: %v", err)
	}
	requireLog(t, f.logs, "package update failed")
}

func TestReconcileCancellation(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.syncer().reconcile(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestSyncLinkingFailureSkipsReconciliation(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.agent, "settings.json"), "local")

	s := f.syncer()
	s.symlinkPath = func(string, string) error { return errors.New("boom") }
	if err := s.Sync(context.Background()); err == nil {
		t.Fatal("expected a linking failure")
	}
	if calls := f.fake.CallLines(); len(calls) != 0 {
		t.Fatalf("reconciliation must be skipped after a linking failure: %v", calls)
	}
}

func TestCanonicalPathReportsPermissionErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	mkdir(t, filepath.Join(locked, "child"))
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	_, err := canonicalPath(filepath.Join(locked, "child"))
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want a permission error", err)
	}
}

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"1.3.0", true},
		{"1.3.1", true},
		{"1.10.0", true},
		{"2.0.0", true},
		{"1.2.9", false},
		{"0.9.0", false},
	}
	for _, c := range cases {
		got, err := versionAtLeast(c.in, 1, 3, 0)
		if err != nil {
			t.Fatalf("versionAtLeast(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("versionAtLeast(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := versionAtLeast("nope", 1, 3, 0); err == nil {
		t.Error("expected an error for an unparsable version")
	}
}

func TestSyncRefusesWrongKindLocalEntry(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.source, "settings.json"), "{}")
	mkdir(t, filepath.Join(f.agent, "settings.json"))

	err := f.syncer().Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "must be a regular file") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(f.agent, "settings.json")); statErr != nil {
		t.Fatalf("the local entry must be left alone: %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(f.source, "settings.json")); statErr != nil {
		t.Fatalf("the canonical entry must be untouched: %v", statErr)
	}
}

func TestSyncRefusesWrongKindLocalDirectoryEntry(t *testing.T) {
	f := newFixture(t)
	mkdir(t, filepath.Join(f.source, "extensions"))
	writeFile(t, filepath.Join(f.agent, "extensions"), "not a directory")

	err := f.syncer().Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "must be a directory") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readFile(t, filepath.Join(f.agent, "extensions")); got != "not a directory" {
		t.Fatalf("the local entry must be left alone, got %q", got)
	}
}

func TestSyncSkipsBeforeConflictWhenSourceIsMissingAndCreationIsDisallowed(t *testing.T) {
	f := newFixture(t)
	localAuth := filepath.Join(f.agent, AuthFile)
	writeFile(t, localAuth, "local-token")
	writeFile(t, filepath.Join(f.agent, "settings.json"), "{}")

	s := f.syncer()
	s.AllowCreate = false
	s.SyncAuth = true
	if err := s.Sync(context.Background()); err != nil {
		t.Fatalf("a missing source must no-op, got %v", err)
	}
	if _, err := os.Stat(f.source); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the source must not be created")
	}
	if got := readFile(t, localAuth); got != "local-token" {
		t.Fatalf("local credentials must be untouched, got %q", got)
	}
}

func TestSyncRechecksTargetsUnderTheLock(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.source, "settings.json"), "canonical")

	s := f.syncer()
	var mutated bool
	s.afterPreflight = func() {
		if mutated {
			return
		}
		mutated = true
		writeFile(t, filepath.Join(f.agent, "settings.json"), "appeared")
	}
	err := s.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("the target created under the lock must be reported, got %v", err)
	}
	if got := readFile(t, filepath.Join(f.agent, "settings.json")); got != "appeared" {
		t.Fatalf("the concurrent entry must be preserved, got %q", got)
	}
}

func TestSyncRefusesCaseAliasedSourceAndAgent(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "CaseTree"), 0o755); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(root, "casetree")); err != nil || info == nil {
		t.Skip("the filesystem is case-sensitive, so a case alias cannot be built")
	}
	source := filepath.Join(root, "CaseTree")
	agent := filepath.Join(root, "casetree")
	writeFile(t, filepath.Join(source, "settings.json"), "canonical")

	var logs bytes.Buffer
	s := &Syncer{
		R:           runner.NewFake(),
		Rep:         output.New(&logs, &logs, false, true),
		AgentDir:    agent,
		Source:      source,
		Host:        "box",
		Adopt:       true,
		AllowCreate: true,
	}
	err := s.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "same directory") {
		t.Fatalf("error = %v, want a refusal to treat the aliases as two trees", err)
	}
	// Nothing may be moved or replaced when the trees alias each other.
	if got := readFile(t, filepath.Join(source, "settings.json")); got != "canonical" {
		t.Fatalf("the canonical file was disturbed: %q", got)
	}
	if k, err := lstatKind(filepath.Join(agent, "settings.json")); err != nil || k != kindFile {
		t.Fatalf("entry = %v (err=%v), want the untouched regular file", k, err)
	}
}

func TestSyncRefusesCanonicalSymlinkIntoTheAgentDirectory(t *testing.T) {
	f := newFixture(t)
	localSettings := filepath.Join(f.agent, "settings.json")
	writeFile(t, localSettings, "local")
	// The canonical entry is a symlink back to the local file, so adopting
	// the local file would repoint the link at itself.
	if err := os.MkdirAll(f.source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(localSettings, filepath.Join(f.source, "settings.json")); err != nil {
		t.Fatal(err)
	}

	s := f.syncer()
	s.Adopt = true
	err := s.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "symlink into the agent directory") {
		t.Fatalf("error = %v, want a refusal to link a self-referential source", err)
	}
	if got := readFile(t, localSettings); got != "local" {
		t.Fatalf("the local entry was moved: %q", got)
	}
	if k, err := lstatKind(localSettings); err != nil || k != kindFile {
		t.Fatalf("local entry = %v (err=%v), want the untouched regular file", k, err)
	}
}
