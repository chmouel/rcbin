// Package host detects the current short hostname and selects the ordered set
// of overlay profile directories that apply to it.
package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chmouel/rc/internal/runner"
)

// Detect resolves the lowercase short hostname using, in order, the HOSTNAME
// environment variable, the hostname command, and hostnamectl.
func Detect() (string, error) {
	return DetectWithRunner(context.Background(), runner.New())
}

// DetectWithRunner resolves the lowercase short hostname using r for external
// probes.
func DetectWithRunner(ctx context.Context, r runner.Runner) (string, error) {
	if h := os.Getenv("HOSTNAME"); h != "" {
		return normalize(h), nil
	}
	if p, ok := r.LookPath("hostname"); ok {
		if res, err := r.Run(ctx, runner.Spec{Name: p, Args: []string{"-s"}}); err == nil {
			if s := normalize(res.Stdout); s != "" {
				return s, nil
			}
		}
	}
	if p, ok := r.LookPath("hostnamectl"); ok {
		if res, err := r.Run(ctx, runner.Spec{Name: p, Args: []string{"hostname"}}); err == nil {
			if s := normalize(res.Stdout); s != "" {
				return s, nil
			}
		}
	}
	if h, err := os.Hostname(); err == nil {
		if s := normalize(h); s != "" {
			return s, nil
		}
	}
	return "", fmt.Errorf("no hostname detected")
}

func normalize(h string) string {
	h = strings.TrimSpace(h)
	if i := strings.IndexByte(h, '.'); i >= 0 {
		h = h[:i]
	}
	return strings.ToLower(h)
}

// Profiles returns the ordered overlay directories under hostsRoot for the
// given lowercase hostname.
//
// The order is the single documented merge order applied everywhere:
//
//  1. common (when present)
//  2. multi-host directories ("a,b,c") that list the hostname, sorted
//     lexically by directory name for determinism
//  3. the exact hostname directory (highest priority)
//
// Directories beginning with "." are ignored.
func Profiles(hostsRoot, hostname string) ([]string, error) {
	var profiles []string

	common := filepath.Join(hostsRoot, "common")
	if isDir(common) {
		profiles = append(profiles, common)
	}

	entries, err := os.ReadDir(hostsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return profiles, nil
		}
		return nil, fmt.Errorf("reading hosts root %q: %w", hostsRoot, err)
	}

	var multi []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "common" {
			continue
		}
		if !strings.Contains(name, ",") {
			continue
		}
		for _, h := range strings.Split(name, ",") {
			if strings.TrimSpace(h) == hostname {
				multi = append(multi, filepath.Join(hostsRoot, name))
				break
			}
		}
	}
	sort.Strings(multi)
	profiles = append(profiles, multi...)

	exact := filepath.Join(hostsRoot, hostname)
	if isDir(exact) {
		profiles = append(profiles, exact)
	}

	return profiles, nil
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
