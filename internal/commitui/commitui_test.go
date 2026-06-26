package commitui

import (
	"context"
	"errors"
	"fmt"
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

// seqPrompter returns successive keys, one per Choice call, so a test can drive
// the action prompt and the follow-up "continue?" prompt independently.
type seqPrompter struct {
	keys []byte
	i    int
}

func (s *seqPrompter) Choice(string, byte) (byte, error) {
	if s.i >= len(s.keys) {
		return 0, fmt.Errorf("seqPrompter: no key for call %d", s.i)
	}
	k := s.keys[s.i]
	s.i++
	return k, nil
}

type fakeSelector struct {
	indexes []int
	ok      bool
	files   []changedFile
}

func (f *fakeSelector) SelectFiles(_ string, files []changedFile, _ *output.Reporter) ([]changedFile, bool, error) {
	f.files = append([]changedFile{}, files...)
	if !f.ok {
		return nil, false, nil
	}
	selected := make([]changedFile, 0, len(f.indexes))
	for _, idx := range f.indexes {
		if idx >= 0 && idx < len(files) {
			selected = append(selected, files[idx])
		}
	}
	return selected, len(selected) > 0, nil
}

func newAdapter(t *testing.T, key byte) (*Adapter, *runner.Fake) {
	t.Helper()
	fake := runner.NewFake()
	fake.AddStub("git -C", runner.Result{Stdout: "abc123\n"}, nil)
	rep := output.New(os.Stdout, os.Stderr, false, false)
	return &Adapter{R: fake, Rep: rep, Pr: fakePrompter{key: key}}, fake
}

func TestMenuPromptHighlightsHotkeys(t *testing.T) {
	a := &Adapter{Rep: output.New(io.Discard, io.Discard, true, false)}
	prompt := a.menuPrompt("emacs-config")
	if !strings.Contains(prompt, "emacs-config") {
		t.Errorf("menu prompt must include the repo name, got %q", prompt)
	}
	if !strings.Contains(prompt, "\033[") {
		t.Errorf("menu prompt with color on must emit ANSI, got %q", prompt)
	}

	plain := (&Adapter{Rep: output.New(io.Discard, io.Discard, false, false)}).menuPrompt("emacs-config")
	if strings.Contains(plain, "\033[") {
		t.Errorf("menu prompt with color off must be plain, got %q", plain)
	}
	for _, want := range []string{"[m]", "[l]", "[s]", "[a]", "[c]", "[q]"} {
		if !strings.Contains(plain, want) {
			t.Errorf("menu prompt missing %q in %q", want, plain)
		}
	}
}

func TestColorStatusLineColorsColumns(t *testing.T) {
	a := &Adapter{Rep: output.New(io.Discard, io.Discard, true, false)}
	got := a.colorStatusLine(" M early-init.el")
	if !strings.Contains(got, "early-init.el") {
		t.Errorf("status line must keep the path, got %q", got)
	}
	if !strings.Contains(got, "\033[") {
		t.Errorf("status line with color on must emit ANSI, got %q", got)
	}
	plain := (&Adapter{Rep: output.New(io.Discard, io.Discard, false, false)}).colorStatusLine(" M early-init.el")
	if plain != " M early-init.el" {
		t.Errorf("status line with color off must be unchanged, got %q", plain)
	}
}

func TestMagitUsesTtyFrameAndRepoDir(t *testing.T) {
	fake := runner.NewFake()
	fake.AddStub("git -C", runner.Result{Stdout: "abc123\n"}, nil)
	fake.AddStub("pgrep", runner.Result{Stdout: "123\n"}, nil) // emacs server present
	a := &Adapter{
		R:   fake,
		Rep: output.New(os.Stdout, os.Stderr, false, false),
		Pr:  &seqPrompter{keys: []byte{'m', 'n'}}, // magit, then quit before pull/push
	}
	if _, err := a.Handle(context.Background(), config.RepoTarget{Path: "/repo"}, "repo"); !errors.Is(err, repo.ErrAbort) {
		t.Fatalf("expected ErrAbort after declining, got %v", err)
	}
	var emacsLine string
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "emacsclient") {
			emacsLine = line
		}
	}
	if emacsLine == "" {
		t.Fatalf("expected an emacsclient invocation, calls: %v", fake.CallLines())
	}
	if !strings.Contains(emacsLine, " -t ") {
		t.Errorf("emacsclient must open a tty frame (-t), got %q", emacsLine)
	}
	if strings.Contains(emacsLine, "-u") {
		t.Errorf("emacsclient must not suppress the frame with -u, got %q", emacsLine)
	}
	if !strings.Contains(emacsLine, `"/repo/"`) {
		t.Errorf("emacsclient must bind default-directory to the repo, got %q", emacsLine)
	}
}

