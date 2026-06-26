package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

func testReporter() *output.Reporter {
	var out, err bytes.Buffer
	return output.New(&out, &err, false, false)
}

func writeSelfUpdateFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestSymlinkInstallPullsBuildsAndGeneratesCompletion(t *testing.T) {
	home := t.TempDir()
	repoRoot := filepath.Join(home, "rc")
	builtBinary := filepath.Join(repoRoot, "bin", "rc")
	installPath := filepath.Join(home, ".local", "bin", "rc")
	completionPath := filepath.Join(home, ".config", "zsh", "functions", "hosts", "testhost", "_rc")
	writeSelfUpdateFile(t, builtBinary, "built")
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
	fake.AddStub("git -C "+resolvedRepoRoot+" config --get remote.origin.url", runner.Result{Stdout: "git@github.com:chmouel/rc.git\n"}, nil)
	fake.AddStub("git -C "+resolvedRepoRoot+" status --porcelain", runner.Result{}, nil)
	fake.AddStub("git -C "+resolvedRepoRoot+" pull --ff-only", runner.Result{}, nil)
	fake.AddStub("make build", runner.Result{}, nil)
	fake.AddStub(installPath+" completion zsh", runner.Result{Stdout: "#compdef rc\n"}, nil)

	u := &Updater{
		R:              fake,
		Rep:            testReporter(),
		InstallPath:    installPath,
		CompletionPath: completionPath,
	}
	if err := u.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	wantCalls := []string{
		"git -C " + resolvedRepoRoot + " config --get remote.origin.url",
		"git -C " + resolvedRepoRoot + " status --porcelain",
		"git -C " + resolvedRepoRoot + " pull --ff-only",
		"make build",
		installPath + " completion zsh",
	}
	gotCalls := fake.CallLines()
	if strings.Join(gotCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("calls:\n got %v\nwant %v", gotCalls, wantCalls)
	}
	if fake.Calls[3].Dir != resolvedRepoRoot {
		t.Fatalf("make build dir = %q, want %q", fake.Calls[3].Dir, resolvedRepoRoot)
	}
	data, err := os.ReadFile(completionPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "#compdef rc\n" {
		t.Fatalf("completion = %q", data)
	}
}

func TestSymlinkInstallFailsOnLocalChanges(t *testing.T) {
	home := t.TempDir()
	repoRoot := filepath.Join(home, "rc")
	builtBinary := filepath.Join(repoRoot, "bin", "rc")
	installPath := filepath.Join(home, ".local", "bin", "rc")
	writeSelfUpdateFile(t, builtBinary, "built")
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
	fake.AddStub("git -C "+resolvedRepoRoot+" config --get remote.origin.url", runner.Result{Stdout: "https://github.com/chmouel/rc\n"}, nil)
	fake.AddStub("git -C "+resolvedRepoRoot+" status --porcelain", runner.Result{Stdout: " M internal/app/commands.go\n"}, nil)

	runErr := (&Updater{
		R:              fake,
		Rep:            testReporter(),
		InstallPath:    installPath,
		CompletionPath: filepath.Join(home, "_rc"),
	}).Run(context.Background())
	if runErr == nil || !strings.Contains(runErr.Error(), "local changes") {
		t.Fatalf("err = %v, want local changes", runErr)
	}
	for _, line := range fake.CallLines() {
		if strings.Contains(line, "pull") || line == "make build" || strings.Contains(line, "completion zsh") {
			t.Fatalf("dirty repo must not pull/build/generate completion, calls: %v", fake.CallLines())
		}
	}
}

func TestSymlinkInstallDryRunDoesNotPullBuildOrWriteCompletion(t *testing.T) {
	home := t.TempDir()
	repoRoot := filepath.Join(home, "rc")
	builtBinary := filepath.Join(repoRoot, "bin", "rc")
	installPath := filepath.Join(home, ".local", "bin", "rc")
	completionPath := filepath.Join(home, ".config", "zsh", "functions", "hosts", "testhost", "_rc")
	writeSelfUpdateFile(t, builtBinary, "built")
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
	fake.AddStub("git -C "+resolvedRepoRoot+" config --get remote.origin.url", runner.Result{Stdout: "https://github.com/chmouel/rc.git\n"}, nil)
	fake.AddStub("git -C "+resolvedRepoRoot+" status --porcelain", runner.Result{}, nil)

	if err := (&Updater{
		R:              fake,
		Rep:            testReporter(),
		InstallPath:    installPath,
		CompletionPath: completionPath,
		DryRun:         true,
	}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, line := range fake.CallLines() {
		if strings.Contains(line, "pull") || line == "make build" || strings.Contains(line, "completion zsh") {
			t.Fatalf("dry-run must not pull/build/generate completion, calls: %v", fake.CallLines())
		}
	}
	if _, err := os.Lstat(completionPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not write completion, err=%v", err)
	}
}

func TestBinaryInstallDownloadsVerifiesReplacesAndGeneratesCompletion(t *testing.T) {
	home := t.TempDir()
	installPath := filepath.Join(home, ".local", "bin", "rc")
	completionPath := filepath.Join(home, ".config", "zsh", "functions", "hosts", "testhost", "_rc")
	writeSelfUpdateFile(t, installPath, "old")
	archive := rcArchive(t, "new binary")
	sum := sha256.Sum256(archive)
	archiveName := "rc_0.0.1-next_darwin_arm64.tar.gz"

	server := releaseServer(t, archiveName, archive, hex.EncodeToString(sum[:]))
	defer server.Close()

	fake := runner.NewFake()
	fake.AddStub(installPath+" completion zsh", runner.Result{Stdout: "#compdef rc\n"}, nil)

	u := &Updater{
		R:              fake,
		Rep:            testReporter(),
		InstallPath:    installPath,
		CompletionPath: completionPath,
		APIBase:        server.URL,
		GOOS:           "darwin",
		GOARCH:         "arm64",
	}
	if err := u.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new binary" {
		t.Fatalf("installed binary = %q", data)
	}
	if info, err := os.Stat(installPath); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed binary should be executable, info=%v err=%v", info, err)
	}
	comp, err := os.ReadFile(completionPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(comp) != "#compdef rc\n" {
		t.Fatalf("completion = %q", comp)
	}
}

func TestBinaryInstallFailsOnChecksumMismatch(t *testing.T) {
	home := t.TempDir()
	installPath := filepath.Join(home, ".local", "bin", "rc")
	writeSelfUpdateFile(t, installPath, "old")
	archive := rcArchive(t, "new binary")
	archiveName := "rc_0.0.1-next_darwin_arm64.tar.gz"
	server := releaseServer(t, archiveName, archive, strings.Repeat("0", sha256.Size*2))
	defer server.Close()

	fake := runner.NewFake()
	err := (&Updater{
		R:              fake,
		Rep:            testReporter(),
		InstallPath:    installPath,
		CompletionPath: filepath.Join(home, "_rc"),
		APIBase:        server.URL,
		GOOS:           "darwin",
		GOARCH:         "arm64",
	}).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	data, readErr := os.ReadFile(installPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "old" {
		t.Fatalf("binary should not be replaced on checksum mismatch, got %q", data)
	}
	if len(fake.CallLines()) != 0 {
		t.Fatalf("completion must not run after failed download, calls: %v", fake.CallLines())
	}
}

func rcArchive(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	data := []byte(content)
	if err := tw.WriteHeader(&tar.Header{
		Name: "rc",
		Mode: 0o755,
		Size: int64(len(data)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func releaseServer(t *testing.T, archiveName string, archive []byte, checksum string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var serverURL string
	mux.HandleFunc("/repos/chmouel/rc/releases/tags/nightly", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"assets":[{"name":"checksums.txt","browser_download_url":"%s/checksums.txt"},{"name":%q,"browser_download_url":"%s/archive.tar.gz"}]}`,
			serverURL, archiveName, serverURL)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", checksum, archiveName)
	})
	mux.HandleFunc("/archive.tar.gz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	server := httptest.NewServer(mux)
	serverURL = server.URL
	return server
}
