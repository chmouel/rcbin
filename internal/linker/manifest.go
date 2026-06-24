package linker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Manifest records the links rc manages so stale links can be removed without
// touching files placed by the user or other tools.
type Manifest struct {
	// Links maps an absolute target path to its absolute source path.
	Links map[string]string `json:"links"`
}

// LoadManifest reads the manifest, returning an empty manifest when absent.
func LoadManifest(path string) (*Manifest, error) {
	m := &Manifest{Links: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, err
	}
	if m.Links == nil {
		m.Links = map[string]string{}
	}
	return m, nil
}

// Save writes the manifest atomically.
func (m *Manifest) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Stable key order for reproducible files.
	ordered := struct {
		Links map[string]string `json:"links"`
	}{Links: m.Links}
	data, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Targets returns the managed target paths in sorted order.
func (m *Manifest) Targets() []string {
	out := make([]string, 0, len(m.Links))
	for k := range m.Links {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