func TestParseChangedFilesZPreservesPathsAndRenames(t *testing.T) {
	files := parseChangedFilesZ(" M plain file\x00R  new name\x00old name\x00?? odd\nname\x00")
	if len(files) != 3 {
		t.Fatalf("expected 3 changed files, got %#v", files)
	}
	if files[0].Status != " M" || files[0].Path != "plain file" {
		t.Errorf("first file parsed incorrectly: %#v", files[0])
	}
	if files[1].Status != "R " || files[1].Path != "new name" || files[1].OldPath != "old name" {
		t.Errorf("rename parsed incorrectly: %#v", files[1])
	}
	if got := files[2].displayPath(); got != `odd\nname` {
		t.Errorf("display path should sanitize newlines, got %q", got)
	}
}

func TestFileSelectStateMovesAndToggles(t *testing.T) {
	state := newFileSelectState(3)
	state.apply(selectKeyDown, 2)
	state.apply(selectKeyToggle, 2)
	if state.cursor != 1 || !state.selected[1] {
		t.Fatalf("expected cursor 1 selected, state: %+v", state)
	}
	state.apply(selectKeyDown, 2)
	if state.cursor != 2 || state.offset != 1 {
		t.Fatalf("expected viewport to follow cursor, state: %+v", state)
	}
	state.apply(selectKeyToggleAll, 2)
	for i, selected := range state.selected {
		if !selected {
			t.Fatalf("toggle all should select index %d, state: %+v", i, state)
		}
	}
	state.apply(selectKeyToggleAll, 2)
	if state.selectedCount() != 0 {
		t.Fatalf("second toggle all should clear selection, state: %+v", state)
	}
	if action := state.apply(selectKeyAccept, 2); action != selectActionAccept {
		t.Fatalf("accept action = %v", action)
	}
}

func TestAICommitSingleFileRunsConfiguredToolInteractively(t *testing.T) {
	fake := runner.NewFake()
	dir := "/repo"
	fake.AddStub("git -C "+dir+" rev-parse HEAD", runner.Result{Stdout: "before\n"}, nil)
	fake.AddStub("git -C "+dir+" status --short", runner.Result{Stdout: " M one file\n"}, nil)
	fake.AddStub("git -C "+dir+" status --porcelain=v1 -z", runner.Result{Stdout: " M one file\x00"}, nil)

	a := &Adapter{
		R:     fake,
		Rep:   output.New(os.Stdout, os.Stderr, false, false),
		Tools: config.ToolsConfig{Aicommit: "aitool"},
		Pr:    &seqPrompter{keys: []byte{'a', 'n'}},
	}
	if _, err := a.Handle(context.Background(), config.RepoTarget{Path: dir}, "repo"); !errors.Is(err, repo.ErrAbort) {
		t.Fatalf("expected abort after declining to continue, got %v", err)
	}
	var sawAI bool
	for _, call := range fake.CallRecords() {
		if call.Name != "aitool" {
			continue
		}
		sawAI = true
		if !call.Interactive {
			t.Fatalf("aicommit must run interactively, call: %+v", call)
		}
		if got := strings.Join(call.Args, " "); got != "-a" {
			t.Fatalf("single-file AI commit should use -a, got %q", got)
		}
	}
	if !sawAI {
		t.Fatalf("expected configured AI tool to run, calls: %v", fake.CallLines())
	}
}

func TestAICommitSelectorRunsSelectedFilesIndividually(t *testing.T) {
	fake := runner.NewFake()
	dir := "/repo"
	fake.AddStub("git -C "+dir+" rev-parse HEAD", runner.Result{Stdout: "before\n"}, nil)
	fake.AddStub("git -C "+dir+" status --short", runner.Result{Stdout: " M first\n M second file\n?? third\n"}, nil)
	fake.AddStub("git -C "+dir+" status --porcelain=v1 -z", runner.Result{Stdout: " M first\x00 M second file\x00?? third\x00"}, nil)
	selector := &fakeSelector{indexes: []int{0, 2}, ok: true}

	a := &Adapter{
		R:        fake,
		Rep:      output.New(os.Stdout, os.Stderr, false, false),
		Pr:       &seqPrompter{keys: []byte{'a', 'n'}},
		Selector: selector,
	}
	if _, err := a.Handle(context.Background(), config.RepoTarget{Path: dir}, "repo"); !errors.Is(err, repo.ErrAbort) {
		t.Fatalf("expected abort after declining to continue, got %v", err)
	}
	if len(selector.files) != 3 || selector.files[1].Path != "second file" {
		t.Fatalf("selector did not receive parsed files: %#v", selector.files)
	}

	var aiCalls []runner.Call
	for _, call := range fake.CallRecords() {
		if call.Name == "aicommit" {
			aiCalls = append(aiCalls, call)
		}
	}
	if len(aiCalls) != 2 {
		t.Fatalf("expected one AI commit per selected file, got %+v", aiCalls)
	}
	for i, want := range []string{"first", "third"} {
		if !aiCalls[i].Interactive {
			t.Fatalf("AI call %d must be interactive: %+v", i, aiCalls[i])
		}
		if len(aiCalls[i].Args) != 1 || aiCalls[i].Args[0] != want {
			t.Fatalf("AI call %d args = %v, want %q", i, aiCalls[i].Args, want)
		}
	}
}

