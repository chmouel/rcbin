package maintenance

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

func rep() *output.Reporter { return output.New(io.Discard, io.Discard, false, false) }

func TestPlatformSelection(t *testing.T) {
	if platformMatches([]string{"linux"}, "darwin") {
		t.Error("linux task should not match darwin")
	}
	if !platformMatches(nil, "darwin") {
		t.Error("empty platforms should match all")
	}
}

func TestMissingOptionalCommandSkips(t *testing.T) {
	fake := runner.NewFake()
	fake.Missing["dconf"] = true
	u := &Update{R: fake, Rep: rep()}
	tasks := []config.ResolvedUpdate{{
		Name:     "x",
		Requires: []string{"dconf"},
		Commands: []config.Command{{Argv: []string{"dconf", "version"}}},
	}}
	if err := u.Run(context.Background(), tasks, nil); err != nil {
		t.Fatalf("missing requirement should skip, not fail: %v", err)
	}
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "dconf") {
			t.Error("must not run command when requirement missing")
		}
	}
}

func TestBackupUnchanged(t *testing.T) {
	repoRoot := t.TempDir()
	out := filepath.Join(repoRoot, "data")
	if err := os.WriteFile(out, []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := runner.NewFake()
	fake.AddStub("dump", runner.Result{Stdout: "same\n"}, nil)

	b := &Backup{R: fake, Rep: rep(), Now: func() time.Time { return time.Unix(0, 0) }}
	task := config.ResolvedBackup{Name: "t", Command: config.Command{Argv: []string{"dump"}}, RepoRoot: repoRoot, Output: out}
	if err := b.Run(context.Background(), []config.ResolvedBackup{task}, nil); err != nil {
		t.Fatal(err)
	}
	for _, line := range fake.CallLines() {
		if strings.Contains(line, "commit") {
			t.Error("unchanged backup must not commit")
		}
	}
}

func TestBackupChangedCommits(t *testing.T) {
	repoRoot := t.TempDir()
	out := filepath.Join(repoRoot, "data")
	fake := runner.NewFake()
	fake.AddStub("dump", runner.Result{Stdout: "new content\n"}, nil)

	b := &Backup{R: fake, Rep: rep(), Now: func() time.Time { return time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC) }}
	task := config.ResolvedBackup{
		Name:     "dconf",
		Command:  config.Command{Argv: []string{"dump"}},
		RepoRoot: repoRoot,
		Output:   out,
		Signoff:  true,
	}
	if err := b.Run(context.Background(), []config.ResolvedBackup{task}, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	if string(data) != "new content\n" {
		t.Errorf("backup file not written: %q", data)
	}
	var addLine, commitLine string
	for _, line := range fake.CallLines() {
		if strings.Contains(line, " add ") {
			addLine = line
		}
		if strings.Contains(line, "commit") {
			commitLine = line
		}
	}
	if addLine == "" {
		t.Fatal("expected changed backup to be staged before commit")
	}
	if commitLine == "" {
		t.Fatal("expected a commit for changed backup")
	}
	if !strings.Contains(commitLine, "-s") {
		t.Errorf("expected signoff in commit args: %q", commitLine)
	}
	if !strings.Contains(commitLine, "dconf update 2026-06-24") {
		t.Errorf("unexpected commit message: %q", commitLine)
	}
}

func TestBackupCommitFailureWithStagedChangeFails(t *testing.T) {
	repoRoot := t.TempDir()
	out := filepath.Join(repoRoot, "data")
	fake := runner.NewFake()
	fake.AddStub("dump", runner.Result{Stdout: "new content\n"}, nil)
	fake.AddStub("git -C "+repoRoot+" commit", runner.Result{ExitCode: 1}, runner.FailWith(runner.Spec{Name: "git"}, 1, "hook failed"))
	fake.AddStub("git -C "+repoRoot+" diff --cached --quiet", runner.Result{ExitCode: 1}, runner.FailWith(runner.Spec{Name: "git"}, 1, "changed"))

	b := &Backup{R: fake, Rep: rep(), Now: func() time.Time { return time.Unix(0, 0) }}
	task := config.ResolvedBackup{Name: "t", Command: config.Command{Argv: []string{"dump"}}, RepoRoot: repoRoot, Output: out}
	err := b.Run(context.Background(), []config.ResolvedBackup{task}, nil)
	if err == nil {
		t.Fatal("expected commit failure with staged changes to fail")
	}
}

func TestBackupFilter(t *testing.T) {
	fake := runner.NewFake()
	b := &Backup{R: fake, Rep: rep()}
	tasks := []config.ResolvedBackup{
		{Name: "a", Command: config.Command{Argv: []string{"x"}}, RepoRoot: t.TempDir(), Output: filepath.Join(t.TempDir(), "a")},
	}
	if err := b.Run(context.Background(), tasks, []string{"nonexistent"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.CallLines()) != 0 {
		t.Errorf("filtered-out task must not run: %v", fake.CallLines())
	}
}

func TestUpdateContinueOnError(t *testing.T) {
	fake := runner.NewFake()
	fake.AddStub("false", runner.Result{ExitCode: 1}, runner.FailWith(runner.Spec{Name: "false"}, 1, "boom"))
	u := &Update{R: fake, Rep: rep()}
	tasks := []config.ResolvedUpdate{{
		Name:            "x",
		Commands:        []config.Command{{Argv: []string{"false"}}, {Argv: []string{"true"}}},
		ContinueOnError: true,
	}}
	if err := u.Run(context.Background(), tasks, nil); err != nil {
		t.Fatalf("continue_on_error should not fail the task: %v", err)
	}
	var ranTrue bool
	for _, line := range fake.CallLines() {
		if line == "true" {
			ranTrue = true
		}
	}
	if !ranTrue {
		t.Error("expected execution to continue to the next command")
	}
}
