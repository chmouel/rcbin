// Package maintenance runs data-driven backup and update tasks. Tasks declare
// their platforms, required executables, and command forms. Missing optional
// executables produce a skip rather than a failure.
package maintenance

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/runner"
)

// platformMatches reports whether the task applies to the current OS. An empty
// platform list matches everything.
func platformMatches(platforms []string, goos string) bool {
	if len(platforms) == 0 {
		return true
	}
	for _, p := range platforms {
		if p == goos {
			return true
		}
	}
	return false
}

// requirementsMet reports whether every required executable or path is present.
func requirementsMet(r runner.Runner, requires []string) (missing string, ok bool) {
	for _, req := range requires {
		if strings.Contains(req, "/") {
			if info, err := os.Stat(req); err != nil || info.IsDir() {
				return req, false
			}
			continue
		}
		if _, found := r.LookPath(req); !found {
			return req, false
		}
	}
	return "", true
}

// selected reports whether name is in the filter, or true when the filter is
// empty.
func selected(name string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, f := range filter {
		if f == name {
			return true
		}
	}
	return false
}

func currentGOOS(override string) string {
	if override != "" {
		return override
	}
	return runtime.GOOS
}

// commandSpec converts a config.Command into a runner spec executed in dir.
func commandSpec(c config.Command, dir, shell string) runner.Spec {
	if c.Shell != "" {
		return runner.Spec{Name: shell, Args: []string{"-c", c.Shell}, Dir: dir}
	}
	return runner.Spec{Name: c.Argv[0], Args: c.Argv[1:], Dir: dir}
}

// writeIfChanged atomically writes content to path when it differs from the
// current contents. It returns whether the file changed.
func writeIfChanged(path string, content []byte) (bool, error) {
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(content) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rc-backup-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return false, err
	}
	return true, nil
}
