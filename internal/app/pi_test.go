package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/rc/internal/runner"
)

// piConfig writes a global configuration that enables Pi sync against a
// desktop-config checkout under the temporary home.
func piConfig(t *testing.T, home, body string) {
	t.Helper()
	writeAppFile(t, filepath.Join(home, ".config", "rc", "config.toml"), `[roots]
desktop_config = "${HOME}/.local/share/desktop-config"

[pi]
`+body)
}

func piFake() *runner.Fake {
	fake := runner.NewFake()
	fake.AddStub("pi --version", runner.Result{Stdout: "1.3.0"}, nil)
	fake.AddStub("pi update", runner.Result{}, nil)
	return fake
}

func piAgentDir(home string) string {
	return filepath.Join(home, ".pi", "agent")
}

func piSourceDir(home string) string {
	return filepath.Join(home, ".local", "share", "desktop-config", "pi", "agent")
}

func TestSyncPiSeedsLocalAgentConfig(t *testing.T) {
	home := t.TempDir()
	piConfig(t, home, "enabled = true\nsync_auth = false\n")
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "desktop-config"), 0o755); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(piAgentDir(home), "settings.json")
	writeAppFile(t, local, `{"local":true}`)

	fake := piFake()
	stdout, stderr, code := runWithHome(t, home, fake, "sync")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("sync should keep stdout clean, got %q", stdout)
	}
	if !strings.Contains(stderr, "pi: seeded") {
		t.Fatalf("stderr should report the seeding:\n%s", stderr)
	}

	canonical := filepath.Join(piSourceDir(home), "settings.json")
	link, err := os.Readlink(local)
	if err != nil || link != canonical {
		t.Fatalf("agent settings link = %q (err=%v), want %q", link, err, canonical)
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"local":true}` {
		t.Fatalf("seeded content = %q", data)
	}

	var updated bool
	for _, call := range fake.CallRecords() {
		if call.Name != "pi" || len(call.Args) == 0 || call.Args[0] != "update" {
			continue
		}
		updated = true
		if got := strings.Join(call.Args, " "); got != "update --extensions --no-approve" {
			t.Errorf("update args = %q", got)
		}
		if call.Dir != piAgentDir(home) {
			t.Errorf("update Dir = %q, want %q", call.Dir, piAgentDir(home))
		}
		if len(call.Extra) != 1 || call.Extra[0] != "PI_CODING_AGENT_DIR="+piAgentDir(home) {
			t.Errorf("update Extra = %v", call.Extra)
		}
	}
	if !updated {
		t.Fatalf("pi update --extensions was not run: %v", fake.CallLines())
	}
}

func TestSyncPiConflictRequiresAdoptFlag(t *testing.T) {
	home := t.TempDir()
	piConfig(t, home, "enabled = true\n")
	canonical := filepath.Join(piSourceDir(home), "settings.json")
	writeAppFile(t, canonical, "canonical")
	local := filepath.Join(piAgentDir(home), "settings.json")
	writeAppFile(t, local, "local")

	_, stderr, code := runWithHome(t, home, piFake(), "sync")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stderr, "--adopt-pi") {
		t.Fatalf("stderr should ask for --adopt-pi:\n%s", stderr)
	}
	data, err := os.ReadFile(local)
	if err != nil || string(data) != "local" {
		t.Fatalf("local file must be untouched, got %q (err=%v)", data, err)
	}

	_, stderr, code = runWithHome(t, home, piFake(), "sync", "--adopt-pi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, stderr)
	}
	backup, err := os.ReadFile(local + ".rc-adopt.bak")
	if err != nil || string(backup) != "local" {
		t.Fatalf("backup = %q (err=%v)", backup, err)
	}
	if link, err := os.Readlink(local); err != nil || link != canonical {
		t.Fatalf("agent settings link = %q (err=%v), want %q", link, err, canonical)
	}
}

func TestRunPiFailureDoesNotSkipLinkOrBackup(t *testing.T) {
	home := t.TempDir()
	writeAppFile(t, filepath.Join(home, ".config", "rc", "config.toml"), `[roots]
desktop_config = "${HOME}/.local/share/desktop-config"

[pi]
enabled = true

