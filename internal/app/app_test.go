package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/rc/internal/runner"
)

// run executes the command tree with a fake runner against a temporary HOME so
// configuration loading is hermetic.
func run(t *testing.T, fake *runner.Fake, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	home := t.TempDir()
	return runWithHome(t, home, fake, args...)
}

func runWithHome(t *testing.T, home string, fake *runner.Fake, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	if fake == nil {
		fake = runner.NewFake()
	}
	var out, errBuf bytes.Buffer
	// Always pass an explicit host to avoid depending on the test machine.
	full := append([]string{"--host", "testhost"}, args...)
	code = Execute(context.Background(), full, Deps{
		Runner: fake,
		Stdout: &out,
		Stderr: &errBuf,
	})
	return out.String(), errBuf.String(), code
}

func writeAppFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNoArgsRunsDefaultWorkflow(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "rc", "zsh"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, ".config", "yadm", "hosts", "testhost", "bin", "new-tool")
	writeAppFile(t, source, "new")

	stdout, stderr, code := runWithHome(t, home, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("default run should keep stdout clean, got %q", stdout)
	}
	if !strings.Contains(stderr, "linked 2 targets (links=1 bins=1 completions=0)") {
		t.Fatalf("default run stderr should report linked breakdown, got:\n%s", stderr)
	}
}

func TestHelpPrintsHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "command", args: []string{"help"}},
		{name: "flag", args: []string{"--help"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runner.NewFake()
			out, errOut, code := run(t, fake, tt.args...)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, errOut)
			}
			if !strings.Contains(out, "Usage:") {
				t.Errorf("help output missing Usage:\n%s", out)
			}
			if calls := fake.CallLines(); len(calls) != 0 {
				t.Fatalf("help should not run workflow, calls: %v", calls)
			}
		})
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	_, errOut, code := run(t, nil, "frobnicate")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut, "unknown command") {
		t.Errorf("stderr = %q, want unknown command", errOut)
	}
}

func TestUnknownFlagIsUsageError(t *testing.T) {
	_, _, code := run(t, nil, "status", "--nope")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestConfigValidateSucceeds(t *testing.T) {
	_, errOut, code := run(t, nil, "config", "validate")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, errOut)
	}
	if !strings.Contains(errOut, "configuration valid") {
		t.Errorf("stderr missing validation message:\n%s", errOut)
	}
}

