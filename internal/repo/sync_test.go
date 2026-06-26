package repo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

type abortHandler struct{ calls int }

func (h *abortHandler) Handle(context.Context, config.RepoTarget, string) (bool, error) {
	h.calls++
	return false, ErrAbort
}

func TestDirtyAbortStopsSync(t *testing.T) {
	work1 := makeRepoPair(t)
	work2 := makeRepoPair(t)
	for _, w := range []string{work1, work2} {
		if err := os.WriteFile(filepath.Join(w, "f"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := &abortHandler{}
	s := newTestSyncer(t)
	s.Dirty = h
	err := s.Sync(context.Background(), []config.RepoTarget{{Path: work1}, {Path: work2}})
	if !errors.Is(err, ErrAbort) {
		t.Fatalf("expected ErrAbort, got %v", err)
	}
	if h.calls != 1 {
		t.Errorf("abort should stop after the first repo, handler called %d times", h.calls)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// makeRepoPair creates a bare "remote" and a working clone with one commit.
func makeRepoPair(t *testing.T) (work string) {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	mustGit(t, root, "init", "--bare", "-b", "main", remote)

	seed := filepath.Join(root, "seed")
	mustGit(t, root, "init", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "f"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, seed, "add", "f")
	mustGit(t, seed, "commit", "-m", "init")
	mustGit(t, seed, "remote", "add", "origin", remote)
	mustGit(t, seed, "push", "-u", "origin", "main")

	work = filepath.Join(root, "work")
	mustGit(t, root, "clone", remote, work)
	mustGit(t, work, "config", "user.name", "t")
	mustGit(t, work, "config", "user.email", "t@e")
	return work
}

func newTestSyncer(t *testing.T) *Syncer {
	t.Helper()
	rep := output.New(os.Stdout, os.Stderr, false, false)
	return &Syncer{R: runner.New(), Rep: rep, Limit: 4}
}

func TestCleanRepoUpToDate(t *testing.T) {
	work := makeRepoPair(t)
	ctx := context.Background()
	r := runner.New()

	if HasChanges(ctx, r, work) {
		t.Fatal("fresh clone should be clean")
	}
	res := newTestSyncer(t).syncClean(ctx, config.RepoTarget{Path: work}, "work")
	if res.err != nil {
		t.Fatalf("clean up-to-date sync failed: %v", res.err)
	}
	if res.headChanged {
		t.Error("HEAD should not change for up-to-date repo")
	}
}

func TestCleanRepoAheadPushes(t *testing.T) {
	work := makeRepoPair(t)
	ctx := context.Background()
	r := runner.New()

	if err := os.WriteFile(filepath.Join(work, "f"), []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, work, "commit", "-am", "local change")

	ahead, ok := Upstream(ctx, r, work)
	if !ok || ahead != 1 {
		t.Fatalf("expected ahead=1 with upstream, got %d ok=%v", ahead, ok)
	}

	res := newTestSyncer(t).syncClean(ctx, config.RepoTarget{Path: work}, "work")
	if res.err != nil {
		t.Fatalf("push sync failed: %v", res.err)
	}
	ahead, _ = Upstream(ctx, r, work)
	if ahead != 0 {
		t.Errorf("expected 0 ahead after push, got %d", ahead)
	}
}

func TestDirtyDetection(t *testing.T) {
	work := makeRepoPair(t)
	ctx := context.Background()
	r := runner.New()
	if err := os.WriteFile(filepath.Join(work, "f"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !HasChanges(ctx, r, work) {
		t.Fatal("expected dirty work tree")
	}
	changed := Scan(ctx, r, []config.RepoTarget{{Path: work}})
	if len(changed) != 1 {
		t.Fatalf("expected 1 changed repo, got %v", changed)
	}
}

func TestNonInteractiveDirtyFails(t *testing.T) {
	work := makeRepoPair(t)
	if err := os.WriteFile(filepath.Join(work, "f"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestSyncer(t) // no Dirty handler => non-interactive failure
	err := s.Sync(context.Background(), []config.RepoTarget{{Path: work}})
	if err == nil {
		t.Fatal("expected non-interactive dirty repo to fail")
	}
}

func TestSyncShowsProgressWhenColorEnabled(t *testing.T) {
	work1 := t.TempDir()
	work2 := t.TempDir()
	fake := runner.NewFake()
	var errBuf bytes.Buffer
	rep := output.New(io.Discard, &errBuf, true, false)
	s := &Syncer{R: fake, Rep: rep, Limit: 1}

	if err := s.Sync(context.Background(), []config.RepoTarget{{Path: work1}, {Path: work2}}); err != nil {
		t.Fatal(err)
	}
	got := errBuf.String()
	for _, want := range []string{"Inspecting repositories", "Synchronizing repositories", "2/2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sync progress output missing %q in %q", want, got)
		}
	}
}

func TestCanonicalDedup(t *testing.T) {
	work := makeRepoPair(t)
	link := work + "-link"
	if err := os.Symlink(work, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	ctx := context.Background()
	changed := Scan(ctx, runner.New(), []config.RepoTarget{{Path: work}, {Path: link}})
	// Both point at the same worktree; clean repo yields no changes and the
	// duplicate is removed regardless.
	if len(changed) != 0 {
		t.Fatalf("expected deduped clean scan, got %v", changed)
	}
}

func TestHooksRunOnHeadChange(t *testing.T) {
	work := makeRepoPair(t)
	ctx := context.Background()
	marker := filepath.Join(t.TempDir(), "hook-ran")

	s := newTestSyncer(t)
	r := result{
		repo: config.RepoTarget{
			Path: work,
			Hooks: config.Hooks{
				PostUpdate: &config.Command{Argv: []string{"touch", marker}},
			},
		},
		name:        "work",
		attempted:   true,
		headChanged: true,
	}

	s.runHooks(ctx, r)
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("post_update hook should have run: %v", err)
	}
}

func TestShellHookUsesConfiguredShell(t *testing.T) {
	ctx := context.Background()
	fake := runner.NewFake()
	rep := output.New(os.Stdout, os.Stderr, false, false)
	s := &Syncer{R: fake, Rep: rep, Shell: "zsh"}
	r := result{
		repo: config.RepoTarget{
			Path: "/repo",
			Hooks: config.Hooks{
				Always: &config.Command{Shell: "print $ZSH_VERSION"},
			},
		},
		name:      "repo",
		attempted: true,
	}
	s.runHooks(ctx, r)
	lines := fake.CallLines()
	if len(lines) != 1 || lines[0] != "zsh -c print $ZSH_VERSION" {
		t.Fatalf("hook should use configured shell, calls: %v", lines)
	}
}