[[backups]]
name = "probe"
command = { argv = ["echo", "backup-ran"] }
repo = "desktop_config"
output = "probe.txt"
`)
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "rc", "zsh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAppFile(t, filepath.Join(piSourceDir(home), "settings.json"), "canonical")
	writeAppFile(t, filepath.Join(piAgentDir(home), "settings.json"), "local")
	writeAppFile(t, filepath.Join(home, ".config", "yadm", "hosts", "testhost", "bin", "new-tool"), "new")

	fake := piFake()
	_, stderr, code := runWithHome(t, home, fake, "run")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 for the Pi conflict (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stderr, "--adopt-pi") {
		t.Fatalf("stderr should report the Pi conflict:\n%s", stderr)
	}
	linked := filepath.Join(home, ".local", "bin", "desktop", "new-tool")
	if info, err := os.Lstat(linked); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link phase must still run after a Pi failure, info=%v err=%v", info, err)
	}
	var backedUp bool
	for _, line := range fake.CallLines() {
		if line == "echo backup-ran" {
			backedUp = true
		}
	}
	if !backedUp {
		t.Fatalf("backup phase must still run after a Pi failure: %v", fake.CallLines())
	}
}

func TestRunSkipsPiWhenSyncPhaseFails(t *testing.T) {
	// A failing sync phase must keep Pi, link, and backup all unstarted. The Pi
	// fixture is actionable: with the desktop-config checkout present and a
	// local settings.json, an ungated Pi sync would seed the canonical file.
	home := t.TempDir()
	repoPath := filepath.Join(home, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAppFile(t, filepath.Join(home, ".config", "rc", "config.toml"), `[roots]
desktop_config = "${HOME}/.local/share/desktop-config"

[pi]
enabled = true

[[git.repositories]]
path = "${HOME}/repo"
`)
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "desktop-config"), 0o755); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(piAgentDir(home), "settings.json")
	writeAppFile(t, local, "local")
	writeAppFile(t, filepath.Join(home, ".config", "yadm", "hosts", "testhost", "bin", "new-tool"), "new")

	fake := piFake()
	fake.AddStub("git -C "+repoPath+" pull --quiet", runner.Result{ExitCode: 1, Stderr: "boom"}, errors.New("exit status 1"))

	_, stderr, code := runWithHome(t, home, fake, "run")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr=%s)", code, stderr)
	}
	if strings.Contains(stderr, "pi:") {
		t.Fatalf("pi should not run before a clean sync phase:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(piSourceDir(home), "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("pi must not seed after a sync failure, stat err=%v", err)
	}
	if got, err := os.ReadFile(local); err != nil || string(got) != "local" {
		t.Fatalf("the local agent config must be untouched, got %q (err=%v)", got, err)
	}
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "pi ") {
			t.Fatalf("no pi command may run after a sync failure: %v", fake.CallLines())
		}
	}
	linked := filepath.Join(home, ".local", "bin", "desktop", "new-tool")
	if _, err := os.Lstat(linked); !os.IsNotExist(err) {
		t.Fatalf("link must not run after a sync failure, got err=%v", err)
	}
}

func TestSyncRepoFailureSkipsPi(t *testing.T) {
	home := t.TempDir()
	repoPath := filepath.Join(home, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAppFile(t, filepath.Join(home, ".config", "rc", "config.toml"), `[roots]
desktop_config = "${HOME}/.local/share/desktop-config"

[pi]
enabled = true

