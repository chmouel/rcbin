// Package selfupdate updates the installed rc binary and its shell completion.
package selfupdate

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

const (
	defaultOwner   = "chmouel"
	defaultRepo    = "rcbin"
	defaultTag     = "nightly"
	defaultAPIBase = "https://api.github.com"
	maxBinarySize  = 100 << 20
	maxArchiveSize = 100 << 20
)

// Updater updates an installed rc binary.
type Updater struct {
	R              runner.Runner
	Rep            *output.Reporter
	Client         HTTPClient
	InstallPath    string
	CompletionPath string
	Owner          string
	Repo           string
	Tag            string
	APIBase        string
	GOOS           string
	GOARCH         string
	DryRun         bool
}

// Run updates rc and regenerates its Zsh completion.
func (u *Updater) Run(ctx context.Context) error {
	if u.R == nil {
		return errors.New("runner is required")
	}
	if u.Rep == nil {
		return errors.New("reporter is required")
	}
	if u.InstallPath == "" {
		return errors.New("install path is required")
	}
	if u.CompletionPath == "" {
		return errors.New("completion path is required")
	}

	info, err := os.Lstat(u.InstallPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("rc binary not found at %s", u.InstallPath)
		}
		return fmt.Errorf("checking %s: %w", u.InstallPath, err)
	}

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		if err := u.updateSymlinkInstall(ctx); err != nil {
			return err
		}
	case info.Mode().IsRegular():
		if err := u.updateBinaryInstall(ctx); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%s is not a symlink or regular binary", u.InstallPath)
	}

	if err := u.generateCompletion(ctx); err != nil {
		return err
	}
	if !u.DryRun {
		u.Rep.Successf("rc self-update complete")
	}
	return nil
}

func (u *Updater) updateSymlinkInstall(ctx context.Context) error {
	target, err := filepath.EvalSymlinks(u.InstallPath)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", u.InstallPath, err)
	}
	repoRoot, ok := sourceRepoRoot(target)
	if !ok {
		return fmt.Errorf("%s points to %s, not to a rc source checkout bin/rc", u.InstallPath, target)
	}
	if err := u.verifyOrigin(ctx, repoRoot); err != nil {
		return err
	}
	status, err := u.runGit(ctx, repoRoot, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("checking local changes in %s: %w", repoRoot, err)
	}
	if strings.TrimSpace(status.Stdout) != "" {
		return fmt.Errorf("%s has local changes; refusing to self-update", repoRoot)
	}

	if u.DryRun {
		u.Rep.Infof("[dry-run] would pull %s and run make build", repoRoot)
		return nil
	}

	u.Rep.Infof("updating source checkout %s", repoRoot)
	if _, err := u.runGit(ctx, repoRoot, "pull", "--ff-only"); err != nil {
		return fmt.Errorf("pulling %s: %w", repoRoot, err)
	}
	if _, err := u.R.Run(ctx, runner.Spec{Name: "make", Args: []string{"build"}, Dir: repoRoot}); err != nil {
		return fmt.Errorf("building %s: %w", repoRoot, err)
	}
	u.Rep.Successf("source checkout updated")
	return nil
}

func sourceRepoRoot(target string) (string, bool) {
	if filepath.Base(target) != "rc" {
		return "", false
	}
	binDir := filepath.Dir(target)
	if filepath.Base(binDir) != "bin" {
		return "", false
	}
	return filepath.Dir(binDir), true
}

func (u *Updater) verifyOrigin(ctx context.Context, repoRoot string) error {
	res, err := u.runGit(ctx, repoRoot, "config", "--get", "remote.origin.url")
	if err != nil {
		return fmt.Errorf("checking origin for %s: %w", repoRoot, err)
	}
	origin := strings.TrimSpace(res.Stdout)
	if !isChmouelRCBinRemote(origin) {
		return fmt.Errorf("%s origin %q is not github.com/chmouel/rcbin", repoRoot, origin)
	}
	return nil
}

