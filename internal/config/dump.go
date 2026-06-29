package config

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

const (
	DumpFormatTOML = "toml"
	DumpFormatJSON = "json"
)

type dumpConfig struct {
	Hostname     string            `toml:"hostname" json:"hostname"`
	Vars         map[string]string `toml:"vars" json:"vars"`
	Roots        map[string]string `toml:"roots" json:"roots"`
	Provider     string            `toml:"provider" json:"provider"`
	Yadm         dumpYadm          `toml:"yadm" json:"yadm"`
	Sync         dumpSync          `toml:"sync" json:"sync"`
	Tools        dumpTools         `toml:"tools" json:"tools"`
	Doctor       dumpDoctor        `toml:"doctor" json:"doctor"`
	Links        []dumpLink        `toml:"links" json:"links"`
	Bins         []dumpBin         `toml:"bins" json:"bins"`
	Repositories []dumpRepository  `toml:"repositories" json:"repositories"`
	Backups      []dumpBackup      `toml:"backups" json:"backups"`
	Updates      []dumpUpdate      `toml:"updates" json:"updates"`
	ManifestPath string            `toml:"manifest_path" json:"manifest_path"`
}

type dumpYadm struct {
	Remote string   `toml:"remote" json:"remote"`
	Track  []string `toml:"track" json:"track"`
}

type dumpSync struct {
	Concurrency int `toml:"concurrency" json:"concurrency"`
}

type dumpTools struct {
	Lazygit     string `toml:"lazygit" json:"lazygit"`
	Aicommit    string `toml:"aicommit" json:"aicommit"`
	Shell       string `toml:"shell" json:"shell"`
	PreferEmacs bool   `toml:"prefer_emacs" json:"prefer_emacs"`
}

type dumpDoctor struct {
	Endpoints      []Endpoint `toml:"endpoints" json:"endpoints"`
	TimeoutSeconds int        `toml:"timeout_seconds" json:"timeout_seconds"`
}

type dumpLink struct {
	Source     string `toml:"source" json:"source"`
	Target     string `toml:"target" json:"target"`
	Optional   bool   `toml:"optional" json:"optional"`
	Privileged bool   `toml:"privileged" json:"privileged"`
}

type dumpBin struct {
	Source             string `toml:"source" json:"source"`
	Target             string `toml:"target" json:"target"`
	Optional           bool   `toml:"optional" json:"optional"`
	DiscoverCompletion bool   `toml:"discover_completion" json:"discover_completion"`
}

type dumpRepository struct {
	Path     string     `toml:"path" json:"path"`
	Clone    string     `toml:"clone" json:"clone"`
	Optional bool       `toml:"optional" json:"optional"`
	Hooks    *dumpHooks `toml:"hooks,omitempty" json:"hooks,omitempty"`
}

type dumpHooks struct {
	PostUpdate *dumpCommand `toml:"post_update,omitempty" json:"post_update,omitempty"`
	Always     *dumpCommand `toml:"always,omitempty" json:"always,omitempty"`
}

type dumpCommand struct {
	Argv  []string `toml:"argv,omitempty" json:"argv,omitempty"`
	Shell string   `toml:"shell,omitempty" json:"shell,omitempty"`
}

type dumpBackup struct {
	Name      string      `toml:"name" json:"name"`
	Platforms []string    `toml:"platforms" json:"platforms"`
	Requires  []string    `toml:"requires" json:"requires"`
	Command   dumpCommand `toml:"command" json:"command"`
	RepoRoot  string      `toml:"repo_root" json:"repo_root"`
	Output    string      `toml:"output" json:"output"`
	Signoff   bool        `toml:"signoff" json:"signoff"`
}

type dumpUpdate struct {
	Name            string        `toml:"name" json:"name"`
	Platforms       []string      `toml:"platforms" json:"platforms"`
	Requires        []string      `toml:"requires" json:"requires"`
	Commands        []dumpCommand `toml:"commands" json:"commands"`
	Dir             string        `toml:"dir" json:"dir"`
	ContinueOnError bool          `toml:"continue_on_error" json:"continue_on_error"`
}

