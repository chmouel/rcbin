package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type release struct {
	Assets []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
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
