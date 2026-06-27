package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// HTTPClient is the subset of http.Client used by Updater.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

var archiveDownloadLimit int64 = maxArchiveSize

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
	limited := io.LimitReader(resp.Body, archiveDownloadLimit+1)
	written, err := io.Copy(io.MultiWriter(tmp, hash), limited)
	if err != nil {
		_ = os.Remove(tmpName)
		return "", "", fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if written > archiveDownloadLimit {
		_ = os.Remove(tmpName)
		return "", "", fmt.Errorf("%s exceeds %d bytes", asset.Name, archiveDownloadLimit)
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

func (u *Updater) httpClient() HTTPClient {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 60 * time.Second}
}