// Dump renders the fully resolved runtime configuration in a stable format.
func Dump(cfg *Config, format string) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cannot dump nil config")
	}
	d := newDumpConfig(cfg)
	switch format {
	case "", DumpFormatTOML:
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(d); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case DumpFormatJSON:
		b, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(b, '\n'), nil
	default:
		return nil, fmt.Errorf("invalid config dump format %q: want toml or json", format)
	}
}

func ValidDumpFormat(format string) bool {
	return format == DumpFormatTOML || format == DumpFormatJSON
}

func newDumpConfig(cfg *Config) dumpConfig {
	return dumpConfig{
		Hostname:     cfg.Hostname,
		Vars:         cloneStringMap(cfg.Vars),
		Roots:        cloneStringMap(cfg.Roots),
		Provider:     cfg.Provider,
		Yadm:         dumpYadm{Remote: cfg.Yadm.Remote, Track: cloneStrings(cfg.Yadm.Track)},
		Sync:         dumpSync{Concurrency: cfg.Concurrency},
		Tools:        dumpTools{Lazygit: cfg.Tools.Lazygit, Aicommit: cfg.Tools.Aicommit, Shell: cfg.Tools.Shell, PreferEmacs: cfg.Tools.PreferEmacs},
		Doctor:       dumpDoctor{Endpoints: cloneEndpoints(cfg.Doctor.Endpoints), TimeoutSeconds: cfg.Doctor.TimeoutSeconds},
		Links:        dumpLinks(cfg.Links),
		Bins:         dumpBins(cfg.Bins),
		Repositories: dumpRepositories(cfg.Repos),
		Backups:      dumpBackups(cfg.Backups),
		Updates:      dumpUpdates(cfg.Updates),
		ManifestPath: cfg.ManifestPath,
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneEndpoints(in []Endpoint) []Endpoint {
	if in == nil {
		return nil
	}
	out := make([]Endpoint, len(in))
	copy(out, in)
	return out
}

func dumpLinks(in []ResolvedLink) []dumpLink {
	out := make([]dumpLink, 0, len(in))
	for _, l := range in {
		out = append(out, dumpLink(l))
	}
	return out
}

func dumpBins(in []ResolvedBin) []dumpBin {
	out := make([]dumpBin, 0, len(in))
	for _, b := range in {
		out = append(out, dumpBin(b))
	}
	return out
}

func dumpRepositories(in []RepoTarget) []dumpRepository {
	out := make([]dumpRepository, 0, len(in))
	for _, r := range in {
		out = append(out, dumpRepository{
			Path:     r.Path,
			Clone:    r.Clone,
			Optional: r.Optional,
			Hooks:    dumpRepoHooks(r.Hooks),
		})
	}
	return out
}

func dumpRepoHooks(h Hooks) *dumpHooks {
	out := &dumpHooks{}
	if h.PostUpdate != nil {
		out.PostUpdate = dumpCommandValue(*h.PostUpdate)
	}
	if h.Always != nil {
		out.Always = dumpCommandValue(*h.Always)
	}
	if out.PostUpdate == nil && out.Always == nil {
		return nil
	}
	return out
}

func dumpCommandValue(c Command) *dumpCommand {
	return &dumpCommand{Argv: cloneStrings(c.Argv), Shell: c.Shell}
}

func dumpBackups(in []ResolvedBackup) []dumpBackup {
	out := make([]dumpBackup, 0, len(in))
	for _, b := range in {
		out = append(out, dumpBackup{
			Name:      b.Name,
			Platforms: cloneStrings(b.Platforms),
			Requires:  cloneStrings(b.Requires),
			Command:   *dumpCommandValue(b.Command),
			RepoRoot:  b.RepoRoot,
			Output:    b.Output,
			Signoff:   b.Signoff,
		})
	}
	return out
}

func dumpUpdates(in []ResolvedUpdate) []dumpUpdate {
	out := make([]dumpUpdate, 0, len(in))
	for _, u := range in {
		commands := make([]dumpCommand, 0, len(u.Commands))
		for _, c := range u.Commands {
			commands = append(commands, *dumpCommandValue(c))
		}
		out = append(out, dumpUpdate{
			Name:            u.Name,
			Platforms:       cloneStrings(u.Platforms),
			Requires:        cloneStrings(u.Requires),
			Commands:        commands,
			Dir:             u.Dir,
			ContinueOnError: u.ContinueOnError,
		})
	}
	return out
}
