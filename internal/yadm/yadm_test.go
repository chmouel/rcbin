package yadm

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

type noopDirty struct{}

func (noopDirty) YadmDirty(context.Context, string) error { return nil }

func newSyncer(t *testing.T, fake *runner.Fake, nonInteractive bool) *Syncer {
	t.Helper()
	rep := output.New(os.Stdout, os.Stderr, false, false)
	return &Syncer{R: fake, Rep: rep, StateDir: t.TempDir(), NonInteractive: nonInteractive}
}

func TestCleanYadmPullsOnceNoPush(t *testing.T) {
	fake := runner.NewFake()
	// clean status, not ahead
	fake.AddStub("yadm status", runner.Result{}, nil)
	fake.AddStub("yadm rev-list", runner.Result{Stdout: "0\n"}, nil)
	fake.AddStub("yadm remote", runner.Result{Stdout: "git@host:repo\n"}, nil)

	s := newSyncer(t, fake, false)
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	var pulls, pushes int
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "yadm pull") {
			pulls++
		}
		if strings.HasPrefix(line, "yadm push") {
			pushes++
		}
	}
	if pulls != 1 {
		t.Errorf("expected exactly one pull, got %d", pulls)
	}
	if pushes != 0 {
		t.Errorf("expected no push when not ahead, got %d", pushes)
	}
}

func TestYadmPushesWhenAhead(t *testing.T) {
	fake := runner.NewFake()
	fake.AddStub("yadm status", runner.Result{}, nil)
	fake.AddStub("yadm rev-list", runner.Result{Stdout: "2\n"}, nil)
	fake.AddStub("yadm remote", runner.Result{Stdout: "git@host:repo\n"}, nil)

	s := newSyncer(t, fake, false)
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	var pushes int
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "yadm push") {
			pushes++
		}
	}
	if pushes != 1 {
		t.Errorf("expected one push when ahead, got %d", pushes)
	}
}

func TestYadmShowsProgressWhenColorEnabled(t *testing.T) {
	fake := runner.NewFake()
	fake.AddStub("yadm status", runner.Result{}, nil)
	fake.AddStub("yadm rev-list", runner.Result{Stdout: "0\n"}, nil)
	fake.AddStub("yadm remote", runner.Result{Stdout: "git@host:repo\n"}, nil)
	var errBuf bytes.Buffer
	rep := output.New(io.Discard, &errBuf, true, false)

	s := &Syncer{R: fake, Rep: rep, StateDir: t.TempDir()}
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := errBuf.String()
	for _, want := range []string{"Preparing YADM", "Synchronizing YADM", "3/3"} {
		if !strings.Contains(got, want) {
			t.Fatalf("yadm progress output missing %q in %q", want, got)
		}
	}
	remoteIdx := strings.LastIndex(got, "read remote")
	successIdx := strings.LastIndex(got, "Synced YADM")
	if remoteIdx < 0 || successIdx < 0 || remoteIdx >= successIdx {
		t.Fatalf("expected progress detail before success line in %q", got)
	}
	if !strings.Contains(got[remoteIdx:successIdx], "\r\033[K") {
		t.Fatalf("progress line should be cleared before success line in %q", got)
	}
}

func TestNonInteractiveDirtyYadmFails(t *testing.T) {
	fake := runner.NewFake()
	fake.AddStub("yadm status", runner.Result{Stdout: "1 .M ... file\n"}, nil)

	s := newSyncer(t, fake, true)
	err := s.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("expected non-interactive dirty failure, got %v", err)
	}
}

func TestDirtyYadmRechecksBeforePull(t *testing.T) {
	fake := runner.NewFake()
	fake.AddStub("yadm status", runner.Result{Stdout: "1 .M ... file\n"}, nil)

	s := newSyncer(t, fake, false)
	s.Dirty = noopDirty{}
	err := s.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "still has uncommitted changes") {
		t.Fatalf("expected still-dirty failure, got %v", err)
	}
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "yadm pull") {
			t.Fatalf("must not pull while yadm remains dirty, calls: %v", fake.CallLines())
		}
	}
}