[[git.repositories]]
path = "${HOME}/repo"
`)
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "desktop-config"), 0o755); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(piAgentDir(home), "settings.json")
	writeAppFile(t, local, "local")

	fake := piFake()
	fake.AddStub("git -C "+repoPath+" pull --quiet", runner.Result{ExitCode: 1, Stderr: "boom"}, errors.New("exit status 1"))

	_, stderr, code := runWithHome(t, home, fake, "sync")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stderr, "skipping the Pi agent configuration") {
		t.Fatalf("stderr should explain the skipped Pi phase:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(piSourceDir(home), "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("pi must not seed after a repository failure, stat err=%v", err)
	}
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "pi ") {
			t.Fatalf("no pi command may run after a repository failure: %v", fake.CallLines())
		}
	}
}

func TestSyncYadmOnlySkipsPi(t *testing.T) {
	home := t.TempDir()
	piConfig(t, home, "enabled = true\n")
	writeAppFile(t, filepath.Join(piSourceDir(home), "settings.json"), "canonical")
	local := filepath.Join(piAgentDir(home), "settings.json")
	writeAppFile(t, local, "local")

	_, stderr, code := runWithHome(t, home, piFake(), "sync", "--yadm-only")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, stderr)
	}
	if got, err := os.ReadFile(local); err != nil || string(got) != "local" {
		t.Fatalf("yadm-only must not touch the agent directory, got %q (err=%v)", got, err)
	}
}

func TestSyncPiDisabled(t *testing.T) {
	home := t.TempDir()
	piConfig(t, home, "enabled = false\n")
	local := filepath.Join(piAgentDir(home), "settings.json")
	writeAppFile(t, local, "local")

	fake := piFake()
	_, stderr, code := runWithHome(t, home, fake, "sync")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, stderr)
	}
	if strings.Contains(stderr, "pi:") {
		t.Fatalf("disabled Pi sync should stay silent:\n%s", stderr)
	}
	if _, err := os.Stat(piSourceDir(home)); !os.IsNotExist(err) {
		t.Fatalf("disabled Pi sync must not create the source: %v", err)
	}
	if info, err := os.Lstat(local); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("disabled Pi sync must not link: info=%v err=%v", info, err)
	}
	for _, call := range fake.CallLines() {
		if strings.HasPrefix(call, "pi update") {
			t.Fatalf("disabled Pi sync must not update packages: %v", fake.CallLines())
		}
	}
}

func TestSyncPiDryRunCreatesNothing(t *testing.T) {
	home := t.TempDir()
	piConfig(t, home, "enabled = true\n")
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "desktop-config"), 0o755); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(piAgentDir(home), "settings.json")
	writeAppFile(t, local, "local")

	fake := piFake()
	_, stderr, code := runWithHome(t, home, fake, "sync", "--dry-run")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stderr, "pi: would seed") || !strings.Contains(stderr, "would create") {
		t.Fatalf("dry run should report the planned seeding:\n%s", stderr)
	}
	if _, err := os.Stat(piSourceDir(home)); !os.IsNotExist(err) {
		t.Fatalf("dry run created the source: %v", err)
	}
	if got, err := os.ReadFile(local); err != nil || string(got) != "local" {
		t.Fatalf("dry run changed the target: %q (err=%v)", got, err)
	}
	for _, call := range fake.CallLines() {
		if strings.HasPrefix(call, "pi ") {
			t.Fatalf("dry run ran a pi command: %v", fake.CallLines())
		}
	}
}

func TestSyncPiSkipsWhenPiIsNotInstalled(t *testing.T) {
	home := t.TempDir()
	piConfig(t, home, "enabled = true\n")
	local := filepath.Join(piAgentDir(home), "settings.json")
	writeAppFile(t, local, "local")

	fake := piFake()
	fake.Missing["pi"] = true
	_, stderr, code := runWithHome(t, home, fake, "sync")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, stderr)
	}
	if _, err := os.Stat(piSourceDir(home)); !os.IsNotExist(err) {
		t.Fatalf("nothing should be created without pi: %v", err)
	}
	if info, err := os.Lstat(local); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("nothing should be linked without pi: info=%v err=%v", info, err)
	}
}

func TestSyncPiNeedsDesktopConfigCheckout(t *testing.T) {
	home := t.TempDir()
	piConfig(t, home, "enabled = true\n")
	local := filepath.Join(piAgentDir(home), "settings.json")
	writeAppFile(t, local, "local")

	fake := piFake()
	_, stderr, code := runWithHome(t, home, fake, "sync")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, stderr)
	}
	if _, err := os.Stat(piSourceDir(home)); !os.IsNotExist(err) {
		t.Fatalf("the source must not be created without desktop-config: %v", err)
	}
}

func TestSyncPiExplicitSourceIsCreated(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "custom", "pi")
	writeAppFile(t, filepath.Join(home, ".config", "rc", "config.toml"), `[pi]
enabled = true
source = "${HOME}/custom/pi"
`)
	local := filepath.Join(piAgentDir(home), "settings.json")
	writeAppFile(t, local, "local")

	_, stderr, code := runWithHome(t, home, piFake(), "sync")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, stderr)
	}
	canonical := filepath.Join(source, "settings.json")
	if link, err := os.Readlink(local); err != nil || link != canonical {
		t.Fatalf("agent settings link = %q (err=%v), want %q", link, err, canonical)
	}
}

func TestSyncCloneFailureSkipsPi(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "missing-repo")
	writeAppFile(t, filepath.Join(home, ".config", "rc", "config.toml"), `[roots]
desktop_config = "${HOME}/.local/share/desktop-config"

[git]
provider = "git@example.com:user"

[pi]
enabled = true

[[git.repositories]]
path = "${HOME}/missing-repo"
clone = "optional-config"
`)
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "desktop-config"), 0o755); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(piAgentDir(home), "settings.json")
	writeAppFile(t, local, "local")

	fake := piFake()
	fake.AddStub("git clone git@example.com:user/optional-config.git "+missing,
		runner.Result{ExitCode: 128, Stderr: "denied"}, errors.New("exit status 128"))

	_, stderr, code := runWithHome(t, home, fake, "sync")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 for the failed clone (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stderr, "clone step reported issues") {
		t.Fatalf("stderr should report the clone failure:\n%s", stderr)
	}
	if !strings.Contains(stderr, "skipping the Pi agent configuration") {
		t.Fatalf("a failed clone must not count as a clean synchronization:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(piSourceDir(home), "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("pi must not seed after a failed clone, stat err=%v", err)
	}
	if got, err := os.ReadFile(local); err != nil || string(got) != "local" {
		t.Fatalf("the local agent config must be untouched, got %q (err=%v)", got, err)
	}
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "pi ") {
			t.Fatalf("no pi command may run after a failed clone: %v", fake.CallLines())
		}
	}
}