func TestStatusWaybarReportsZero(t *testing.T) {
	// No repositories exist under the temp HOME, so the count is zero and the
	// payload is valid JSON on stdout.
	out, _, code := run(t, nil, "status", "--format", "waybar")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var payload struct {
		Text    string `json:"text"`
		Tooltip string `json:"tooltip"`
		Class   string `json:"class"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("waybar output is not JSON: %v\n%s", err, out)
	}
	if payload.Text != "0" {
		t.Errorf("text = %q, want 0", payload.Text)
	}
}

func TestStatusInvalidFormatIsUsageError(t *testing.T) {
	_, _, code := run(t, nil, "status", "--format", "xml")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestSyncMutuallyExclusiveFlags(t *testing.T) {
	_, _, code := run(t, nil, "sync", "--skip-yadm", "--yadm-only")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestSyncChangedOnlyDoesNotCloneOrSyncYadmByDefault(t *testing.T) {
	fake := runner.NewFake()
	out, errOut, code := run(t, fake, "sync", "--changed-only")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, errOut)
	}
	if out != "" {
		t.Fatalf("changed-only human output should not use stdout, got %q", out)
	}
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "git clone") {
			t.Fatalf("changed-only must not clone missing repositories, calls: %v", fake.CallLines())
		}
		if strings.HasPrefix(line, "yadm ") {
			t.Fatalf("changed-only must not sync yadm by default, calls: %v", fake.CallLines())
		}
	}
}

func TestLinkRecreatesDesktopBin(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "rc", "zsh"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, ".config", "yadm", "hosts", "testhost", "bin", "new-tool")
	oldEntry := filepath.Join(home, ".local", "bin", "desktop", "old-tool")
	newTarget := filepath.Join(home, ".local", "bin", "desktop", "new-tool")
	writeAppFile(t, source, "new")
	writeAppFile(t, oldEntry, "old")

	stdout, stderr, code := runWithHome(t, home, nil, "link")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("link should keep stdout clean, got %q", stdout)
	}
	if !strings.Contains(stderr, "linked 2 targets (links=1 bins=1 completions=0)") {
		t.Fatalf("link stderr should report linked breakdown, got:\n%s", stderr)
	}
	if _, err := os.Lstat(oldEntry); !os.IsNotExist(err) {
		t.Fatalf("old desktop entry should be removed, got err=%v", err)
	}
	if info, err := os.Lstat(newTarget); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("new desktop binary should be linked, info=%v err=%v", info, err)
	}
}

func TestRunReportsLinkedBreakdown(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "rc", "zsh"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, ".config", "yadm", "hosts", "testhost", "bin", "new-tool")
	writeAppFile(t, source, "new")

	stdout, stderr, code := runWithHome(t, home, nil, "run")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("run should keep stdout clean, got %q", stdout)
	}
	if !strings.Contains(stderr, "linked 2 targets (links=1 bins=1 completions=0)") {
		t.Fatalf("run stderr should report linked breakdown, got:\n%s", stderr)
	}
}

func TestSelfUpdateSymlinkInstallWritesHostCompletion(t *testing.T) {
	home := t.TempDir()
	repoRoot := filepath.Join(home, "git", "perso", "rcbin")
	builtBinary := filepath.Join(repoRoot, "bin", "rc")
	installPath := filepath.Join(home, ".local", "bin", "rc")
	writeAppFile(t, builtBinary, "built")
	if err := os.Chmod(builtBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(installPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(builtBinary, installPath); err != nil {
		t.Fatal(err)
	}
	resolvedBinary, err := filepath.EvalSymlinks(builtBinary)
	if err != nil {
		t.Fatal(err)
	}
	resolvedRepoRoot := filepath.Dir(filepath.Dir(resolvedBinary))

	fake := runner.NewFake()
	fake.AddStub("git -C "+resolvedRepoRoot+" config --get remote.origin.url", runner.Result{Stdout: "git@github.com:chmouel/rcbin.git\n"}, nil)
	fake.AddStub("git -C "+resolvedRepoRoot+" status --porcelain", runner.Result{}, nil)
	fake.AddStub("git -C "+resolvedRepoRoot+" pull --ff-only", runner.Result{}, nil)
	fake.AddStub("make build", runner.Result{}, nil)
	fake.AddStub(installPath+" completion zsh", runner.Result{Stdout: "#compdef rc\n"}, nil)

	stdout, stderr, code := runWithHome(t, home, fake, "self-update", "--path", installPath)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("self-update should keep stdout clean, got %q", stdout)
	}
	completionPath := filepath.Join(home, ".config", "zsh", "functions", "hosts", "testhost", "_rc")
	data, err := os.ReadFile(completionPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "#compdef rc\n" {
		t.Fatalf("completion = %q", data)
	}
}

func TestMigrateCommandRemoved(t *testing.T) {
	_, errOut, code := run(t, nil, "migrate")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut, "unknown command") {
		t.Errorf("stderr = %q, want unknown command", errOut)
	}
}

func TestDoctorRunsAndReports(t *testing.T) {
	fake := runner.NewFake()
	// Report some required tools as missing to exercise warning paths without
	// failing the overall run unexpectedly; doctor returns an error only when a
	// check fails. We assert the command produces a deterministic exit code.
	_, _, code := run(t, fake, "doctor", "--offline")
	if code != 0 && code != 1 {
		t.Fatalf("doctor exit code = %d, want 0 or 1", code)
	}
}
