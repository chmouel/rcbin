package commitui

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/repo"
	"github.com/chmouel/rc/internal/runner"
)

type fakePrompter struct{ key byte }

func (f fakePrompter) Choice(string, byte) (byte, error) { return f.key, nil }

func newAdapter(t *testing.T, key byte) (*Adapter, *runner.Fake) {
	t.Helper()
	fake := runner.NewFake()
	fake.AddStub("git -C", runner.Result{Stdout: "abc123\n"}, nil)
	rep := output.New(os.Stdout, os.Stderr, false, false)
	return &Adapter{R: fake, Rep: rep, Pr: fakePrompter{key: key}}, fake
}

func TestQuitAborts(t *testing.T) {
	a, fake := newAdapter(t, 'q')
	changed, err := a.Handle(context.Background(), config.RepoTarget{Path: "/repo"}, "repo")
	if !errors.Is(err, repo.ErrAbort) {
		t.Fatalf("quit should return repo.ErrAbort, got %v", err)
	}
	if changed {
		t.Error("quit should report no change")
	}
	for _, line := range fake.CallLines() {
		if strings.Contains(line, "pull") || strings.Contains(line, "push") {
			t.Errorf("quit must not pull or push, saw %q", line)
		}
	}
}

func TestStdinPrompterLineFallback(t *testing.T) {
	p := &StdinPrompter{In: strings.NewReader("Q\n"), Out: io.Discard}
	got, err := p.Choice("? ", 'l')
	if err != nil {
		t.Fatal(err)
	}
	if got != 'q' {
		t.Errorf("got %q, want q", got)
	}
}

func TestStdinPrompterEmptyUsesDefault(t *testing.T) {
	p := &StdinPrompter{In: strings.NewReader("\n"), Out: io.Discard}
	got, err := p.Choice("? ", 'l')
	if err != nil {
		t.Fatal(err)
	}
	if got != 'l' {
		t.Errorf("got %q, want default l", got)
	}
}

func TestSkipDoesNotPush(t *testing.T) {
	a, fake := newAdapter(t, 's')
	changed, err := a.Handle(context.Background(), config.RepoTarget{Path: "/repo"}, "repo")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("skip should report no change")
	}
	for _, line := range fake.CallLines() {
		if strings.Contains(line, "push") {
			t.Errorf("skip must not push, saw %q", line)
		}
	}
}

func TestDirectCommitInvokesCommitAndPush(t *testing.T) {
	a, fake := newAdapter(t, 'c')
	if _, err := a.Handle(context.Background(), config.RepoTarget{Path: "/repo"}, "repo"); err != nil {
		t.Fatal(err)
	}
	var sawCommit, sawPush bool
	for _, line := range fake.CallLines() {
		if strings.Contains(line, "commit -s -a") {
			sawCommit = true
		}
		if strings.HasSuffix(line, "push") {
			sawPush = true
		}
	}
	if !sawCommit {
		t.Error("expected a signed commit")
	}
	if !sawPush {
		t.Error("expected a push after commit")
	}
}

func TestPullFailureStopsBeforePush(t *testing.T) {
	fake := runner.NewFake()
	dir := t.TempDir()
	fake.AddStub("git -C "+dir+" rev-parse HEAD", runner.Result{Stdout: "before\n"}, nil)
	fake.AddStub("git -C "+dir+" status --short", runner.Result{}, nil)
	fake.AddStub("git commit", runner.Result{}, nil)
	fake.AddStub("git -C "+dir+" pull --quiet", runner.Result{ExitCode: 1}, runner.FailWith(runner.Spec{Name: "git"}, 1, "conflict"))

	a := &Adapter{
		R:   fake,
		Rep: output.New(os.Stdout, os.Stderr, false, false),
		Pr:  fakePrompter{key: 'c'},
	}
	_, err := a.Handle(context.Background(), config.RepoTarget{Path: dir}, "repo")
	if err == nil || !strings.Contains(err.Error(), "pull failed") {
		t.Fatalf("expected pull failure, got %v", err)
	}
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "git -C "+dir+" push") {
			t.Fatalf("push must not run after pull failure, calls: %v", fake.CallLines())
		}
	}
}

// TestPullFailureToleratedWhenDirty verifies that a pull failure is treated as a
// warning (not a fatal error) when the working tree still has local changes,
// matching the legacy `git pull -q || true` behavior, and that the push is still
// attempted.
func TestPullFailureToleratedWhenDirty(t *testing.T) {
	fake := runner.NewFake()
	dir := t.TempDir()
	fake.AddStub("git -C "+dir+" rev-parse HEAD", runner.Result{Stdout: "before\n"}, nil)
	fake.AddStub("git -C "+dir+" status --short", runner.Result{}, nil)
	fake.AddStub("git commit", runner.Result{}, nil)
	fake.AddStub("git -C "+dir+" pull --quiet", runner.Result{ExitCode: 1}, runner.FailWith(runner.Spec{Name: "git"}, 1, "unstaged changes"))
	// HasChanges sees a dirty tree.
	fake.AddStub("git -C "+dir+" status --porcelain", runner.Result{Stdout: "1 M. early-init.el\n"}, nil)

	a := &Adapter{
		R:   fake,
		Rep: output.New(os.Stdout, os.Stderr, false, false),
		Pr:  fakePrompter{key: 'c'},
	}
	if _, err := a.Handle(context.Background(), config.RepoTarget{Path: dir}, "repo"); err != nil {
		t.Fatalf("dirty pull failure should not abort the run, got %v", err)
	}
	var sawPush bool
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "git -C "+dir+" push") {
			sawPush = true
		}
	}
	if !sawPush {
		t.Errorf("expected push to run after a tolerated pull failure, calls: %v", fake.CallLines())
	}
}
