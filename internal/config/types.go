// Package config defines the layered configuration model for rc, loads and
// merges the global TOML file with legacy host profiles, expands path variables,
// and validates the result into typed values shared by execution and diagnostics.
//
// Merge order is: built-in defaults, global TOML file, common legacy profile,
// lexically sorted multi-host profiles, then the exact host profile. Scalars
// take the last specified value. Domain lists are keyed (links by target, bins by
// target, repositories by path, tasks by name); later layers replace or extend
// earlier entries with the same key, giving the exact host deterministic
// priority.
package config

// Command is a single command in argv or shell form. Exactly one form must be
// set. argv is preferred; shell explicitly runs through the configured shell
// and is required for pipelines.
type Command struct {
	Argv  []string `toml:"argv,omitempty"`
	Shell string   `toml:"shell,omitempty"`
}

// IsZero reports whether neither form is set.
func (c Command) IsZero() bool { return len(c.Argv) == 0 && c.Shell == "" }

// Link describes a managed symlink from a source under a named root to a target.
type Link struct {
	SourceRoot string `toml:"source_root,omitempty"`
	Source     string `toml:"source"`
	Target     string `toml:"target"`
	Optional   bool   `toml:"optional,omitempty"`
	Privileged bool   `toml:"privileged,omitempty"`
}

// Bin describes a managed binary symlink, optionally discovering an adjacent
// Zsh completion file (named "_<name>").
type Bin struct {
	SourceRoot         string `toml:"source_root"`
	Source             string `toml:"source"`
	Target             string `toml:"target"`
	Optional           bool   `toml:"optional,omitempty"`
	DiscoverCompletion bool   `toml:"discover_completion,omitempty"`
}

// Hooks attaches commands to a repository's synchronization lifecycle.
type Hooks struct {
	PostUpdate *Command `toml:"post_update"`
	Always     *Command `toml:"always"`
}

// Repository is an extra Git repository contributed by a host profile.
type Repository struct {
	Path  string `toml:"path"`
	Hooks Hooks  `toml:"hooks"`
}

// DefaultRepo is a globally configured Git repository included in sync.
type DefaultRepo struct {
	Path  string `toml:"path"`
	Clone string `toml:"clone,omitempty"`
	// Optional repositories are only synchronized when their path exists.
	Optional bool `toml:"optional,omitempty"`
}

// BackupTask captures a command whose stdout is written to a tracked file and
// committed when changed.
type BackupTask struct {
	Name      string   `toml:"name"`
	Platforms []string `toml:"platforms"`
	Requires  []string `toml:"requires"`
	Command   Command  `toml:"command"`
	// Repo is the named root of the repository holding Output.
	Repo string `toml:"repo"`
	// Output is the path of the backup file relative to the repo root.
	Output  string `toml:"output"`
	Signoff bool   `toml:"signoff"`
}

// UpdateTask runs one or more maintenance commands when its requirements exist.
type UpdateTask struct {
	Name            string    `toml:"name"`
	Platforms       []string  `toml:"platforms"`
	Requires        []string  `toml:"requires"`
	Commands        []Command `toml:"commands"`
	Dir             string    `toml:"dir"`
	ContinueOnError bool      `toml:"continue_on_error"`
}

// GitConfig holds Git provider and default repositories.
type GitConfig struct {
	Provider     string        `toml:"provider"`
	Repositories []DefaultRepo `toml:"repositories"`
}

// YadmConfig holds the YADM remote and the paths to stage before sync.
type YadmConfig struct {
	Remote string   `toml:"remote"`
	Track  []string `toml:"track"`
}

// SyncConfig holds repository synchronization tuning.
type SyncConfig struct {
	Concurrency int `toml:"concurrency"`
}

// ToolsConfig records interactive tool preferences.
type ToolsConfig struct {
	Lazygit     string `toml:"lazygit"`
	Aicommit    string `toml:"aicommit"`
	Shell       string `toml:"shell"`
	PreferEmacs bool   `toml:"prefer_emacs"`
}

// ToolsLayer records interactive tool preferences from one configuration layer.
// Optional booleans use pointers so an explicit false can override defaults.
type ToolsLayer struct {
	Lazygit     string `toml:"lazygit"`
	Aicommit    string `toml:"aicommit"`
	Shell       string `toml:"shell"`
	PreferEmacs *bool  `toml:"prefer_emacs"`
}

// Endpoint is a doctor connectivity probe.
type Endpoint struct {
	Name string `toml:"name"`
	URL  string `toml:"url"`
}

// DoctorConfig holds connectivity probes and their timeout.
type DoctorConfig struct {
	Endpoints      []Endpoint `toml:"endpoints"`
	TimeoutSeconds int        `toml:"timeout_seconds"`
}

// File is the in-memory representation of any configuration layer. Global TOML
// files populate roots and engine sections; legacy host profiles populate links,
// bins, and repositories. Unset sections are ignored during merge.
type File struct {
	Version int `toml:"version"`

	Roots map[string]string `toml:"roots"`

	Git    GitConfig    `toml:"git"`
	Yadm   YadmConfig   `toml:"yadm"`
	Sync   SyncConfig   `toml:"sync"`
	Tools  ToolsLayer   `toml:"tools"`
	Doctor DoctorConfig `toml:"doctor"`

	Links        []Link       `toml:"links"`
	Bins         []Bin        `toml:"bins"`
	Repositories []Repository `toml:"repositories"`

	Backups []BackupTask `toml:"backups"`
	Updates []UpdateTask `toml:"updates"`
}
