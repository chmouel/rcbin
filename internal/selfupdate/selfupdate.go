// Package selfupdate updates the installed rc binary and its shell completion.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

const (
	defaultOwner   = "chmouel"
	defaultRepo    = "rc"
	defaultTag     = "nightly"
	defaultAPIBase = "https://api.github.com"
	maxBinarySize  = 100 << 20
)

// HTTPClient is the subset of http.Client used by Updater.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

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

type release struct {
	Assets []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
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
	if !isChmouelRCRemote(origin) {
		return fmt.Errorf("%s origin %q is not github.com/chmouel/rc", repoRoot, origin)
	}
	return nil
}

func (u *Updater) runGit(ctx context.Context, repoRoot string, args ...string) (runner.Result, error) {
	gitArgs := append([]string{"-C", repoRoot}, args...)
	return u.R.Run(ctx, runner.Spec{Name: "git", Args: gitArgs})
}

func isChmouelRCRemote(remote string) bool {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimSuffix(remote, "/")
	remote = strings.TrimSuffix(remote, ".git")

	switch remote {
	case "https://github.com/chmouel/rc", "http://github.com/chmouel/rc",
		"git@github.com:chmouel/rc", "ssh://git@github.com/chmouel/rc":
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

func (u *Updater) fetchRelease(ctx context.Context) (release, error) {
	apiURL := strings.TrimRight(u.apiBase(), "/") + "/repos/" + url.PathEscape(u.owner()) + "/" +
		url.PathEscape(u.repo()) + "/releases/tags/" + url.PathEscape(u.tag())
	data, err := u.downloadBytes(ctx, apiURL, 2<<20)
	if err != nil {
		return release{}, fmt.Errorf("fetching release %s: %w", u.tag(), err)
	}
	var rel release
	if err := json.Unmarshal(data, &rel); err != nil {
		return release{}, fmt.Errorf("parsing release %s: %w", u.tag(), err)
	}
	return rel, nil
}

func (r release) asset(name string) (releaseAsset, bool) {
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return releaseAsset{}, false
}

func (r release) archiveAsset(goos, goarch string) (releaseAsset, bool) {
	suffix := fmt.Sprintf("_%s_%s.tar.gz", goos, goarch)
	for _, asset := range r.Assets {
		if strings.HasPrefix(asset.Name, "rc_") && strings.HasSuffix(asset.Name, suffix) {
			return asset, true
		}
	}
	return releaseAsset{}, false
}

func (u *Updater) downloadArchive(ctx context.Context, asset releaseAsset) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "rc-self-update")

	resp, err := u.httpClient().Do(req)
	if err != nil {
		return "", "", fmt.Errorf("downloading %s: %w", asset.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("downloading %s: HTTP %s", asset.Name, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(u.InstallPath), ".rc-download-*")
	if err != nil {
		return "", "", err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
	}()

	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hash), resp.Body); err != nil {
		_ = os.Remove(tmpName)
		return "", "", fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", "", err
	}
	return tmpName, hex.EncodeToString(hash.Sum(nil)), nil
}

func (u *Updater) downloadBytes(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "rc-self-update")

	resp, err := u.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	limited := io.LimitReader(resp.Body, limit+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return data, nil
}

func checksumFor(data []byte, name string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[len(fields)-1] != name {
			continue
		}
		sum := fields[0]
		if len(sum) != sha256.Size*2 {
			return "", fmt.Errorf("invalid checksum for %s", name)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return "", fmt.Errorf("invalid checksum for %s: %w", name, err)
		}
		return strings.ToLower(sum), nil
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s", name)
}

func extractRCBinary(archivePath, dir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", archivePath, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", archivePath, err)
		}
		if filepath.Base(filepath.Clean(header.Name)) != "rc" {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return "", fmt.Errorf("archive entry %s is not a regular file", header.Name)
		}
		if header.Size <= 0 || header.Size > maxBinarySize {
			return "", fmt.Errorf("archive entry %s has invalid size %d", header.Name, header.Size)
		}

		tmp, err := os.CreateTemp(dir, ".rc-new-*")
		if err != nil {
			return "", err
		}
		tmpName := tmp.Name()
		written, err := io.CopyN(tmp, tr, header.Size)
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			return "", fmt.Errorf("extracting rc binary: %w", err)
		}
		if written != header.Size {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			return "", fmt.Errorf("extracting rc binary: wrote %d bytes, want %d", written, header.Size)
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmpName)
			return "", err
		}
		mode := header.FileInfo().Mode().Perm()
		if mode&0o111 == 0 {
			mode = 0o755
		}
		if err := os.Chmod(tmpName, mode); err != nil {
			_ = os.Remove(tmpName)
			return "", err
		}
		return tmpName, nil
	}
	return "", fmt.Errorf("%s does not contain an rc binary", archivePath)
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

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rc-completion-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (u *Updater) httpClient() HTTPClient {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 60 * time.Second}
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