func TestAICommitSelectorCancelReturnsToMenu(t *testing.T) {
	fake := runner.NewFake()
	dir := "/repo"
	fake.AddStub("git -C "+dir+" rev-parse HEAD", runner.Result{Stdout: "before\n"}, nil)
	fake.AddStub("git -C "+dir+" status --short", runner.Result{Stdout: " M first\n M second\n"}, nil)
	fake.AddStub("git -C "+dir+" status --porcelain=v1 -z", runner.Result{Stdout: " M first\x00 M second\x00"}, nil)
	selector := &fakeSelector{ok: false}

	a := &Adapter{
		R:        fake,
		Rep:      output.New(os.Stdout, os.Stderr, false, false),
		Pr:       &seqPrompter{keys: []byte{'a', 's'}},
		Selector: selector,
	}
	changed, err := a.Handle(context.Background(), config.RepoTarget{Path: dir}, "repo")
	if err != nil {
		t.Fatalf("cancel then skip should succeed, got %v", err)
	}
	if changed {
		t.Fatal("cancel then skip should report no HEAD change")
	}
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "aicommit") {
			t.Fatalf("aicommit must not run after selector cancel, calls: %v", fake.CallLines())
		}
	}
}

func TestShowStatusPreservesFirstLineColumn(t *testing.T) {
	fake := runner.NewFake()
	// Worktree-modified files have a leading-space staged column (" M path").
	// The first line must keep that column instead of losing its first char.
	fake.AddStub("git -C /repo status --short",
		runner.Result{Stdout: " M early-init.el\n M init.el\n"}, nil)
	fake.AddStub("git -C", runner.Result{Stdout: "abc123\n"}, nil)
	var errBuf strings.Builder
	a := &Adapter{
		R:   fake,
		Rep: output.New(io.Discard, &errBuf, false, false),
	}
	a.showStatus(context.Background(), "/repo")
	got := errBuf.String()
	if !strings.Contains(got, "early-init.el") {
		t.Fatalf("first line mangled, got %q", got)
	}
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if !strings.HasSuffix(line, "early-init.el") && !strings.HasSuffix(line, "init.el") {
			t.Fatalf("unexpected status line %q", line)
		}
		if !strings.Contains(line, " M ") {
			t.Errorf("status column lost on line %q", line)
		}
	}
}

func TestBackReturnsToMenu(t *testing.T) {
	fake := runner.NewFake()
	fake.AddStub("git -C", runner.Result{Stdout: "abc123\n"}, nil)
	a := &Adapter{
		R:   fake,
		Rep: output.New(os.Stdout, os.Stderr, false, false),
		// lazygit, back to menu, skip out.
		Pr: &seqPrompter{keys: []byte{'l', 'b', 's'}},
	}
	changed, err := a.Handle(context.Background(), config.RepoTarget{Path: "/repo"}, "repo")
	if err != nil {
		t.Fatalf("back then skip should succeed, got %v", err)
	}
	if changed {
		t.Error("skip after back should report no change")
	}
	var lazygitRuns int
	for _, line := range fake.CallLines() {
		if strings.Contains(line, "lazygit") {
			lazygitRuns++
		}
		if strings.Contains(line, "pull") || strings.Contains(line, "push") {
			t.Errorf("skip must not pull or push, saw %q", line)
		}
	}
	if lazygitRuns != 1 {
		t.Errorf("expected lazygit to run once before going back, ran %d times", lazygitRuns)
	}
}

func TestContinueNoAborts(t *testing.T) {
	fake := runner.NewFake()
	fake.AddStub("git -C", runner.Result{Stdout: "abc123\n"}, nil)
	a := &Adapter{
		R:   fake,
		Rep: output.New(os.Stdout, os.Stderr, false, false),
		Pr:  &seqPrompter{keys: []byte{'c', 'n'}}, // commit, then decline to continue
	}
	if _, err := a.Handle(context.Background(), config.RepoTarget{Path: "/repo"}, "repo"); !errors.Is(err, repo.ErrAbort) {
		t.Fatalf("declining to continue should abort with repo.ErrAbort, got %v", err)
	}
	for _, line := range fake.CallLines() {
		if strings.Contains(line, "pull") || strings.Contains(line, "push") {
			t.Errorf("aborting must not pull or push, saw %q", line)
		}
	}
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