func (u *Updater) runGit(ctx context.Context, repoRoot string, args ...string) (runner.Result, error) {
	gitArgs := append([]string{"-C", repoRoot}, args...)
	return u.R.Run(ctx, runner.Spec{Name: "git", Args: gitArgs})
}

func isChmouelRCBinRemote(remote string) bool {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimSuffix(remote, "/")
	remote = strings.TrimSuffix(remote, ".git")

	switch remote {
	case "https://github.com/chmouel/rcbin", "http://github.com/chmouel/rcbin",
		"git@github.com:chmouel/rcbin", "ssh://git@github.com/chmouel/rcbin":
		return true
	default:
		return false
	}
}

func (u *Updater) updateBinaryInstall(ctx context.Context) error {
	goos, goarch := u.goos(), u.goarch()
	if u.DryRun {
		u.Rep.Infof("[dry-run] would download %s release asset for %s/%s and replace %s",
			u.tag(), goos, goarch, u.InstallPath)
		return nil
	}

	rel, err := u.fetchRelease(ctx)
	if err != nil {
		return err
	}
	archive, ok := rel.archiveAsset(goos, goarch)
	if !ok {
		return fmt.Errorf("%s release has no rc archive for %s/%s", u.tag(), goos, goarch)
	}
	checksums, ok := rel.asset("checksums.txt")
	if !ok {
		return fmt.Errorf("%s release has no checksums.txt asset", u.tag())
	}

	checksumData, err := u.downloadBytes(ctx, checksums.BrowserDownloadURL, 1<<20)
	if err != nil {
		return fmt.Errorf("downloading checksums.txt: %w", err)
	}
	want, err := checksumFor(checksumData, archive.Name)
	if err != nil {
		return err
	}

	archivePath, got, err := u.downloadArchive(ctx, archive)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(archivePath)
	}()
	if subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		return fmt.Errorf("checksum mismatch for %s: got %s want %s", archive.Name, got, want)
	}

	binaryPath, err := extractRCBinary(archivePath, filepath.Dir(u.InstallPath))
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(binaryPath)
	}()
	if err := os.Rename(binaryPath, u.InstallPath); err != nil {
		return fmt.Errorf("replacing %s: %w", u.InstallPath, err)
	}
	u.Rep.Successf("rc binary updated from %s", archive.Name)
	return nil
}

func (u *Updater) generateCompletion(ctx context.Context) error {
	if u.DryRun {
		u.Rep.Infof("[dry-run] would generate zsh completion at %s", u.CompletionPath)
		return nil
	}

	res, err := u.R.Run(ctx, runner.Spec{Name: u.InstallPath, Args: []string{"completion", "zsh"}})
	if err != nil {
		return fmt.Errorf("generating zsh completion: %w", err)
	}
	if strings.TrimSpace(res.Stdout) == "" {
		return fmt.Errorf("generated zsh completion is empty")
	}
	if err := writeAtomic(u.CompletionPath, []byte(res.Stdout), 0o644); err != nil {
		return fmt.Errorf("writing zsh completion to %s: %w", u.CompletionPath, err)
	}
	u.Rep.Successf("zsh completion updated at %s", u.CompletionPath)
	return nil
}

func (u *Updater) owner() string {
	if u.Owner != "" {
		return u.Owner
	}
	return defaultOwner
}

func (u *Updater) repo() string {
	if u.Repo != "" {
		return u.Repo
	}
	return defaultRepo
}

func (u *Updater) tag() string {
	if u.Tag != "" {
		return u.Tag
	}
	return defaultTag
}

func (u *Updater) apiBase() string {
	if u.APIBase != "" {
		return u.APIBase
	}
	return defaultAPIBase
}

func (u *Updater) goos() string {
	if u.GOOS != "" {
		return u.GOOS
	}
	return runtime.GOOS
}

func (u *Updater) goarch() string {
	if u.GOARCH != "" {
		return u.GOARCH
	}
	return runtime.GOARCH
}
